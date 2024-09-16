package proxy

import (
	"context"
	"net/netip"
	"reflect"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/steved/kubewire/pkg/config"
	"github.com/steved/kubewire/pkg/kuberneteshelpers"
)

func TestResolveWireguardConfig(t *testing.T) {
	validClusterDetails := kuberneteshelpers.ClusterDetails{
		ServiceIP:   netip.MustParseAddr("172.0.0.1"),
		PodCIDR:     netip.MustParsePrefix("100.64.0.0/16"),
		ServiceCIDR: netip.MustParsePrefix("172.0.0.0/16"),
		NodeCIDR:    netip.MustParsePrefix("10.0.0.0/16"),
	}

	overlappingClusterDetails := kuberneteshelpers.ClusterDetails{
		ServiceIP:   netip.MustParseAddr("172.0.0.1"),
		PodCIDR:     netip.MustParsePrefix("100.64.51.0/16"),
		ServiceCIDR: netip.MustParsePrefix("172.0.0.0/16"),
		NodeCIDR:    netip.MustParsePrefix("10.1.0.0/16"),
	}

	findLocalAddressAndPort = func(_ context.Context) (string, int, error) {
		return "1.2.3.4", 9080, nil
	}

	defaultOverlay := netip.MustParsePrefix("10.1.0.0/28")

	tests := []struct {
		name           string
		clusterDetails kuberneteshelpers.ClusterDetails
		overlayPrefix  string
		directAccess   bool
		want           config.Wireguard
		wantErr        bool
	}{
		{
			"invalid overlay prefix",
			validClusterDetails,
			"1.2./1",
			false,
			config.Wireguard{},
			true,
		},
		{
			"overlapping CIDR prefixes",
			overlappingClusterDetails,
			"",
			false,
			config.Wireguard{},
			true,
		},
		{
			"valid",
			validClusterDetails,
			"",
			false,
			config.Wireguard{
				DirectAccess:        false,
				LocalAddress:        netip.AddrPort{},
				OverlayPrefix:       defaultOverlay,
				LocalOverlayAddress: netip.MustParseAddr("10.1.0.1"),
				AgentOverlayAddress: netip.MustParseAddr("10.1.0.2"),
				AllowedIPs: []netip.Prefix{
					validClusterDetails.PodCIDR,
					validClusterDetails.ServiceCIDR,
					validClusterDetails.NodeCIDR,
					defaultOverlay,
				},
			},
			false,
		},
		{
			"overlay prefix",
			validClusterDetails,
			"192.168.0.0/16",
			false,
			config.Wireguard{
				DirectAccess:        false,
				LocalAddress:        netip.AddrPort{},
				OverlayPrefix:       netip.MustParsePrefix("192.168.0.0/16"),
				LocalOverlayAddress: netip.MustParseAddr("192.168.0.1"),
				AgentOverlayAddress: netip.MustParseAddr("192.168.0.2"),
				AllowedIPs: []netip.Prefix{
					validClusterDetails.PodCIDR,
					validClusterDetails.ServiceCIDR,
					validClusterDetails.NodeCIDR,
					netip.MustParsePrefix("192.168.0.0/16"),
				},
			},
			false,
		},
		{
			"direct access",
			validClusterDetails,
			"",
			true,
			config.Wireguard{
				DirectAccess:        true,
				LocalAddress:        netip.MustParseAddrPort("1.2.3.4:9080"),
				OverlayPrefix:       defaultOverlay,
				LocalOverlayAddress: netip.MustParseAddr("10.1.0.1"),
				AgentOverlayAddress: netip.MustParseAddr("10.1.0.2"),
				AllowedIPs: []netip.Prefix{
					validClusterDetails.PodCIDR,
					validClusterDetails.ServiceCIDR,
					validClusterDetails.NodeCIDR,
					defaultOverlay,
				},
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getClusterDetails = func(_ context.Context, _ kuberneteshelpers.ClusterDetails, _ kubernetes.Interface, _ string) (kuberneteshelpers.ClusterDetails, error) {
				return tt.clusterDetails, nil
			}

			cfg := &config.Config{}
			if err := ResolveWireguardConfig(context.Background(), cfg, fake.NewClientset(), tt.overlayPrefix, tt.directAccess); (err != nil) != tt.wantErr {
				t.Errorf("ResolveWireguardConfig() error = %v, expected %v", err, tt.wantErr)
				return
			}

			if cfg.KubernetesClusterDetails != tt.clusterDetails {
				t.Errorf("ResolveWireguardConfig() cluster details got = %v, want %v", cfg.KubernetesClusterDetails, tt.clusterDetails)
			}

			if cfg.Wireguard.AgentKey.String() == "" || cfg.Wireguard.LocalKey.String() == "" {
				t.Errorf("ResolveWireguardConfig() invalid cluster keys got = (%s, %s)", cfg.Wireguard.AgentKey.String(), cfg.Wireguard.LocalKey.String())
			}

			tt.want.AgentKey = cfg.Wireguard.AgentKey
			tt.want.LocalKey = cfg.Wireguard.LocalKey

			if !reflect.DeepEqual(cfg.Wireguard, tt.want) {
				t.Errorf("ResolveWireguardConfig() got = %v, want %v", cfg.Wireguard, tt.want)
			}
		})
	}
}
