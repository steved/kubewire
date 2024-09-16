package proxy

import (
	"context"
	"net/netip"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/steved/kubewire/pkg/agent"
	"github.com/steved/kubewire/pkg/config"
	"github.com/steved/kubewire/pkg/routing"
	"github.com/steved/kubewire/pkg/runnable"
	"github.com/steved/kubewire/pkg/wg"
)

var stopFuncs []runnable.StopFunc

func Run(ctx context.Context, cfg *config.Config, kubernetesClient kubernetes.Interface, kubernetesRestConfig *rest.Config) error {
	log := logr.FromContextOrDiscard(ctx)

	defer func() {
		for _, stop := range stopFuncs {
			stop()
		}
	}()

	if cfg.Wireguard.LocalAddress.IsValid() {
		if err := wireguardDeviceSetup(ctx, cfg, netip.AddrPort{}); err != nil {
			return err
		}

		if _, err := kubernetesSetup(ctx, cfg, kubernetesClient, kubernetesRestConfig); err != nil {
			return err
		}
	} else {
		agentAddress, err := kubernetesSetup(ctx, cfg, kubernetesClient, kubernetesRestConfig)
		if err != nil {
			return err
		}

		if err := wireguardDeviceSetup(ctx, cfg, agentAddress); err != nil {
			return err
		}
	}

	log.Info("Started. Use Ctrl-C to exit...")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	return nil
}

func wireguardDeviceSetup(ctx context.Context, cfg *config.Config, agentAddress netip.AddrPort) error {
	log := logr.FromContextOrDiscard(ctx)

	log.V(1).Info("Starting Wireguard device setup")

	listenPort := 0
	if cfg.Wireguard.LocalAddress.IsValid() {
		listenPort = int(cfg.Wireguard.LocalAddress.Port())
	}

	wireguardDevice := wg.NewWireguardDevice(wg.WireguardDeviceConfig{
		Peer: wg.WireguardDevicePeer{
			Endpoint:   agentAddress,
			PublicKey:  cfg.Wireguard.AgentKey.PublicKey(),
			AllowedIPs: cfg.Wireguard.AllowedIPs,
		},
		PrivateKey: cfg.Wireguard.LocalKey.Key,
		ListenPort: listenPort,
		Address:    cfg.Wireguard.LocalOverlayAddress,
	})

	wgStop, err := wireguardDevice.Start(ctx)
	if err != nil {
		return err
	}

	stopFuncs = append(stopFuncs, wgStop)

	log.Info("Wireguard device setup complete")

	log.V(1).Info("Starting route setup")

	router := routing.NewRouting(
		wireguardDevice.DeviceName(),
		cfg.KubernetesClusterDetails.ServiceIP,
		cfg.KubernetesClusterDetails.PodCIDR,
		cfg.KubernetesClusterDetails.ServiceCIDR,
		cfg.KubernetesClusterDetails.NodeCIDR,
		netip.PrefixFrom(cfg.Wireguard.AgentOverlayAddress, 32),
	)

	routerStop, err := router.Start(ctx)
	if err != nil {
		return err
	}

	stopFuncs = append(stopFuncs, routerStop)

	log.Info("Routing setup complete")

	return nil
}

func kubernetesSetup(ctx context.Context, cfg *config.Config, kubernetesClient kubernetes.Interface, kubernetesRestConfig *rest.Config) (netip.AddrPort, error) {
	log := logr.FromContextOrDiscard(ctx)

	log.V(1).Info("Starting Kubernetes setup")

	kubernetesAgent := agent.NewKubernetesAgent(cfg, kubernetesClient, kubernetesRestConfig)

	agentStop, err := kubernetesAgent.Start(ctx)
	if err != nil {
		return netip.AddrPort{}, err
	}

	stopFuncs = append(stopFuncs, agentStop)

	log.Info("Kubernetes setup complete")

	return kubernetesAgent.AgentAddress(), nil
}
