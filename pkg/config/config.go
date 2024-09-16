package config

import (
	"net/netip"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/steved/kubewire/pkg/kuberneteshelpers"
)

type Config struct {
	// AgentImage is the container image reference to use within Kubernetes
	AgentImage string

	// TargetObject is the target Kubernetes object to proxy access for
	TargetObject runtime.Object
	// Namespace is the namespace of the target Kubernetes object
	Namespace string
	// Container is the name of the container to target within the Kubernetes object
	Container string

	// KeepResources will prevent load balancers and other created resources from being deleted when exiting
	KeepResources bool

	// Wireguard contains wireguard-specific configuration
	Wireguard Wireguard

	// KubernetesClusterDetails contains resolved information about the K8s cluster
	KubernetesClusterDetails kuberneteshelpers.ClusterDetails
}

func NewConfig() *Config {
	return &Config{}
}

// Wireguard represents configuration needed to set up wireguard in two different contexts: Local and Kubernetes Agent
type Wireguard struct {
	// DirectAccess controls whether to attempt NAT hole-punching or use load balancers to access the target pod
	DirectAccess bool

	// LocalKey always represents the keypair associated with the machine we're connecting from
	LocalKey Key
	// AgentKey represents the keypair associated with the Kubernetes agent we're connecting to
	AgentKey Key

	// LocalAddress represents the local endpoint address for wireguard
	LocalAddress netip.AddrPort

	// OverlayPrefix is the prefix of the overlay network
	OverlayPrefix netip.Prefix

	// LocalOverlayAddress is the proxy address inside the overlay network
	LocalOverlayAddress netip.Addr

	// AgentOverlayAddress is the agent address inside the overlay network
	AgentOverlayAddress netip.Addr

	// AllowedIPs is the set of prefixes allowed to be routed through wireguard
	AllowedIPs []netip.Prefix
}

type WireguardOption func(*Wireguard) error

func NewWireguardConfig(options ...WireguardOption) (Wireguard, error) {
	wg := Wireguard{}

	for _, option := range options {
		if err := option(&wg); err != nil {
			return wg, err
		}
	}

	return wg, nil
}

func WithGeneratedKeypairs() WireguardOption {
	return func(wg *Wireguard) error {
		localKeypair, err := wgtypes.GeneratePrivateKey()
		if err != nil {
			return err
		}

		wg.LocalKey = Key{localKeypair}

		agentKeypair, err := wgtypes.GeneratePrivateKey()
		if err != nil {
			return err
		}

		wg.AgentKey = Key{agentKeypair}

		return nil
	}
}

func WithLocalAddress(address netip.AddrPort) WireguardOption {
	return func(wg *Wireguard) error {
		wg.LocalAddress = address
		return nil
	}
}

func WithDirectAccess(directAccess bool) WireguardOption {
	return func(wg *Wireguard) error {
		wg.DirectAccess = directAccess
		return nil
	}
}

func WithOverlay(overlay, localAddress, agentAddress string) WireguardOption {
	return func(wg *Wireguard) (err error) {
		wg.OverlayPrefix, err = netip.ParsePrefix(overlay)
		if err != nil {
			return
		}

		wg.LocalOverlayAddress, err = netip.ParseAddr(localAddress)
		if err != nil {
			return
		}

		wg.AgentOverlayAddress, err = netip.ParseAddr(agentAddress)

		return
	}
}

func WithAllowedIPs(allowedIPs ...string) WireguardOption {
	return func(wg *Wireguard) error {
		for _, allowedIP := range allowedIPs {
			prefix, err := netip.ParsePrefix(allowedIP)
			if err != nil {
				return err
			}

			wg.AllowedIPs = append(wg.AllowedIPs, prefix)
		}

		return nil
	}
}
