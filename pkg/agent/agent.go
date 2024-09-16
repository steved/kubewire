package agent

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/coreos/go-iptables/iptables"
	"github.com/go-logr/logr"
	"tailscale.com/net/netutil"

	"github.com/steved/kubewire/pkg/config"
	"github.com/steved/kubewire/pkg/nat"
	"github.com/steved/kubewire/pkg/routing"
	"github.com/steved/kubewire/pkg/wg"
)

var defaultInterface = func() (string, netip.Addr, error) {
	return netutil.DefaultInterfacePortable()
}

type iptablesManager interface {
	AppendUnique(string, string, ...string) error
	InsertUnique(string, string, int, ...string) error
	ChainExists(string, string) (bool, error)
}

func Run(ctx context.Context, cfg config.Wireguard, istioEnabled bool, proxyExcludedPorts []string) error {
	log := logr.FromContextOrDiscard(ctx)

	var listenPort int

	if cfg.DirectAccess {
		log.V(1).Info("Starting NAT address lookup")

		localHost, localPort, err := nat.FindLocalAddressAndPort(ctx)
		if err != nil {
			return fmt.Errorf("unable to proxy connectable address for NAT traversal: %w", err)
		}

		listenPort = localPort

		localAddress := fmt.Sprintf("%s:%d", localHost, localPort)
		if err := os.WriteFile(ContainerAddressPath, []byte(localAddress), 0600); err != nil {
			return err
		}

		log.Info("NAT address lookup complete", "address", localAddress)
	} else if cfg.LocalAddress.IsValid() {
		listenPort = -1
	}

	log.V(1).Info("Starting wireguard device setup")

	wireguardDevice := wg.NewWireguardDevice(wg.WireguardDeviceConfig{
		Peer: wg.WireguardDevicePeer{
			Endpoint:   cfg.LocalAddress,
			PublicKey:  cfg.LocalKey.PublicKey(),
			AllowedIPs: cfg.AllowedIPs,
		},
		PrivateKey: cfg.AgentKey.Key,
		ListenPort: listenPort,
		Address:    cfg.AgentOverlayAddress,
	})

	wgStop, err := wireguardDevice.Start(ctx)
	if err != nil {
		return err
	}

	defer wgStop()

	log.Info("Wireguard device setup complete")

	log.V(1).Info("Starting route setup")

	router := routing.NewRouting(wireguardDevice.DeviceName(), netip.Addr{}, netip.PrefixFrom(cfg.LocalOverlayAddress, 32))

	routerStop, err := router.Start(ctx)
	if err != nil {
		return err
	}

	defer routerStop()

	log.Info("Routing setup complete")

	log.V(1).Info("Starting IPTables setup")

	ipt, err := iptables.New(iptables.IPFamily(iptables.ProtocolIPv4), iptables.Timeout(5))
	if err != nil {
		return fmt.Errorf("unable to initialize iptables client: %w", err)
	}

	if err := updateIPTablesRules(cfg, ipt, wireguardDevice.DeviceName(), istioEnabled, proxyExcludedPorts); err != nil {
		return err
	}

	log.Info("IPTables setup complete")

	log.Info("Started, waiting for signal")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	return nil
}

func updateIPTablesRules(cfg config.Wireguard, ipt iptablesManager, wireguardDeviceName string, istioEnabled bool, proxyExcludedPorts []string) error {
	deviceName, deviceAddr, err := defaultInterface()
	if err != nil {
		return fmt.Errorf("unable to determine default device name: %w", err)
	}

	if err := ipt.AppendUnique("nat", "POSTROUTING", "-p", "udp", "-o", deviceName, "-j", "MASQUERADE"); err != nil {
		return fmt.Errorf("unable to create iptables rule: %w", err)
	}

	defaultIfaceRulespec := []string{"-p", "tcp", "-i", deviceName}

	if len(proxyExcludedPorts) > 0 {
		defaultIfaceRulespec = append(defaultIfaceRulespec, "-m", "multiport", "!", "--dports", strings.Join(proxyExcludedPorts, ","))
	}

	defaultIfaceRulespec = append(defaultIfaceRulespec, "-j", "DNAT", "--to-destination", cfg.LocalOverlayAddress.String())

	if err := ipt.AppendUnique("nat", "PREROUTING", defaultIfaceRulespec...); err != nil {
		return fmt.Errorf("unable to create iptables rule: %w", err)
	}

	if istioEnabled {
		if err := ipt.InsertUnique("nat", "PREROUTING", 1, "-p", "tcp", "-i", wireguardDeviceName, "-j", "DNAT", "--to-destination", "127.0.0.6:15001"); err != nil {
			return fmt.Errorf("unable to create iptables rule: %w", err)
		}
	} else {
		if err := ipt.AppendUnique("nat", "PREROUTING", "-p", "tcp", "-i", wireguardDeviceName, "--destination", deviceAddr.String(), "-j", "DNAT", "--to-destination", cfg.LocalOverlayAddress.String()); err != nil {
			return fmt.Errorf("unable to create iptables rule: %w", err)
		}

		if err := ipt.AppendUnique("nat", "POSTROUTING", "-p", "tcp", "-o", deviceName, "-j", "MASQUERADE"); err != nil {
			return fmt.Errorf("unable to create iptables rule: %w", err)
		}
	}

	return nil
}
