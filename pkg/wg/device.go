package wg

import (
	"net/netip"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/steved/kubewire/pkg/runnable"
)

const (
	PersistentKeepaliveInterval = 25 * time.Second
	DefaultWireguardPort        = 19070
	defaultDeviceName           = "wg0"
)

type WireguardDevicePeer struct {
	Endpoint   netip.AddrPort
	PublicKey  wgtypes.Key
	AllowedIPs []netip.Prefix
}

type WireguardDeviceConfig struct {
	Peer       WireguardDevicePeer
	PrivateKey wgtypes.Key
	ListenPort int
	Address    netip.Addr
}

type WireguardDevice interface {
	runnable.Runnable
	DeviceName() string
}

type wireguardDevice struct {
	config     WireguardDeviceConfig
	deviceName string
}

func NewWireguardDevice(cfg WireguardDeviceConfig) WireguardDevice {
	return &wireguardDevice{config: cfg, deviceName: defaultDeviceName}
}

func (w *wireguardDevice) DeviceName() string {
	return w.deviceName
}
