//go:build linux

package routing

import (
	"context"
	"fmt"
	"net"
	"syscall"

	"github.com/go-logr/logr"
	"github.com/godbus/dbus/v5"
	"github.com/jsimonetti/rtnetlink"
	"golang.org/x/sys/unix"

	"github.com/steved/kubewire/pkg/runnable"
)

type resolvedLinkDNS struct {
	Family int
	IP     [4]byte
}

type resolvedLinkDomain struct {
	Name        string
	RoutingOnly bool
}

func (r *routing) Start(ctx context.Context) (runnable.StopFunc, error) {
	log := logr.FromContextOrDiscard(ctx)

	netlink, err := rtnetlink.Dial(nil)
	if err != nil {
		return nil, fmt.Errorf("unable to initialize netlink client: %w", err)
	}

	defer func() {
		if err := netlink.Close(); err != nil {
			log.Error(err, "unable to close netlink client")
		}
	}()

	iface, err := net.InterfaceByName(r.deviceName)
	if err != nil {
		return nil, fmt.Errorf("unable to find wireguard interface %q: %w", r.deviceName, err)
	}

	for _, route := range r.routes {
		ip := net.IP(route.Addr().AsSlice())

		err = netlink.Route.Add(&rtnetlink.RouteMessage{
			Family:    unix.AF_INET,
			Table:     unix.RT_TABLE_MAIN,
			Protocol:  unix.RTPROT_STATIC,
			Scope:     unix.RT_SCOPE_LINK,
			Type:      unix.RTN_UNICAST,
			DstLength: uint8(route.Bits()),
			Attributes: rtnetlink.RouteAttributes{
				Dst:      ip,
				OutIface: uint32(iface.Index),
				Gateway:  net.ParseIP("0.0.0.0"),
			},
		})
		if err != nil {
			return nil, fmt.Errorf("unable to add route for %s: %w", route.String(), err)
		}
	}

	if r.dnsServer.IsValid() {
		dbusClient, err := dbus.ConnectSystemBus()
		if err != nil {
			return nil, fmt.Errorf("unable to create dbus client: %w", err)
		}

		defer func() {
			if err := dbusClient.Close(); err != nil {
				log.Error(err, "unable to close dbus client")
			}
		}()

		resolved := dbusClient.Object("org.freedesktop.resolve1", "/org/freedesktop/resolve1")

		err = resolved.CallWithContext(
			ctx,
			"org.freedesktop.resolve1.Manager.SetLinkDNS",
			0,
			iface.Index,
			[]resolvedLinkDNS{{Family: syscall.AF_INET, IP: r.dnsServer.As4()}},
		).Err
		if err != nil {
			return nil, fmt.Errorf("unable to set DNS for %q: %w", r.deviceName, err)
		}

		err = resolved.CallWithContext(
			ctx,
			"org.freedesktop.resolve1.Manager.SetLinkDomains",
			0,
			iface.Index,
			[]resolvedLinkDomain{{Name: "svc.cluster.local", RoutingOnly: false}},
		).Err
		if err != nil {
			return nil, fmt.Errorf("unable to set DNS for %q: %w", r.deviceName, err)
		}

		err = resolved.CallWithContext(
			ctx,
			"org.freedesktop.resolve1.Manager.SetLinkDefaultRoute",
			0,
			iface.Index,
			false,
		).Err
		if err != nil {
			return nil, fmt.Errorf("unable to set DNS for %q: %w", r.deviceName, err)
		}
	}

	return func() {}, nil
}
