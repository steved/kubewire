package kuberneteshelpers

import (
	"context"
	"fmt"
	"net/netip"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ClusterDetails struct {
	ServiceIP                      netip.Addr
	PodCIDR, ServiceCIDR, NodeCIDR netip.Prefix
}

func (c ClusterDetails) Resolve(ctx context.Context, client kubernetes.Interface, namespace string) (ClusterDetails, error) {
	pods, err := client.CoreV1().Pods(namespace).List(ctx, v1.ListOptions{})
	if err != nil {
		return ClusterDetails{}, fmt.Errorf("unable to list pods for cluster details: %w", err)
	}

	podCIDR := c.PodCIDR
	if !podCIDR.IsValid() {
		podAddr, err := func() (netip.Addr, error) {
			// Find the first non-hostNetwork pod and assume that represents the pod CIDR as a /16
			for _, p := range pods.Items {
				if p.Spec.HostNetwork || p.Status.PodIP == "" {
					continue
				}

				addr, err := netip.ParseAddr(p.Status.PodIP)
				if err != nil {
					continue
				}

				return addr, nil
			}

			return netip.Addr{}, fmt.Errorf("unable to find any pods")
		}()
		if err != nil {
			return ClusterDetails{}, fmt.Errorf("unable to obtain pod CIDR: %w", err)
		}

		podCIDR, err = podAddr.Prefix(16)
		if err != nil {
			return ClusterDetails{}, fmt.Errorf("unable to obtain pod CIDR: %w", err)
		}
	}

	dnsService, err := client.CoreV1().Services("kube-system").Get(ctx, "kube-dns", v1.GetOptions{})
	if err != nil {
		return ClusterDetails{}, fmt.Errorf("unable to find kube-dns service for service CIDR: %w", err)
	}

	serviceAddr, err := netip.ParseAddr(dnsService.Spec.ClusterIP)
	if err != nil {
		return ClusterDetails{}, fmt.Errorf("unable to obtain service CIDR: %w", err)
	}

	serviceCIDR := c.ServiceCIDR
	if !serviceCIDR.IsValid() {
		serviceCIDR, err = serviceAddr.Prefix(16)
		if err != nil {
			return ClusterDetails{}, fmt.Errorf("unable to obtain service CIDR: %w", err)
		}
	}

	nodeCIDR := c.NodeCIDR
	if !nodeCIDR.IsValid() {
		nodes, err := client.CoreV1().Nodes().List(ctx, v1.ListOptions{})
		if err != nil {
			return ClusterDetails{}, fmt.Errorf("unable to list nodes for cluster details: %w", err)
		}

		if len(nodes.Items) < 1 {
			return ClusterDetails{}, fmt.Errorf("no nodes found for cluster details: %w", err)
		}

		nodeCIDR, err = func() (netip.Prefix, error) {
			for _, node := range nodes.Items {
				for _, address := range node.Status.Addresses {
					if address.Type == corev1.NodeInternalIP {
						nodeAddr, err := netip.ParseAddr(address.Address)
						if err != nil {
							continue
						}

						return nodeAddr.Prefix(16)
					}
				}
			}

			return netip.Prefix{}, fmt.Errorf("no valid InternalIP node addresses found")
		}()
		if err != nil {
			return ClusterDetails{}, fmt.Errorf("unable to obtain node CIDR: %w", err)
		}
	}

	return ClusterDetails{
		ServiceIP:   serviceAddr,
		ServiceCIDR: serviceCIDR,
		PodCIDR:     podCIDR,
		NodeCIDR:    nodeCIDR,
	}, nil
}
