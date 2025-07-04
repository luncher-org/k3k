package cmds

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/retry"
)

var (
	name                 string
	cn                   string
	org                  cli.StringSlice
	altNames             cli.StringSlice
	expirationDays       int64
	configName           string
	kubeconfigServerHost string
)

func newGenerateKubeconfigFlags(appCtx *AppContext) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "name",
			Usage:       "cluster name",
			Destination: &name,
		},
		&cli.StringFlag{
			Name:        "config-name",
			Usage:       "the name of the generated kubeconfig file",
			Destination: &configName,
		},
		&cli.StringFlag{
			Name:        "cn",
			Usage:       "Common name (CN) of the generated certificates for the kubeconfig",
			Destination: &cn,
			Value:       controller.AdminCommonName,
		},
		&cli.StringSliceFlag{
			Name:  "org",
			Usage: "Organization name (ORG) of the generated certificates for the kubeconfig",
			Value: &org,
		},
		&cli.StringSliceFlag{
			Name:  "altNames",
			Usage: "altNames of the generated certificates for the kubeconfig",
			Value: &altNames,
		},
		&cli.Int64Flag{
			Name:        "expiration-days",
			Usage:       "Expiration date of the certificates used for the kubeconfig",
			Destination: &expirationDays,
			Value:       356,
		},
		&cli.StringFlag{
			Name:        "kubeconfig-server",
			Usage:       "override the kubeconfig server host",
			Destination: &kubeconfigServerHost,
			Value:       "",
		},
	}
}

func NewKubeconfigCmd(appCtx *AppContext) *cli.Command {
	return &cli.Command{
		Name:  "kubeconfig",
		Usage: "Manage kubeconfig for clusters",
		Subcommands: []*cli.Command{
			NewKubeconfigGenerateCmd(appCtx),
		},
	}
}

func NewKubeconfigGenerateCmd(appCtx *AppContext) *cli.Command {
	flags := CommonFlags(appCtx)
	flags = append(flags, FlagNamespace(appCtx))
	flags = append(flags, newGenerateKubeconfigFlags(appCtx)...)

	return &cli.Command{
		Name:            "generate",
		Usage:           "Generate kubeconfig for clusters",
		SkipFlagParsing: false,
		Action:          generate(appCtx),
		Flags:           flags,
	}
}

func generate(appCtx *AppContext) cli.ActionFunc {
	return func(clx *cli.Context) error {
		ctx := context.Background()
		client := appCtx.Client

		clusterKey := types.NamespacedName{
			Name:      name,
			Namespace: appCtx.Namespace(name),
		}

		var cluster v1alpha1.Cluster

		if err := client.Get(ctx, clusterKey, &cluster); err != nil {
			return err
		}

		url, err := url.Parse(appCtx.RestConfig.Host)
		if err != nil {
			return err
		}

		host := strings.Split(url.Host, ":")
		if kubeconfigServerHost != "" {
			host = []string{kubeconfigServerHost}

			if err := altNames.Set(kubeconfigServerHost); err != nil {
				return err
			}
		}

		certAltNames := certs.AddSANs(altNames.Value())

		orgs := org.Value()
		if orgs == nil {
			orgs = []string{user.SystemPrivilegedGroup}
		}

		cfg := kubeconfig.KubeConfig{
			CN:         cn,
			ORG:        orgs,
			ExpiryDate: time.Hour * 24 * time.Duration(expirationDays),
			AltNames:   certAltNames,
		}

		logrus.Infof("waiting for cluster to be available..")

		var kubeconfig *clientcmdapi.Config

		if err := retry.OnError(controller.Backoff, apierrors.IsNotFound, func() error {
			kubeconfig, err = cfg.Extract(ctx, client, &cluster, host[0])
			return err
		}); err != nil {
			return err
		}

		return writeKubeconfigFile(&cluster, kubeconfig)
	}
}

func writeKubeconfigFile(cluster *v1alpha1.Cluster, kubeconfig *clientcmdapi.Config) error {
	if configName == "" {
		configName = cluster.Namespace + "-" + cluster.Name + "-kubeconfig.yaml"
	}

	pwd, err := os.Getwd()
	if err != nil {
		return err
	}

	logrus.Infof(`You can start using the cluster with:

	export KUBECONFIG=%s
	kubectl cluster-info
	`, filepath.Join(pwd, configName))

	kubeconfigData, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return err
	}

	return os.WriteFile(configName, kubeconfigData, 0644)
}
