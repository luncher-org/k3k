package cmds

import (
	"context"
	"errors"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
)

var keepData bool

func NewClusterDeleteCmd(appCtx *AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete",
		Short:   "Delete an existing cluster",
		Example: "k3kcli cluster delete [command options] NAME",
		RunE:    delete(appCtx),
		Args:    cobra.ExactArgs(1),
	}

	CobraFlagNamespace(appCtx, cmd.Flags())
	cmd.Flags().BoolVar(&keepData, "keep-data", false, "keeps persistence volumes created for the cluster after deletion")

	return cmd
}

func delete(appCtx *AppContext) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := appCtx.Client
		name := args[0]

		if name == k3kcluster.ClusterInvalidName {
			return errors.New("invalid cluster name")
		}

		namespace := appCtx.Namespace(name)

		logrus.Infof("Deleting [%s] cluster in namespace [%s]", name, namespace)

		cluster := v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
		}
		// keep bootstrap secrets and tokens if --keep-data flag is passed
		if keepData {
			// skip removing tokenSecret
			if err := RemoveOwnerReferenceFromSecret(ctx, k3kcluster.TokenSecretName(cluster.Name), client, cluster); err != nil {
				return err
			}

			// skip removing webhook secret
			if err := RemoveOwnerReferenceFromSecret(ctx, agent.WebhookSecretName(cluster.Name), client, cluster); err != nil {
				return err
			}
		} else {
			matchingLabels := ctrlclient.MatchingLabels(map[string]string{"cluster": cluster.Name, "role": "server"})
			listOpts := ctrlclient.ListOptions{Namespace: cluster.Namespace}
			matchingLabels.ApplyToList(&listOpts)
			deleteOpts := &ctrlclient.DeleteAllOfOptions{ListOptions: listOpts}

			if err := client.DeleteAllOf(ctx, &v1.PersistentVolumeClaim{}, deleteOpts); err != nil {
				return ctrlclient.IgnoreNotFound(err)
			}
		}

		if err := client.Delete(ctx, &cluster); err != nil {
			return ctrlclient.IgnoreNotFound(err)
		}

		return nil
	}
}

func RemoveOwnerReferenceFromSecret(ctx context.Context, name string, cl ctrlclient.Client, cluster v1alpha1.Cluster) error {
	var secret v1.Secret

	key := types.NamespacedName{
		Name:      name,
		Namespace: cluster.Namespace,
	}

	if err := cl.Get(ctx, key, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			logrus.Warnf("%s secret is not found", name)
			return nil
		}

		return err
	}

	if controllerutil.HasControllerReference(&secret) {
		if err := controllerutil.RemoveOwnerReference(&cluster, &secret, cl.Scheme()); err != nil {
			return err
		}

		return cl.Update(ctx, &secret)
	}

	return nil
}
