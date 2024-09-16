package agent

import (
	"context"
	"fmt"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	"github.com/steved/kubewire/pkg/config"
	"github.com/steved/kubewire/pkg/runnable"
	"github.com/steved/kubewire/pkg/wg"
)

var (
	agentImage        = "agent-image"
	namespace         = "test-namespace"
	objectName        = "test-object"
	relatedObjectName = fmt.Sprintf("wg-%s", objectName)
	selector          = map[string]string{"app.kubernetes.io/name": objectName}
	agentAddr         = netip.MustParseAddrPort("4.5.6.7:19017")
)

func testAgent(t *testing.T, obj runtime.Object, cfg *config.Config, f func(t *testing.T, stop runnable.StopFunc, client kubernetes.Interface, remoteAddr netip.AddrPort)) {
	newRevision = func() string { return "1-2-3-4" }

	waitForLoadBalancerReady = func(_ context.Context, _ cache.Getter, namespace, name string) (*corev1.Service, error) {
		return &corev1.Service{
			ObjectMeta: v1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Status: corev1.ServiceStatus{
				LoadBalancer: corev1.LoadBalancerStatus{
					Ingress: []corev1.LoadBalancerIngress{{Hostname: "example.com"}},
				},
			},
		}, nil
	}

	waitForPod = func(_ context.Context, _ cache.Getter, _ *rest.Config, _ string, _ map[string]string, _ string) (address netip.AddrPort, err error) {
		return agentAddr, nil
	}

	cfg.TargetObject = obj
	cfg.Namespace = namespace
	cfg.AgentImage = agentImage

	client := fake.NewClientset(obj)
	a := NewKubernetesAgent(cfg, client, nil)
	stop, err := a.Start(context.Background())

	assert.NoError(t, err)

	f(t, stop, client, a.AgentAddress())
}

func TestAgentDeployment(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: v1.ObjectMeta{Name: objectName, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(3)),
			Selector: &v1.LabelSelector{
				MatchLabels: selector,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: v1.ObjectMeta{},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "test-container", Image: "test-image"},
					},
				},
			},
		},
	}

	t.Run("deployment", func(t *testing.T) {
		testAgent(t, deployment.DeepCopy(), config.NewConfig(), func(t *testing.T, _ runnable.StopFunc, client kubernetes.Interface, remoteAddr netip.AddrPort) {
			assert.True(t, remoteAddr.IsValid(), "failed to return a valid remote address")

			service, err := client.CoreV1().Services(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					map[string]string{
						"service.beta.kubernetes.io/aws-load-balancer-backend-protocol":                  "tcp",
						"service.beta.kubernetes.io/aws-load-balancer-internal":                          "false",
						"service.beta.kubernetes.io/aws-load-balancer-type":                              "nlb",
						"service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled": "true",
						"cloud.google.com/l4-rbs":                                                        "enabled",
					},
					service.ObjectMeta.Annotations)

				assert.Equal(
					t,
					corev1.ServiceSpec{
						Ports: []corev1.ServicePort{{
							Name:       "wireguard",
							Protocol:   corev1.ProtocolUDP,
							Port:       int32(wg.DefaultWireguardPort),
							TargetPort: intstr.FromInt32(wg.DefaultWireguardPort),
						}},
						Selector:              selector,
						Type:                  corev1.ServiceTypeLoadBalancer,
						ExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyLocal,
						InternalTrafficPolicy: ptr.To(corev1.ServiceInternalTrafficPolicyLocal),
					},
					service.Spec,
				)
			}

			deployment, err := client.AppsV1().Deployments(namespace).Get(context.Background(), objectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					appsv1.DeploymentSpec{
						Selector: &v1.LabelSelector{MatchLabels: selector},
						Replicas: ptr.To(int32(1)),
						Template: corev1.PodTemplateSpec{
							ObjectMeta: v1.ObjectMeta{
								Annotations: map[string]string{
									WireguardRevisionAnnotationName: "1-2-3-4",
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name:            ContainerName,
										Image:           agentImage,
										ImagePullPolicy: corev1.PullAlways,
										// Retain ports in case they're named at the service level
										Ports: nil,
										Env: []corev1.EnvVar{
											{
												Name:  "LOCAL_PORTS_EXCLUDE_PROXY",
												Value: "",
											},
											{
												Name: "ISTIO_INTERCEPTION_MODE",
												ValueFrom: &corev1.EnvVarSource{
													FieldRef: &corev1.ObjectFieldSelector{
														FieldPath: "metadata.annotations['sidecar.istio.io/interceptionMode']",
													},
												},
											},
										},

										SecurityContext: &corev1.SecurityContext{
											Capabilities:           &corev1.Capabilities{Add: []corev1.Capability{"NET_ADMIN"}},
											RunAsUser:              ptr.To(int64(0)),
											RunAsGroup:             ptr.To(int64(0)),
											RunAsNonRoot:           ptr.To(false),
											ReadOnlyRootFilesystem: ptr.To(false),
										},
										VolumeMounts: []corev1.VolumeMount{
											{
												Name:      WireguardConfigVolumeName,
												ReadOnly:  true,
												MountPath: "/app/config",
											},
										},
									},
								},
								Volumes: []corev1.Volume{
									{
										Name: WireguardConfigVolumeName,
										VolumeSource: corev1.VolumeSource{
											Secret: &corev1.SecretVolumeSource{
												SecretName: relatedObjectName,
												Optional:   ptr.To(false),
											},
										},
									},
								},
							},
						},
					},
					deployment.Spec,
				)
			}

			netpol, err := client.NetworkingV1().NetworkPolicies(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					networkingv1.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{MatchLabels: selector},
						Ingress: []networkingv1.NetworkPolicyIngressRule{{
							Ports: []networkingv1.NetworkPolicyPort{{
								Protocol: ptr.To(corev1.ProtocolUDP),
								Port:     ptr.To(intstr.FromInt32(19070)),
							}},
						}},
						PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
					},
					netpol.Spec,
				)
			}

			secret, err := client.CoreV1().Secrets(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				err = yaml.Unmarshal(secret.Data["wg.yml"], &config.Wireguard{})
				assert.NoError(t, err)
			}
		})
	})

	t.Run("deployment with multiple containers", func(t *testing.T) {
		testDeployment := deployment.DeepCopy()
		testDeployment.Spec.Template.Annotations = map[string]string{DefaultContainerAnnotationName: "test-container"}
		testDeployment.Spec.Template.Spec.Containers = []corev1.Container{
			{Name: "istio", Ports: []corev1.ContainerPort{{Name: "istio-proxy", ContainerPort: 15001}}},
			{Name: "test-container", Image: "test-image"},
			{
				Name:           "other-container",
				LivenessProbe:  &corev1.Probe{InitialDelaySeconds: 10},
				ReadinessProbe: &corev1.Probe{InitialDelaySeconds: 11},
				StartupProbe:   &corev1.Probe{InitialDelaySeconds: 12},
				Ports:          []corev1.ContainerPort{{Name: "other", ContainerPort: 12345}},
			},
		}

		testAgent(t, testDeployment, config.NewConfig(), func(t *testing.T, _ runnable.StopFunc, client kubernetes.Interface, _ netip.AddrPort) {
			deployment, err := client.AppsV1().Deployments(namespace).Get(context.Background(), objectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					appsv1.DeploymentSpec{
						Selector: &v1.LabelSelector{MatchLabels: selector},
						Replicas: ptr.To(int32(1)),
						Template: corev1.PodTemplateSpec{
							ObjectMeta: v1.ObjectMeta{
								Annotations: map[string]string{
									WireguardRevisionAnnotationName: "1-2-3-4",
									DefaultContainerAnnotationName:  "test-container",
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{Name: "istio", Ports: []corev1.ContainerPort{{Name: "istio-proxy", ContainerPort: 15001}}},
									{
										Name:            ContainerName,
										Image:           agentImage,
										ImagePullPolicy: corev1.PullAlways,
										// Retain ports in case they're named at the service level
										Ports: nil,
										Env: []corev1.EnvVar{
											{
												Name:  "LOCAL_PORTS_EXCLUDE_PROXY",
												Value: "15001,12345",
											},
											{
												Name: "ISTIO_INTERCEPTION_MODE",
												ValueFrom: &corev1.EnvVarSource{
													FieldRef: &corev1.ObjectFieldSelector{
														FieldPath: "metadata.annotations['sidecar.istio.io/interceptionMode']",
													},
												},
											},
										},
										SecurityContext: &corev1.SecurityContext{
											Capabilities:           &corev1.Capabilities{Add: []corev1.Capability{"NET_ADMIN"}},
											RunAsUser:              ptr.To(int64(0)),
											RunAsGroup:             ptr.To(int64(0)),
											RunAsNonRoot:           ptr.To(false),
											ReadOnlyRootFilesystem: ptr.To(false),
										},
										VolumeMounts: []corev1.VolumeMount{
											{
												Name:      WireguardConfigVolumeName,
												ReadOnly:  true,
												MountPath: "/app/config",
											},
										},
									},
									{
										Name:  "other-container",
										Ports: []corev1.ContainerPort{{Name: "other", ContainerPort: 12345}},
									},
								},
								Volumes: []corev1.Volume{
									{
										Name: WireguardConfigVolumeName,
										VolumeSource: corev1.VolumeSource{
											Secret: &corev1.SecretVolumeSource{
												SecretName: relatedObjectName,
												Optional:   ptr.To(false),
											},
										},
									},
								},
							},
						},
					},
					deployment.Spec,
				)
			}
		})
	})

	t.Run("deployment with existing volume", func(t *testing.T) {
		testDeployment := deployment.DeepCopy()
		testDeployment.Spec.Template.Spec.Volumes = []corev1.Volume{
			{
				Name: WireguardConfigVolumeName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "different-secret-name",
						Optional:   ptr.To(true),
					},
				},
			},
		}

		testAgent(t, testDeployment, config.NewConfig(), func(t *testing.T, _ runnable.StopFunc, client kubernetes.Interface, _ netip.AddrPort) {
			deployment, err := client.AppsV1().Deployments(namespace).Get(context.Background(), objectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					[]corev1.Volume{
						{
							Name: WireguardConfigVolumeName,
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: relatedObjectName,
									Optional:   ptr.To(false),
								},
							},
						},
					},
					deployment.Spec.Template.Spec.Volumes,
				)
			}
		})
	})

	t.Run("deployment with direct", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Wireguard.DirectAccess = true

		testAgent(t, deployment, cfg, func(t *testing.T, _ runnable.StopFunc, client kubernetes.Interface, remoteAddr netip.AddrPort) {
			assert.Equal(t, agentAddr, remoteAddr)

			_, err := client.CoreV1().Services(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.True(t, errors.IsNotFound(err))

			deployment, err := client.AppsV1().Deployments(namespace).Get(context.Background(), objectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					appsv1.DeploymentSpec{
						Selector: &v1.LabelSelector{MatchLabels: selector},
						Replicas: ptr.To(int32(1)),
						Template: corev1.PodTemplateSpec{
							ObjectMeta: v1.ObjectMeta{
								Annotations: map[string]string{
									WireguardRevisionAnnotationName: "1-2-3-4",
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name:            ContainerName,
										Image:           agentImage,
										ImagePullPolicy: corev1.PullAlways,
										// Retain ports in case they're named at the service level
										Ports: nil,
										Env: []corev1.EnvVar{
											{
												Name:  "LOCAL_PORTS_EXCLUDE_PROXY",
												Value: "",
											},
											{
												Name: "ISTIO_INTERCEPTION_MODE",
												ValueFrom: &corev1.EnvVarSource{
													FieldRef: &corev1.ObjectFieldSelector{
														FieldPath: "metadata.annotations['sidecar.istio.io/interceptionMode']",
													},
												},
											},
										},
										SecurityContext: &corev1.SecurityContext{
											Capabilities:           &corev1.Capabilities{Add: []corev1.Capability{"NET_ADMIN"}},
											RunAsUser:              ptr.To(int64(0)),
											RunAsGroup:             ptr.To(int64(0)),
											RunAsNonRoot:           ptr.To(false),
											ReadOnlyRootFilesystem: ptr.To(false),
										},
										VolumeMounts: []corev1.VolumeMount{
											{
												Name:      WireguardConfigVolumeName,
												ReadOnly:  true,
												MountPath: "/app/config",
											},
										},
									},
								},
								Volumes: []corev1.Volume{
									{
										Name: WireguardConfigVolumeName,
										VolumeSource: corev1.VolumeSource{
											Secret: &corev1.SecretVolumeSource{
												SecretName: relatedObjectName,
												Optional:   ptr.To(false),
											},
										},
									},
								},
							},
						},
					},
					deployment.Spec,
				)
			}

			netpol, err := client.NetworkingV1().NetworkPolicies(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					networkingv1.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{MatchLabels: selector},
						Ingress: []networkingv1.NetworkPolicyIngressRule{{
							Ports: []networkingv1.NetworkPolicyPort{{
								Protocol: ptr.To(corev1.ProtocolUDP),
								Port:     ptr.To(intstr.FromInt32(19017)),
							}},
						}},
						PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
					},
					netpol.Spec,
				)
			}

			secret, err := client.CoreV1().Secrets(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				err = yaml.Unmarshal(secret.Data["wg.yml"], &config.Wireguard{})
				assert.NoError(t, err)
			}
		})
	})

	t.Run("deployment with local address", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Wireguard.LocalAddress = netip.MustParseAddrPort("7.6.5.4:33010")

		testAgent(t, deployment, cfg, func(t *testing.T, _ runnable.StopFunc, client kubernetes.Interface, remoteAddr netip.AddrPort) {
			assert.True(t, !remoteAddr.IsValid(), "agent address expected to be nil")

			_, err := client.CoreV1().Services(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.True(t, errors.IsNotFound(err))

			_, err = client.NetworkingV1().NetworkPolicies(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.True(t, errors.IsNotFound(err))

			deployment, err := client.AppsV1().Deployments(namespace).Get(context.Background(), objectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					appsv1.DeploymentSpec{
						Selector: &v1.LabelSelector{MatchLabels: selector},
						Replicas: ptr.To(int32(1)),
						Template: corev1.PodTemplateSpec{
							ObjectMeta: v1.ObjectMeta{
								Annotations: map[string]string{
									WireguardRevisionAnnotationName: "1-2-3-4",
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name:            ContainerName,
										Image:           agentImage,
										ImagePullPolicy: corev1.PullAlways,
										// Retain ports in case they're named at the service level
										Ports: nil,
										Env: []corev1.EnvVar{
											{
												Name:  "LOCAL_PORTS_EXCLUDE_PROXY",
												Value: "",
											},
											{
												Name: "ISTIO_INTERCEPTION_MODE",
												ValueFrom: &corev1.EnvVarSource{
													FieldRef: &corev1.ObjectFieldSelector{
														FieldPath: "metadata.annotations['sidecar.istio.io/interceptionMode']",
													},
												},
											},
										},
										SecurityContext: &corev1.SecurityContext{
											Capabilities:           &corev1.Capabilities{Add: []corev1.Capability{"NET_ADMIN"}},
											RunAsUser:              ptr.To(int64(0)),
											RunAsGroup:             ptr.To(int64(0)),
											RunAsNonRoot:           ptr.To(false),
											ReadOnlyRootFilesystem: ptr.To(false),
										},
										VolumeMounts: []corev1.VolumeMount{
											{
												Name:      WireguardConfigVolumeName,
												ReadOnly:  true,
												MountPath: "/app/config",
											},
										},
									},
								},
								Volumes: []corev1.Volume{
									{
										Name: WireguardConfigVolumeName,
										VolumeSource: corev1.VolumeSource{
											Secret: &corev1.SecretVolumeSource{
												SecretName: relatedObjectName,
												Optional:   ptr.To(false),
											},
										},
									},
								},
							},
						},
					},
					deployment.Spec,
				)
			}

			secret, err := client.CoreV1().Secrets(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				err = yaml.Unmarshal(secret.Data["wg.yml"], &config.Wireguard{})
				assert.NoError(t, err)
			}
		})
	})

	t.Run("deployment down", func(t *testing.T) {
		cfg := config.NewConfig()

		testAgent(t, deployment, cfg, func(t *testing.T, stop runnable.StopFunc, client kubernetes.Interface, _ netip.AddrPort) {
			cfg.KeepResources = true

			stop()

			_, err := client.CoreV1().Services(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.NoError(t, err)

			_, err = client.NetworkingV1().NetworkPolicies(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.NoError(t, err)

			_, err = client.CoreV1().Secrets(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.NoError(t, err)

			cfg.KeepResources = false

			stop()

			_, err = client.CoreV1().Services(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.True(t, errors.IsNotFound(err))

			_, err = client.NetworkingV1().NetworkPolicies(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.True(t, errors.IsNotFound(err))

			_, err = client.CoreV1().Secrets(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.True(t, errors.IsNotFound(err))
		})
	})
}

func TestAgentStatefulset(t *testing.T) {
	statefulset := &appsv1.StatefulSet{
		ObjectMeta: v1.ObjectMeta{Name: objectName, Namespace: namespace},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To(int32(3)),
			Selector: &v1.LabelSelector{
				MatchLabels: selector,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: v1.ObjectMeta{},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "test-container", Image: "test-image"},
					},
				},
			},
		},
	}

	t.Run("statefulset", func(t *testing.T) {
		testAgent(t, statefulset.DeepCopy(), config.NewConfig(), func(t *testing.T, _ runnable.StopFunc, client kubernetes.Interface, remoteAddr netip.AddrPort) {
			assert.True(t, remoteAddr.IsValid(), "failed to return a valid remote address")

			service, err := client.CoreV1().Services(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					map[string]string{
						"service.beta.kubernetes.io/aws-load-balancer-backend-protocol":                  "tcp",
						"service.beta.kubernetes.io/aws-load-balancer-internal":                          "false",
						"service.beta.kubernetes.io/aws-load-balancer-type":                              "nlb",
						"service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled": "true",
						"cloud.google.com/l4-rbs":                                                        "enabled",
					},
					service.ObjectMeta.Annotations)

				assert.Equal(
					t,
					corev1.ServiceSpec{
						Ports: []corev1.ServicePort{{
							Name:       "wireguard",
							Protocol:   corev1.ProtocolUDP,
							Port:       int32(wg.DefaultWireguardPort),
							TargetPort: intstr.FromInt32(wg.DefaultWireguardPort),
						}},
						Selector:              selector,
						Type:                  corev1.ServiceTypeLoadBalancer,
						ExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyLocal,
						InternalTrafficPolicy: ptr.To(corev1.ServiceInternalTrafficPolicyLocal),
					},
					service.Spec,
				)
			}

			sts, err := client.AppsV1().StatefulSets(namespace).Get(context.Background(), objectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					appsv1.StatefulSetSpec{
						Selector: &v1.LabelSelector{MatchLabels: selector},
						Replicas: ptr.To(int32(1)),
						Template: corev1.PodTemplateSpec{
							ObjectMeta: v1.ObjectMeta{
								Annotations: map[string]string{
									WireguardRevisionAnnotationName: "1-2-3-4",
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name:            ContainerName,
										Image:           agentImage,
										ImagePullPolicy: corev1.PullAlways,
										// Retain ports in case they're named at the service level
										Ports: nil,
										Env: []corev1.EnvVar{
											{
												Name:  "LOCAL_PORTS_EXCLUDE_PROXY",
												Value: "",
											},

											{
												Name: "ISTIO_INTERCEPTION_MODE",
												ValueFrom: &corev1.EnvVarSource{
													FieldRef: &corev1.ObjectFieldSelector{
														FieldPath: "metadata.annotations['sidecar.istio.io/interceptionMode']",
													},
												},
											},
										},
										SecurityContext: &corev1.SecurityContext{
											Capabilities:           &corev1.Capabilities{Add: []corev1.Capability{"NET_ADMIN"}},
											RunAsUser:              ptr.To(int64(0)),
											RunAsGroup:             ptr.To(int64(0)),
											RunAsNonRoot:           ptr.To(false),
											ReadOnlyRootFilesystem: ptr.To(false),
										},
										VolumeMounts: []corev1.VolumeMount{
											{
												Name:      WireguardConfigVolumeName,
												ReadOnly:  true,
												MountPath: "/app/config",
											},
										},
									},
								},
								Volumes: []corev1.Volume{
									{
										Name: WireguardConfigVolumeName,
										VolumeSource: corev1.VolumeSource{
											Secret: &corev1.SecretVolumeSource{
												SecretName: relatedObjectName,
												Optional:   ptr.To(false),
											},
										},
									},
								},
							},
						},
					},
					sts.Spec,
				)
			}

			netpol, err := client.NetworkingV1().NetworkPolicies(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					networkingv1.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{MatchLabels: selector},
						Ingress: []networkingv1.NetworkPolicyIngressRule{{
							Ports: []networkingv1.NetworkPolicyPort{{
								Protocol: ptr.To(corev1.ProtocolUDP),
								Port:     ptr.To(intstr.FromInt32(19070)),
							}},
						}},
						PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
					},
					netpol.Spec,
				)
			}

			secret, err := client.CoreV1().Secrets(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				err = yaml.Unmarshal(secret.Data["wg.yml"], &config.Wireguard{})
				assert.NoError(t, err)
			}
		})
	})

	t.Run("statefulset with multiple containers", func(t *testing.T) {
		testStatefulset := statefulset.DeepCopy()
		testStatefulset.Spec.Template.Annotations = map[string]string{DefaultContainerAnnotationName: "test-container"}
		testStatefulset.Spec.Template.Spec.Containers = []corev1.Container{
			{Name: "istio", Ports: []corev1.ContainerPort{{Name: "istio-proxy", ContainerPort: 15001}}},
			{Name: "test-container", Image: "test-image"},
			{
				Name:           "other-container",
				LivenessProbe:  &corev1.Probe{InitialDelaySeconds: 10},
				ReadinessProbe: &corev1.Probe{InitialDelaySeconds: 11},
				StartupProbe:   &corev1.Probe{InitialDelaySeconds: 12},
				Ports:          []corev1.ContainerPort{{Name: "other", ContainerPort: 12345}},
			},
		}

		testAgent(t, testStatefulset, config.NewConfig(), func(t *testing.T, _ runnable.StopFunc, client kubernetes.Interface, _ netip.AddrPort) {
			sts, err := client.AppsV1().StatefulSets(namespace).Get(context.Background(), objectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					appsv1.StatefulSetSpec{
						Selector: &v1.LabelSelector{MatchLabels: selector},
						Replicas: ptr.To(int32(1)),
						Template: corev1.PodTemplateSpec{
							ObjectMeta: v1.ObjectMeta{
								Annotations: map[string]string{
									WireguardRevisionAnnotationName: "1-2-3-4",
									DefaultContainerAnnotationName:  "test-container",
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{Name: "istio", Ports: []corev1.ContainerPort{{Name: "istio-proxy", ContainerPort: 15001}}},
									{
										Name:            ContainerName,
										Image:           agentImage,
										ImagePullPolicy: corev1.PullAlways,
										// Retain ports in case they're named at the service level
										Ports: nil,
										Env: []corev1.EnvVar{
											{
												Name:  "LOCAL_PORTS_EXCLUDE_PROXY",
												Value: "15001,12345",
											},
											{
												Name: "ISTIO_INTERCEPTION_MODE",
												ValueFrom: &corev1.EnvVarSource{
													FieldRef: &corev1.ObjectFieldSelector{
														FieldPath: "metadata.annotations['sidecar.istio.io/interceptionMode']",
													},
												},
											},
										},
										SecurityContext: &corev1.SecurityContext{
											Capabilities:           &corev1.Capabilities{Add: []corev1.Capability{"NET_ADMIN"}},
											RunAsUser:              ptr.To(int64(0)),
											RunAsGroup:             ptr.To(int64(0)),
											RunAsNonRoot:           ptr.To(false),
											ReadOnlyRootFilesystem: ptr.To(false),
										},
										VolumeMounts: []corev1.VolumeMount{
											{
												Name:      WireguardConfigVolumeName,
												ReadOnly:  true,
												MountPath: "/app/config",
											},
										},
									},
									{
										Name:  "other-container",
										Ports: []corev1.ContainerPort{{Name: "other", ContainerPort: 12345}},
									},
								},
								Volumes: []corev1.Volume{
									{
										Name: WireguardConfigVolumeName,
										VolumeSource: corev1.VolumeSource{
											Secret: &corev1.SecretVolumeSource{
												SecretName: relatedObjectName,
												Optional:   ptr.To(false),
											},
										},
									},
								},
							},
						},
					},
					sts.Spec,
				)
			}
		})
	})

	t.Run("deployment with existing volume", func(t *testing.T) {
		testStatefulset := statefulset.DeepCopy()
		testStatefulset.Spec.Template.Spec.Volumes = []corev1.Volume{
			{
				Name: WireguardConfigVolumeName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "different-secret-name",
						Optional:   ptr.To(true),
					},
				},
			},
		}

		testAgent(t, testStatefulset, config.NewConfig(), func(t *testing.T, _ runnable.StopFunc, client kubernetes.Interface, _ netip.AddrPort) {
			sts, err := client.AppsV1().StatefulSets(namespace).Get(context.Background(), objectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					[]corev1.Volume{
						{
							Name: WireguardConfigVolumeName,
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: relatedObjectName,
									Optional:   ptr.To(false),
								},
							},
						},
					},
					sts.Spec.Template.Spec.Volumes,
				)
			}
		})
	})

	t.Run("statefulset with direct", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Wireguard.DirectAccess = true

		testAgent(t, statefulset, cfg, func(t *testing.T, _ runnable.StopFunc, client kubernetes.Interface, remoteAddr netip.AddrPort) {
			assert.Equal(t, agentAddr, remoteAddr)

			_, err := client.CoreV1().Services(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.True(t, errors.IsNotFound(err))

			sts, err := client.AppsV1().StatefulSets(namespace).Get(context.Background(), objectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					appsv1.StatefulSetSpec{
						Selector: &v1.LabelSelector{MatchLabels: selector},
						Replicas: ptr.To(int32(1)),
						Template: corev1.PodTemplateSpec{
							ObjectMeta: v1.ObjectMeta{
								Annotations: map[string]string{
									WireguardRevisionAnnotationName: "1-2-3-4",
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name:            ContainerName,
										Image:           agentImage,
										ImagePullPolicy: corev1.PullAlways,
										// Retain ports in case they're named at the service level
										Ports: nil,
										Env: []corev1.EnvVar{
											{
												Name:  "LOCAL_PORTS_EXCLUDE_PROXY",
												Value: "",
											},
											{
												Name: "ISTIO_INTERCEPTION_MODE",
												ValueFrom: &corev1.EnvVarSource{
													FieldRef: &corev1.ObjectFieldSelector{
														FieldPath: "metadata.annotations['sidecar.istio.io/interceptionMode']",
													},
												},
											},
										},
										SecurityContext: &corev1.SecurityContext{
											Capabilities:           &corev1.Capabilities{Add: []corev1.Capability{"NET_ADMIN"}},
											RunAsUser:              ptr.To(int64(0)),
											RunAsGroup:             ptr.To(int64(0)),
											RunAsNonRoot:           ptr.To(false),
											ReadOnlyRootFilesystem: ptr.To(false),
										},
										VolumeMounts: []corev1.VolumeMount{
											{
												Name:      WireguardConfigVolumeName,
												ReadOnly:  true,
												MountPath: "/app/config",
											},
										},
									},
								},
								Volumes: []corev1.Volume{
									{
										Name: WireguardConfigVolumeName,
										VolumeSource: corev1.VolumeSource{
											Secret: &corev1.SecretVolumeSource{
												SecretName: relatedObjectName,
												Optional:   ptr.To(false),
											},
										},
									},
								},
							},
						},
					},
					sts.Spec,
				)
			}

			netpol, err := client.NetworkingV1().NetworkPolicies(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					networkingv1.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{MatchLabels: selector},
						Ingress: []networkingv1.NetworkPolicyIngressRule{{
							Ports: []networkingv1.NetworkPolicyPort{{
								Protocol: ptr.To(corev1.ProtocolUDP),
								Port:     ptr.To(intstr.FromInt32(19017)),
							}},
						}},
						PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
					},
					netpol.Spec,
				)
			}

			secret, err := client.CoreV1().Secrets(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				err = yaml.Unmarshal(secret.Data["wg.yml"], &config.Wireguard{})
				assert.NoError(t, err)
			}
		})
	})

	t.Run("statefulset with local address", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Wireguard.LocalAddress = netip.MustParseAddrPort("7.6.5.4:33010")

		testAgent(t, statefulset, cfg, func(t *testing.T, _ runnable.StopFunc, client kubernetes.Interface, remoteAddr netip.AddrPort) {
			assert.True(t, !remoteAddr.IsValid(), "agent address expected to be nil")

			_, err := client.CoreV1().Services(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.True(t, errors.IsNotFound(err))

			_, err = client.NetworkingV1().NetworkPolicies(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.True(t, errors.IsNotFound(err))

			sts, err := client.AppsV1().StatefulSets(namespace).Get(context.Background(), objectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Equal(
					t,
					appsv1.StatefulSetSpec{
						Selector: &v1.LabelSelector{MatchLabels: selector},
						Replicas: ptr.To(int32(1)),
						Template: corev1.PodTemplateSpec{
							ObjectMeta: v1.ObjectMeta{
								Annotations: map[string]string{
									WireguardRevisionAnnotationName: "1-2-3-4",
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name:            ContainerName,
										Image:           agentImage,
										ImagePullPolicy: corev1.PullAlways,
										// Retain ports in case they're named at the service level
										Ports: nil,
										Env: []corev1.EnvVar{
											{
												Name:  "LOCAL_PORTS_EXCLUDE_PROXY",
												Value: "",
											},
											{
												Name: "ISTIO_INTERCEPTION_MODE",
												ValueFrom: &corev1.EnvVarSource{
													FieldRef: &corev1.ObjectFieldSelector{
														FieldPath: "metadata.annotations['sidecar.istio.io/interceptionMode']",
													},
												},
											},
										},
										SecurityContext: &corev1.SecurityContext{
											Capabilities:           &corev1.Capabilities{Add: []corev1.Capability{"NET_ADMIN"}},
											RunAsUser:              ptr.To(int64(0)),
											RunAsGroup:             ptr.To(int64(0)),
											RunAsNonRoot:           ptr.To(false),
											ReadOnlyRootFilesystem: ptr.To(false),
										},
										VolumeMounts: []corev1.VolumeMount{
											{
												Name:      WireguardConfigVolumeName,
												ReadOnly:  true,
												MountPath: "/app/config",
											},
										},
									},
								},
								Volumes: []corev1.Volume{
									{
										Name: WireguardConfigVolumeName,
										VolumeSource: corev1.VolumeSource{
											Secret: &corev1.SecretVolumeSource{
												SecretName: relatedObjectName,
												Optional:   ptr.To(false),
											},
										},
									},
								},
							},
						},
					},
					sts.Spec,
				)
			}

			secret, err := client.CoreV1().Secrets(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			if assert.NoError(t, err) {
				err = yaml.Unmarshal(secret.Data["wg.yml"], &config.Wireguard{})
				assert.NoError(t, err)
			}
		})
	})

	t.Run("statefulset stop", func(t *testing.T) {
		cfg := config.NewConfig()

		testAgent(t, statefulset, cfg, func(t *testing.T, stop runnable.StopFunc, client kubernetes.Interface, _ netip.AddrPort) {
			cfg.KeepResources = true

			stop()

			_, err := client.CoreV1().Services(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.NoError(t, err)

			_, err = client.NetworkingV1().NetworkPolicies(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.NoError(t, err)

			_, err = client.CoreV1().Secrets(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.NoError(t, err)

			cfg.KeepResources = false

			stop()

			_, err = client.CoreV1().Services(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.True(t, errors.IsNotFound(err))

			_, err = client.NetworkingV1().NetworkPolicies(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.True(t, errors.IsNotFound(err))

			_, err = client.CoreV1().Secrets(namespace).Get(context.Background(), relatedObjectName, v1.GetOptions{})
			assert.True(t, errors.IsNotFound(err))
		})
	})
}

func Test_containerIndexOrDefault(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		annotations   map[string]string
		containers    []corev1.Container
		want          int
	}{
		{
			"no containers",
			"first",
			map[string]string{},
			[]corev1.Container{},
			-1,
		},
		{
			"no containers, no name",
			"",
			map[string]string{},
			[]corev1.Container{},
			-1,
		},
		{
			"one container",
			"first",
			map[string]string{},
			[]corev1.Container{
				{Name: "first"},
			},
			0,
		},
		{
			"empty annotation",
			"",
			map[string]string{DefaultContainerAnnotationName: ""},
			[]corev1.Container{
				{Name: "first"},
			},
			0,
		},
		{
			"annotation with missing pod",
			"",
			map[string]string{DefaultContainerAnnotationName: "second"},
			[]corev1.Container{
				{Name: "first"},
			},
			-1,
		},
		{
			"annotation",
			"",
			map[string]string{DefaultContainerAnnotationName: "second"},
			[]corev1.Container{
				{Name: "first"},
				{Name: "second"},
			},
			1,
		},
		{
			"missing",
			"third",
			map[string]string{},
			[]corev1.Container{
				{Name: "first"},
				{Name: "second"},
			},
			-1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containerIndexOrDefault(tt.containerName, tt.annotations, tt.containers); got != tt.want {
				t.Errorf("containerIndexOrDefault() = %v, want %v", got, tt.want)
			}
		})
	}
}
