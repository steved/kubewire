package agent

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	corev1apply "k8s.io/client-go/applyconfigurations/core/v1"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	netv1apply "k8s.io/client-go/applyconfigurations/networking/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
	"k8s.io/utils/ptr"

	"github.com/steved/kubewire/pkg/config"
	"github.com/steved/kubewire/pkg/kuberneteshelpers"
	"github.com/steved/kubewire/pkg/runnable"
	"github.com/steved/kubewire/pkg/wg"
)

const (
	DefaultContainerAnnotationName  = "kubectl.kubernetes.io/default-container"
	FieldManager                    = "wireguard"
	WireguardRevisionAnnotationName = "wgko.io/revision"
	WaitTimeout                     = 5 * time.Minute
	WireguardConfigVolumeName       = "wireguard-config"
	ContainerAddressPath            = "/app/address"
	ContainerName                   = "agent"
)

type Agent interface {
	runnable.Runnable
	AgentAddress() netip.AddrPort
}

type kubernetesAgent struct {
	config     *config.Config
	client     kubernetes.Interface
	restConfig *rest.Config

	agentAddress netip.AddrPort
}

func NewKubernetesAgent(config *config.Config, client kubernetes.Interface, restConfig *rest.Config) Agent {
	return &kubernetesAgent{config: config, client: client, restConfig: restConfig}
}

func (a *kubernetesAgent) AgentAddress() netip.AddrPort {
	return a.agentAddress
}

func (a *kubernetesAgent) Start(ctx context.Context) (runnable.StopFunc, error) {
	var (
		matchLabels           map[string]string
		replaceContainerIndex int
		revision              = newRevision()
	)

	log := logr.FromContextOrDiscard(ctx)
	accessor := meta.NewAccessor()

	objectName, err := accessor.Name(a.config.TargetObject)
	if err != nil {
		return nil, fmt.Errorf("unable to determine target object name: %w", err)
	}

	relatedObjectName := wgObjectName(objectName)

	switch targetObject := a.config.TargetObject.(type) {
	case *appsv1.Deployment:
		matchLabels = targetObject.Spec.Selector.MatchLabels

		if targetObject.Spec.Template.Annotations == nil {
			targetObject.Spec.Template.Annotations = make(map[string]string)
		}

		targetObject.Spec.Template.Annotations[WireguardRevisionAnnotationName] = revision
		targetObject.Spec.Replicas = ptr.To(int32(1))

		replaceContainerIndex = containerIndexOrDefault(a.config.Container, targetObject.Spec.Template.Annotations, targetObject.Spec.Template.Spec.Containers)
		if replaceContainerIndex == -1 {
			return nil, fmt.Errorf("unable to find container to replace in target object %s/%s", targetObject.Namespace, targetObject.Name)
		}

		if err := a.applyConfig(ctx, a.config.Namespace, relatedObjectName); err != nil {
			return nil, fmt.Errorf("unable to create config: %w", err)
		}

		a.replaceContainerWithAgent(&targetObject.Spec.Template.Spec, relatedObjectName, replaceContainerIndex)

		_, err := a.client.AppsV1().Deployments(targetObject.Namespace).Update(ctx, targetObject, v1.UpdateOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to update target object %s/%s: %w", targetObject.Namespace, targetObject.Name, err)
		}
	case *appsv1.StatefulSet:
		matchLabels = targetObject.Spec.Selector.MatchLabels

		if targetObject.Spec.Template.Annotations == nil {
			targetObject.Spec.Template.Annotations = make(map[string]string)
		}

		targetObject.Spec.Template.Annotations[WireguardRevisionAnnotationName] = revision
		targetObject.Spec.Replicas = ptr.To(int32(1))

		replaceContainerIndex = containerIndexOrDefault(a.config.Container, targetObject.Spec.Template.Annotations, targetObject.Spec.Template.Spec.Containers)
		if replaceContainerIndex == -1 {
			return nil, fmt.Errorf("unable to find container to replace in target object %s/%s", targetObject.Namespace, targetObject.Name)
		}

		if err := a.applyConfig(ctx, a.config.Namespace, relatedObjectName); err != nil {
			return nil, fmt.Errorf("unable to create config: %w", err)
		}

		a.replaceContainerWithAgent(&targetObject.Spec.Template.Spec, relatedObjectName, replaceContainerIndex)

		_, err := a.client.AppsV1().StatefulSets(targetObject.Namespace).Update(ctx, targetObject, v1.UpdateOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to update target object %s/%s: %w", targetObject.Namespace, targetObject.Name, err)
		}
	default:
		return nil, fmt.Errorf("target object is not a supported type: %t", targetObject)
	}

	if a.config.Wireguard.DirectAccess {
		address, err := waitForPod(ctx, a.client.CoreV1().RESTClient(), a.restConfig, a.config.Namespace, matchLabels, revision)
		if err != nil {
			return nil, fmt.Errorf("failed to find new pod for %s/%s: %w", a.config.Namespace, objectName, err)
		}

		if err := a.applyNetworkPolicy(ctx, a.config.Namespace, relatedObjectName, matchLabels, int32(address.Port())); err != nil {
			return nil, fmt.Errorf("failed to create network policy for %s/%s: %w", a.config.Namespace, objectName, err)
		}

		a.agentAddress = address
	} else if !a.config.Wireguard.LocalAddress.IsValid() {
		address, err := a.applyLoadbalancer(ctx, a.config.Namespace, relatedObjectName, matchLabels)
		if err != nil {
			return nil, fmt.Errorf("failed to create load balancer service for %s/%s: %w", a.config.Namespace, objectName, err)
		}

		if err := a.applyNetworkPolicy(ctx, a.config.Namespace, relatedObjectName, matchLabels, int32(wg.DefaultWireguardPort)); err != nil {
			return nil, fmt.Errorf("failed to create network policy for %s/%s: %w", a.config.Namespace, objectName, err)
		}

		a.agentAddress = address
	}

	return func() {
		if a.config.KeepResources {
			return
		}

		if err := a.client.CoreV1().Services(a.config.Namespace).Delete(ctx, relatedObjectName, v1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
			log.Error(err, "unable to delete service", "name", relatedObjectName)
		}

		if err := a.client.NetworkingV1().NetworkPolicies(a.config.Namespace).Delete(ctx, relatedObjectName, v1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
			log.Error(err, "unable to delete netpol", "name", relatedObjectName)
		}

		if err := a.client.CoreV1().Secrets(a.config.Namespace).Delete(ctx, relatedObjectName, v1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
			log.Error(err, "unable to delete secret", "name", relatedObjectName)
		}
	}, nil
}

func (a *kubernetesAgent) applyConfig(ctx context.Context, namespace, configName string) error {
	cfg, err := yaml.Marshal(a.config.Wireguard)
	if err != nil {
		return fmt.Errorf("unable to marshal wireguard config to YAML: %w", err)
	}

	secret := corev1apply.Secret(configName, namespace).WithData(map[string][]byte{"wg.yml": cfg})
	_, err = a.client.CoreV1().Secrets(namespace).Apply(ctx, secret, v1.ApplyOptions{FieldManager: FieldManager})

	return err
}

func (a *kubernetesAgent) replaceContainerWithAgent(podSpec *corev1.PodSpec, configName string, containerIndex int) {
	var excludePorts []string

	// Remove liveness probes in case they're checking the container we're
	// replacing; if the proxy service isn't up yet these would fail.
	for index, container := range podSpec.Containers {
		podSpec.Containers[index].LivenessProbe = nil
		podSpec.Containers[index].ReadinessProbe = nil
		podSpec.Containers[index].StartupProbe = nil

		if index != containerIndex {
			// Add other listeners to allow direct access without proxying through wireguard
			for _, port := range container.Ports {
				excludePorts = append(excludePorts, strconv.FormatInt(int64(port.ContainerPort), 10))
			}
		}
	}

	replacedContainer := podSpec.Containers[containerIndex]
	podSpec.Containers[containerIndex] = corev1.Container{
		Name:            ContainerName,
		Image:           a.config.AgentImage,
		ImagePullPolicy: corev1.PullAlways,
		// Retain ports in case they're named at the service level
		Ports: replacedContainer.Ports,
		Env: []corev1.EnvVar{
			{
				Name:  "LOCAL_PORTS_EXCLUDE_PROXY",
				Value: strings.Join(excludePorts, ","),
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
	}

	volume := corev1.Volume{
		Name: WireguardConfigVolumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: configName,
				Optional:   ptr.To(false),
			},
		},
	}

	volumeIndex := slices.IndexFunc(podSpec.Volumes, func(v corev1.Volume) bool { return v.Name == WireguardConfigVolumeName })
	if volumeIndex == -1 {
		podSpec.Volumes = append(podSpec.Volumes, volume)
	} else {
		podSpec.Volumes[volumeIndex] = volume
	}
}

func (a *kubernetesAgent) applyLoadbalancer(ctx context.Context, namespace, name string, selector map[string]string) (netip.AddrPort, error) {
	log := logr.FromContextOrDiscard(ctx)

	annotations := map[string]string{
		// AWS
		"service.beta.kubernetes.io/aws-load-balancer-backend-protocol":                  "tcp",
		"service.beta.kubernetes.io/aws-load-balancer-internal":                          "false",
		"service.beta.kubernetes.io/aws-load-balancer-type":                              "nlb",
		"service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled": "true",
		// GCP
		"cloud.google.com/l4-rbs": "enabled",
		// No Azure annotations necessary
	}

	service := corev1apply.Service(name, namespace).
		WithAnnotations(annotations).
		WithSpec(&corev1apply.ServiceSpecApplyConfiguration{
			Ports: []corev1apply.ServicePortApplyConfiguration{{
				Name:       ptr.To("wireguard"),
				Protocol:   ptr.To(corev1.ProtocolUDP),
				Port:       ptr.To(int32(wg.DefaultWireguardPort)),
				TargetPort: ptr.To(intstr.FromInt32(wg.DefaultWireguardPort)),
			}},
			Selector:              selector,
			Type:                  ptr.To(corev1.ServiceTypeLoadBalancer),
			ExternalTrafficPolicy: ptr.To(corev1.ServiceExternalTrafficPolicyLocal),
			InternalTrafficPolicy: ptr.To(corev1.ServiceInternalTrafficPolicyLocal),
		})

	_, err := a.client.CoreV1().Services(namespace).Apply(ctx, service, v1.ApplyOptions{FieldManager: FieldManager})
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("unable to apply service: %w", err)
	}

	svc, err := waitForLoadBalancerReady(ctx, a.client.CoreV1().RESTClient(), namespace, name)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("timeout after %s waiting for load balancer service to be ready: %w", WaitTimeout.String(), err)
	}

	ing := svc.Status.LoadBalancer.Ingress[0]

	if ing.Hostname != "" {
		var ip netip.Addr

		resolveCtx, cancel := context.WithTimeout(ctx, WaitTimeout)
		defer cancel()

		log.Info("Load balancer ready, waiting for DNS to resolve", "hostname", ing.Hostname)

		err = wait.PollUntilContextCancel(resolveCtx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
			ips, _ := net.DefaultResolver.LookupIP(ctx, "ip4", ing.Hostname)
			if len(ips) == 0 {
				return false, nil
			}

			ip = netip.AddrFrom4([4]byte(ips[0]))

			return true, nil
		})

		if err != nil {
			return netip.AddrPort{}, fmt.Errorf("unable to lookup IP for load balancer hostname %q: %w", ing.Hostname, err)
		}

		return netip.AddrPortFrom(ip, wg.DefaultWireguardPort), nil
	} else if ing.IP != "" {
		ip, err := netip.ParseAddr(ing.IP)
		if err != nil {
			return netip.AddrPort{}, fmt.Errorf("unable to parse IP for load balancer IP %q: %w", ing.IP, err)
		}

		return netip.AddrPortFrom(ip, wg.DefaultWireguardPort), nil
	}

	return netip.AddrPort{}, fmt.Errorf("unable to find load balancer address for service %q", svc.Name)
}

func (a *kubernetesAgent) applyNetworkPolicy(ctx context.Context, namespace, name string, selector map[string]string, port int32) error {
	netpol := netv1apply.NetworkPolicy(name, namespace).
		WithSpec(&netv1apply.NetworkPolicySpecApplyConfiguration{
			PodSelector: &metav1apply.LabelSelectorApplyConfiguration{MatchLabels: selector},
			Ingress: []netv1apply.NetworkPolicyIngressRuleApplyConfiguration{{
				Ports: []netv1apply.NetworkPolicyPortApplyConfiguration{{
					Protocol: ptr.To(corev1.ProtocolUDP),
					Port:     ptr.To(intstr.FromInt32(port)),
				}},
			}},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeIngress},
		})

	_, err := a.client.NetworkingV1().NetworkPolicies(namespace).Apply(ctx, netpol, v1.ApplyOptions{FieldManager: FieldManager})

	return err
}

func wgObjectName(name string) string {
	return fmt.Sprintf("wg-%s", name)
}

func containerIndexOrDefault(containerName string, annotations map[string]string, containers []corev1.Container) int {
	if len(containers) == 0 {
		return -1
	}

	if containerName == "" {
		if name := annotations[DefaultContainerAnnotationName]; len(name) > 0 {
			containerName = name
		} else {
			containerName = containers[0].Name
		}
	}

	return slices.IndexFunc(containers, func(container corev1.Container) bool { return container.Name == containerName })
}

func podReady(c corev1.PodCondition) bool {
	return c.Status == corev1.ConditionTrue && c.Type == corev1.PodReady
}

var waitForPod = func(ctx context.Context, client cache.Getter, restConfig *rest.Config, namespace string, matchLabels map[string]string, revision string) (address netip.AddrPort, err error) {
	log := logr.FromContextOrDiscard(ctx)

	lw := cache.NewFilteredListWatchFromClient(client, "pods", namespace, func(o *v1.ListOptions) {
		o.LabelSelector = labels.SelectorFromSet(matchLabels).String()
	})

	deadlineCtx, cancel := context.WithTimeout(ctx, WaitTimeout)
	defer cancel()

	log.Info("Waiting for new pod to by ready", "revision", revision)

	sync, syncErr := watchtools.UntilWithSync(deadlineCtx, lw, &corev1.Pod{}, nil, func(event watch.Event) (bool, error) {
		pod := event.Object.(*corev1.Pod)

		return pod.Annotations[WireguardRevisionAnnotationName] == revision && slices.ContainsFunc(pod.Status.Conditions, podReady), nil
	})
	if syncErr != nil {
		err = fmt.Errorf("timeout after %s waiting for pod to be ready: %w", WaitTimeout.String(), syncErr)
		return
	}

	pod := sync.Object.(*corev1.Pod)
	log = log.WithValues("pod", pod.Name)

	log.Info("Waiting for pod remote address", "pod", pod.Name)

	pollErr := wait.PollUntilContextCancel(deadlineCtx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		contents, err := kuberneteshelpers.FileContents(ctx, restConfig, pod, ContainerName, ContainerAddressPath)
		if err != nil {
			log.V(1).Info("unable to read address from pod", "error", err.Error())
			return false, nil
		}

		podAddress := strings.Split(contents, "\n")[0]
		if podAddress == "" {
			log.V(1).Info("unable to read address from pod", "error", err.Error())
			return false, nil
		}

		address, err = netip.ParseAddrPort(podAddress)
		if err != nil {
			log.V(1).Info("unable to read address from pod", "error", err.Error())
			return false, nil
		}

		return true, nil
	})
	if pollErr != nil {
		err = fmt.Errorf("timeout after %s waiting for pod address: %w", WaitTimeout.String(), pollErr)
	}

	return
}

var waitForLoadBalancerReady = func(ctx context.Context, client cache.Getter, namespace, name string) (*corev1.Service, error) {
	log := logr.FromContextOrDiscard(ctx)

	log.Info("Waiting for load balancer to be ready", "service", name, "namespace", namespace)

	lw := cache.NewListWatchFromClient(client, "services", namespace, fields.OneTermEqualSelector("metadata.name", name))

	deadlineCtx, cancel := context.WithTimeout(ctx, WaitTimeout)
	defer cancel()

	sync, err := watchtools.UntilWithSync(deadlineCtx, lw, &corev1.Service{}, nil, func(event watch.Event) (bool, error) {
		svc := event.Object.(*corev1.Service)
		if len(svc.Status.LoadBalancer.Ingress) > 0 {
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		return nil, err
	}

	return sync.Object.(*corev1.Service), nil
}

var newRevision = func() string { return uuid.New().String() }
