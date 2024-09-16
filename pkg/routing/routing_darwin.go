//go:build darwin

package routing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/go-logr/logr"

	"github.com/steved/kubewire/pkg/runnable"
)

func (r *routing) Start(ctx context.Context) (runnable.StopFunc, error) {
	for _, route := range r.routes {
		rt := exec.Command("route", "add", "-net", route.String(), "-interface", r.deviceName)
		if _, err := rt.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("unable to add route for %s: %w", route.String(), err)
		}
	}

	if r.dnsServer.IsValid() {
		if err := os.MkdirAll("/etc/resolver", 0o755); err != nil {
			return nil, fmt.Errorf("unable to create /etc/resolver: %w", err)
		}

		contents := []byte(fmt.Sprintf("domain cluster.local\nnameserver %s\nsearch svc.cluster.local cluster.local local", r.dnsServer.String()))

		err := os.WriteFile("/etc/resolver/cluster.local", contents, 0o644)
		if err != nil {
			return nil, fmt.Errorf("unable to create /etc/resolver/cluster.local: %w", err)
		}

		_, err = exec.Command("defaults", "write", "/Library/Preferences/com.apple.mDNSResponder.plist", "AlwaysAppendSearchDomains", "-bool", "yes").CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("unable to enable mDNSResponder AlwaysAppendSearchDomains: %w", err)
		}

		_, err = exec.Command("killall", "mDNSResponder").CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("unable to restart mDNSResponder: %w", err)
		}
	}

	return func() {
		log := logr.FromContextOrDiscard(ctx)

		var errs []error

		if _, err := exec.Command("defaults", "write", "/Library/Preferences/com.apple.mDNSResponder.plist", "AlwaysAppendSearchDomains", "-bool", "no").CombinedOutput(); err != nil {
			errs = append(errs, fmt.Errorf("unable to disable mDNSResponder AlwaysAppendSearchDomains: %w", err))
		}

		if _, err := exec.Command("killall", "mDNSResponder").CombinedOutput(); err != nil {
			errs = append(errs, fmt.Errorf("unable to restart mDNSResponder: %w", err))
		}

		if err := os.Remove("/etc/resolver/cluster.local"); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("unable to remove /etc/resolver/cluster.local: %w", err))
		}

		if len(errs) > 0 {
			log.Error(errors.Join(errs...), "unable to clean up routing")
		}
	}, nil
}
