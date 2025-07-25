package cluster_test

import (
	"context"
	"fmt"
	"time"

	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcontroller "github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/server"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Cluster Controller", Label("controller"), Label("Cluster"), func() {
	Context("creating a Cluster", func() {
		var (
			namespace string
			ctx       context.Context
		)

		BeforeEach(func() {
			ctx = context.Background()

			createdNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "ns-"}}
			err := k8sClient.Create(context.Background(), createdNS)
			Expect(err).To(Not(HaveOccurred()))
			namespace = createdNS.Name
		})

		When("creating a Cluster", func() {
			It("will be created with some defaults", func() {
				cluster := &v1alpha1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "cluster-",
						Namespace:    namespace,
					},
				}

				err := k8sClient.Create(ctx, cluster)
				Expect(err).To(Not(HaveOccurred()))

				Expect(cluster.Spec.Mode).To(Equal(v1alpha1.SharedClusterMode))
				Expect(cluster.Spec.Agents).To(Equal(ptr.To[int32](0)))
				Expect(cluster.Spec.Servers).To(Equal(ptr.To[int32](1)))
				Expect(cluster.Spec.Version).To(BeEmpty())

				Expect(cluster.Spec.Persistence.Type).To(Equal(v1alpha1.DynamicPersistenceMode))
				Expect(cluster.Spec.Persistence.StorageRequestSize).To(Equal("1G"))

				Expect(cluster.Status.Phase).To(Equal(v1alpha1.ClusterUnknown))

				serverVersion, err := k8s.ServerVersion()
				Expect(err).To(Not(HaveOccurred()))
				expectedHostVersion := fmt.Sprintf("%s-k3s1", serverVersion.GitVersion)

				Eventually(func() string {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)
					Expect(err).To(Not(HaveOccurred()))
					return cluster.Status.HostVersion
				}).
					WithTimeout(time.Second * 30).
					WithPolling(time.Second).
					Should(Equal(expectedHostVersion))

				// check NetworkPolicy
				expectedNetworkPolicy := &networkingv1.NetworkPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      k3kcontroller.SafeConcatNameWithPrefix(cluster.Name),
						Namespace: cluster.Namespace,
					},
				}

				err = k8sClient.Get(ctx, client.ObjectKeyFromObject(expectedNetworkPolicy), expectedNetworkPolicy)
				Expect(err).To(Not(HaveOccurred()))

				spec := expectedNetworkPolicy.Spec
				Expect(spec.PolicyTypes).To(HaveLen(2))
				Expect(spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeEgress))
				Expect(spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeIngress))

				Expect(spec.Ingress).To(Equal([]networkingv1.NetworkPolicyIngressRule{{}}))
			})

			When("exposing the cluster with nodePort", func() {
				It("will have a NodePort service", func() {
					cluster := &v1alpha1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "cluster-",
							Namespace:    namespace,
						},
						Spec: v1alpha1.ClusterSpec{
							Expose: &v1alpha1.ExposeConfig{
								NodePort: &v1alpha1.NodePortConfig{},
							},
						},
					}

					Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

					var service corev1.Service

					Eventually(func() corev1.ServiceType {
						serviceKey := client.ObjectKey{
							Name:      server.ServiceName(cluster.Name),
							Namespace: cluster.Namespace,
						}

						err := k8sClient.Get(ctx, serviceKey, &service)
						Expect(client.IgnoreNotFound(err)).To(Not(HaveOccurred()))
						return service.Spec.Type
					}).
						WithTimeout(time.Second * 30).
						WithPolling(time.Second).
						Should(Equal(corev1.ServiceTypeNodePort))
				})

				It("will have the specified ports exposed when specified", func() {
					cluster := &v1alpha1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "cluster-",
							Namespace:    namespace,
						},
						Spec: v1alpha1.ClusterSpec{
							Expose: &v1alpha1.ExposeConfig{
								NodePort: &v1alpha1.NodePortConfig{
									ServerPort: ptr.To[int32](30010),
									ETCDPort:   ptr.To[int32](30011),
								},
							},
						},
					}

					Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

					var service corev1.Service

					Eventually(func() corev1.ServiceType {
						serviceKey := client.ObjectKey{
							Name:      server.ServiceName(cluster.Name),
							Namespace: cluster.Namespace,
						}

						err := k8sClient.Get(ctx, serviceKey, &service)
						Expect(client.IgnoreNotFound(err)).To(Not(HaveOccurred()))
						return service.Spec.Type
					}).
						WithTimeout(time.Second * 30).
						WithPolling(time.Second).
						Should(Equal(corev1.ServiceTypeNodePort))

					servicePorts := service.Spec.Ports
					Expect(servicePorts).NotTo(BeEmpty())
					Expect(servicePorts).To(HaveLen(2))

					serverPort := servicePorts[0]
					Expect(serverPort.Name).To(Equal("k3s-server-port"))
					Expect(serverPort.Port).To(BeEquivalentTo(443))
					Expect(serverPort.NodePort).To(BeEquivalentTo(30010))

					etcdPort := servicePorts[1]
					Expect(etcdPort.Name).To(Equal("k3s-etcd-port"))
					Expect(etcdPort.Port).To(BeEquivalentTo(2379))
					Expect(etcdPort.NodePort).To(BeEquivalentTo(30011))
				})

				It("will not expose the port when out of range", func() {
					cluster := &v1alpha1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "cluster-",
							Namespace:    namespace,
						},
						Spec: v1alpha1.ClusterSpec{
							Expose: &v1alpha1.ExposeConfig{
								NodePort: &v1alpha1.NodePortConfig{
									ETCDPort: ptr.To[int32](2222),
								},
							},
						},
					}

					Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

					var service corev1.Service

					Eventually(func() corev1.ServiceType {
						serviceKey := client.ObjectKey{
							Name:      server.ServiceName(cluster.Name),
							Namespace: cluster.Namespace,
						}

						err := k8sClient.Get(ctx, serviceKey, &service)
						Expect(client.IgnoreNotFound(err)).To(Not(HaveOccurred()))
						return service.Spec.Type
					}).
						WithTimeout(time.Second * 30).
						WithPolling(time.Second).
						Should(Equal(corev1.ServiceTypeNodePort))

					servicePorts := service.Spec.Ports
					Expect(servicePorts).NotTo(BeEmpty())
					Expect(servicePorts).To(HaveLen(1))

					serverPort := servicePorts[0]
					Expect(serverPort.Name).To(Equal("k3s-server-port"))
					Expect(serverPort.Port).To(BeEquivalentTo(443))
					Expect(serverPort.TargetPort.IntValue()).To(BeEquivalentTo(6443))
				})
			})

			When("exposing the cluster with loadbalancer", func() {
				It("will have a LoadBalancer service with the default ports exposed", func() {
					cluster := &v1alpha1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "cluster-",
							Namespace:    namespace,
						},
						Spec: v1alpha1.ClusterSpec{
							Expose: &v1alpha1.ExposeConfig{
								LoadBalancer: &v1alpha1.LoadBalancerConfig{},
							},
						},
					}

					Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

					var service corev1.Service

					Eventually(func() error {
						serviceKey := client.ObjectKey{
							Name:      server.ServiceName(cluster.Name),
							Namespace: cluster.Namespace,
						}

						return k8sClient.Get(ctx, serviceKey, &service)
					}).
						WithTimeout(time.Second * 30).
						WithPolling(time.Second).
						Should(Succeed())

					Expect(service.Spec.Type).To(Equal(corev1.ServiceTypeLoadBalancer))

					servicePorts := service.Spec.Ports
					Expect(servicePorts).NotTo(BeEmpty())
					Expect(servicePorts).To(HaveLen(2))

					serverPort := servicePorts[0]
					Expect(serverPort.Name).To(Equal("k3s-server-port"))
					Expect(serverPort.Port).To(BeEquivalentTo(443))
					Expect(serverPort.TargetPort.IntValue()).To(BeEquivalentTo(6443))

					etcdPort := servicePorts[1]
					Expect(etcdPort.Name).To(Equal("k3s-etcd-port"))
					Expect(etcdPort.Port).To(BeEquivalentTo(2379))
					Expect(etcdPort.TargetPort.IntValue()).To(BeEquivalentTo(2379))
				})
			})
		})
	})
})
