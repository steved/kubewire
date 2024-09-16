package routing

import (
	"net/netip"

	"github.com/steved/kubewire/pkg/runnable"
)

type routing struct {
	deviceName string
	routes     []netip.Prefix
	dnsServer  netip.Addr
}

func NewRouting(deviceName string, dnsServer netip.Addr, routes ...netip.Prefix) runnable.Runnable {
	return &routing{deviceName: deviceName, dnsServer: dnsServer, routes: routes}
}
