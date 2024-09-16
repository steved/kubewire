package proxy

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"

	"github.com/steved/kubewire/pkg/config"
	"github.com/steved/kubewire/pkg/kuberneteshelpers"
	"github.com/steved/kubewire/pkg/nat"
)

var getClusterDetails = func(ctx context.Context, clusterDetails kuberneteshelpers.ClusterDetails, client kubernetes.Interface, namespace string) (kuberneteshelpers.ClusterDetails, error) {
	return clusterDetails.Resolve(ctx, client, namespace)
}

var findLocalAddressAndPort = func(ctx context.Context) (string, int, error) {
	return nat.FindLocalAddressAndPort(ctx)
}

var overlayCIDRs = []netip.Prefix{
	netip.MustParsePrefix("10.1.0.0/28"),
	netip.MustParsePrefix("100.64.51.0/28"),
}

func ResolveWireguardConfig(ctx context.Context, proxyConfig *config.Config, client kubernetes.Interface, overlayPrefix string, directAccess bool) error {
	log := logr.FromContextOrDiscard(ctx)

	clusterDetails, err := getClusterDetails(ctx, proxyConfig.KubernetesClusterDetails, client, proxyConfig.Namespace)
	if err != nil {
		return fmt.Errorf("unable to obtain Kubernetes cluster details: %w", err)
	}

	log.V(1).Info(
		"Resolved Kubernetes cluster details",
		"service_ip",
		clusterDetails.ServiceIP,
		"service_cidr",
		clusterDetails.ServiceCIDR,
		"pod_cidr",
		clusterDetails.PodCIDR,
		"node_cidr",
		clusterDetails.NodeCIDR,
	)

	proxyConfig.KubernetesClusterDetails = clusterDetails

	var overlay netip.Prefix
	if overlayPrefix != "" {
		overlay, err = netip.ParsePrefix(overlayPrefix)
		if err != nil {
			return fmt.Errorf("unable to parse overlay prefix %q: %w", overlayPrefix, err)
		}
	} else {
		overlay, err = func() (netip.Prefix, error) {
			for _, cidr := range overlayCIDRs {
				if cidr.Overlaps(clusterDetails.PodCIDR) || cidr.Overlaps(clusterDetails.ServiceCIDR) || cidr.Overlaps(clusterDetails.NodeCIDR) {
					continue
				}

				return cidr, nil
			}

			return netip.Prefix{}, fmt.Errorf("unable to determine non-overlapping CIDR range for overlay network")
		}()
		if err != nil {
			return err
		}
	}

	log.V(1).Info("Determined overlay prefix", "overlay", overlay.String())

	proxyAddr := overlay.Addr()
	localOverlayAddress := proxyAddr.Next()
	agentOverlayAddress := localOverlayAddress.Next()

	options := []config.WireguardOption{
		config.WithGeneratedKeypairs(),
		config.WithOverlay(overlay.String(), localOverlayAddress.String(), agentOverlayAddress.String()),
		config.WithAllowedIPs(clusterDetails.PodCIDR.String(), clusterDetails.ServiceCIDR.String(), clusterDetails.NodeCIDR.String(), overlay.String()),
	}

	if directAccess {
		log.V(1).Info("Starting NAT address lookup")

		localIP, localPort, err := findLocalAddressAndPort(ctx)
		if err != nil {
			return fmt.Errorf("unable to proxy connectable address for NAT traversal: %w", err)
		}

		localAddr, err := netip.ParseAddr(localIP)
		if err != nil {
			return fmt.Errorf("unable to parse local IP: %w", err)
		}

		localAddress := netip.AddrPortFrom(localAddr, uint16(localPort))
		options = append(options, config.WithLocalAddress(localAddress), config.WithDirectAccess(directAccess))

		log.Info("NAT address lookup complete", "address", localAddress)
	} else if proxyConfig.Wireguard.LocalAddress.IsValid() {
		options = append(options, config.WithLocalAddress(proxyConfig.Wireguard.LocalAddress))
	}

	proxyConfig.Wireguard, err = config.NewWireguardConfig(options...)

	return err
}
