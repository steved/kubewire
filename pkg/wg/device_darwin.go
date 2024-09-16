//go:build darwin

package wg

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/go-logr/logr"
	"github.com/tailscale/wireguard-go/conn"
	"github.com/tailscale/wireguard-go/device"
	"github.com/tailscale/wireguard-go/ipc"
	"github.com/tailscale/wireguard-go/tun"

	"github.com/steved/kubewire/pkg/runnable"
)

func (w *wireguardDevice) Start(ctx context.Context) (runnable.StopFunc, error) {
	log := logr.FromContextOrDiscard(ctx)

	tunDev, err := tun.CreateTUN("utun", device.DefaultMTU)
	if err != nil {
		return nil, fmt.Errorf("unable to create utun device: %w", err)
	}

	w.deviceName, err = tunDev.Name()
	if err != nil {
		return nil, fmt.Errorf("unable to obtain utun device name: %w", err)
	}

	ipcDev, err := ipc.UAPIOpen(w.deviceName)
	if err != nil {
		return nil, fmt.Errorf("unable to create proxy socket: %w", err)
	}

	deviceLogger := &device.Logger{
		Verbosef: func(format string, args ...any) {
			log.V(1).Info(fmt.Sprintf(format, args...))
		},
		Errorf: func(format string, args ...any) {
			log.Error(nil, fmt.Sprintf(format, args...))
		},
	}

	dev := device.NewDevice(tunDev, conn.NewDefaultBind(), deviceLogger)

	ipcListener, err := ipc.UAPIListen(w.deviceName, ipcDev)
	if err != nil {
		return nil, fmt.Errorf("unable to create proxy socket listener: %w", err)
	}

	go func() {
		for {
			newConn, err := ipcListener.Accept()

			log.V(1).Info("accepting new connection")

			if err != nil {
				log.V(1).Info("unable to accept new connection", "error", err.Error())
			} else {
				go dev.IpcHandle(newConn)
			}
		}
	}()

	var replacePeerConfig strings.Builder

	replacePeerConfig.WriteString("replace_peers=true\n")
	replacePeerConfig.WriteString(fmt.Sprintf("public_key=%s\n", hex.EncodeToString(w.config.Peer.PublicKey[:])))

	if w.config.Peer.Endpoint.IsValid() {
		replacePeerConfig.WriteString(fmt.Sprintf("endpoint=%s\n", w.config.Peer.Endpoint.String()))
		replacePeerConfig.WriteString(fmt.Sprintf("persistent_keepalive_interval=%d\n", int(PersistentKeepaliveInterval.Seconds())))
	}

	for _, ip := range w.config.Peer.AllowedIPs {
		replacePeerConfig.WriteString(fmt.Sprintf("allowed_ip=%s\n", ip.String()))
	}

	err = dev.IpcSet(replacePeerConfig.String())
	if err != nil {
		return nil, fmt.Errorf("unable to configure wireguard device with new peer: %w", err)
	}

	err = dev.IpcSet(fmt.Sprintf("private_key=%s", hex.EncodeToString(w.config.PrivateKey[:])))
	if err != nil {
		return nil, fmt.Errorf("unable to setup %s: %w", w.deviceName, err)
	}

	listenPort := DefaultWireguardPort
	if w.config.ListenPort < 0 {
		listenPort = 0
	} else if w.config.ListenPort != 0 {
		listenPort = w.config.ListenPort
	}

	err = dev.IpcSet(fmt.Sprintf("listen_port=%d", listenPort))
	if err != nil {
		return nil, fmt.Errorf("unable to setup %s: %w", w.deviceName, err)
	}

	output, err := exec.Command("ifconfig", w.deviceName, "inet", w.config.Address.String(), w.config.Address.String()).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("unable to setup %s with ifconfig (%w): %s", w.deviceName, err, string(output))
	}

	output, err = exec.Command("ifconfig", w.deviceName, "up").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("unable to setup %s with ifconfig (%w): %s", w.deviceName, err, string(output))
	}

	return func() {
		err := errors.Join(
			ipcListener.Close(),
			ipcDev.Close(),
			tunDev.Close(),
		)

		if err != nil {
			log.Error(err, "unable to cleanly terminate wireguard device")
		}
	}, nil
}
