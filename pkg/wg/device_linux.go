//go:build linux

package wg

import (
	"context"
	"fmt"
	"net"
	"syscall"

	"github.com/go-logr/logr"
	"github.com/jsimonetti/rtnetlink"
	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"k8s.io/utils/ptr"

	"github.com/steved/kubewire/pkg/runnable"
)

func (w *wireguardDevice) Start(ctx context.Context) (runnable.StopFunc, error) {
	log := logr.FromContextOrDiscard(ctx)

	conn, err := rtnetlink.Dial(nil)
	if err != nil {
		return nil, fmt.Errorf("unable to initialize netlink client: %w", err)
	}

	err = conn.Link.New(&rtnetlink.LinkMessage{
		Family: syscall.AF_UNSPEC,
		Flags:  unix.IFF_UP,
		Attributes: &rtnetlink.LinkAttributes{
			Name: w.deviceName,
			Info: &rtnetlink.LinkInfo{Kind: "wireguard"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("unable to create wireguard interface: %w", err)
	}

	iface, err := net.InterfaceByName(w.deviceName)
	if err != nil {
		return nil, fmt.Errorf("unable to find created wireguard interface: %w", err)
	}

	overlayIP := net.IP(w.config.Address.AsSlice())
	broadcast := net.IPv4(255, 255, 255, 255)

	err = conn.Address.New(&rtnetlink.AddressMessage{
		Family:       syscall.AF_INET,
		PrefixLength: uint8(32),
		Scope:        unix.RT_SCOPE_UNIVERSE,
		Index:        uint32(iface.Index),
		Attributes: &rtnetlink.AddressAttributes{
			Address:   overlayIP,
			Local:     overlayIP,
			Broadcast: broadcast,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("unable to add %s to %s: %w", overlayIP, w.deviceName, err)
	}

	wgClient, err := wgctrl.New()
	if err != nil {
		return nil, fmt.Errorf("unable to create wireguard client: %w", err)
	}

	defer func() {
		if err := wgClient.Close(); err != nil {
			log.Error(err, "unable to close wireguard client")
		}
	}()

	listenPort := DefaultWireguardPort
	if w.config.ListenPort < 0 {
		listenPort = 0
	} else if w.config.ListenPort != 0 {
		listenPort = w.config.ListenPort
	}

	if err := wgClient.ConfigureDevice(w.deviceName, wgtypes.Config{
		ListenPort: ptr.To(listenPort),
		PrivateKey: ptr.To(w.config.PrivateKey),
	}); err != nil {
		return nil, fmt.Errorf("unable to configure wireguard: %w", err)
	}

	allowedIPs := make([]net.IPNet, len(w.config.Peer.AllowedIPs))
	for i, ip := range w.config.Peer.AllowedIPs {
		allowedIPs[i] = net.IPNet{
			IP:   ip.Addr().AsSlice(),
			Mask: net.CIDRMask(ip.Bits(), ip.Addr().BitLen()),
		}
	}

	var endpoint *net.UDPAddr
	if w.config.Peer.Endpoint.IsValid() {
		endpoint = net.UDPAddrFromAddrPort(w.config.Peer.Endpoint)
	}

	peer := wgtypes.PeerConfig{
		PublicKey:                   w.config.Peer.PublicKey,
		PersistentKeepaliveInterval: ptr.To(PersistentKeepaliveInterval),
		AllowedIPs:                  allowedIPs,
		Endpoint:                    endpoint,
	}

	if err := wgClient.ConfigureDevice(w.deviceName, wgtypes.Config{
		ReplacePeers: true,
		Peers:        []wgtypes.PeerConfig{peer},
	}); err != nil {
		return nil, fmt.Errorf("unable to configure wireguard with peer: %w", err)
	}

	return func() {
		if err := conn.Link.Delete(uint32(iface.Index)); err != nil {
			log.Error(err, "unable to delete interface", "w.deviceName", iface.Name)
		}

		if err := conn.Close(); err != nil {
			log.Error(err, "unable to close netlink client")
		}
	}, nil
}
