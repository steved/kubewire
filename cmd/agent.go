//go:build linux

package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/steved/kubewire/pkg/agent"
	"github.com/steved/kubewire/pkg/config"
)

func init() {
	var configFile string

	remoteCmd := &cobra.Command{
		Use:    "agent",
		Short:  "Runs wireguard agent",
		Hidden: true, // users shouldn't run this themselves
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			configContents, err := os.ReadFile(configFile)
			if err != nil {
				return fmt.Errorf("unable to open config file %q: %w", configFile, err)
			}

			cfg := config.Wireguard{}

			if err := yaml.Unmarshal(configContents, &cfg); err != nil {
				return fmt.Errorf("unable to read config file %q: %w", configFile, err)
			}

			var proxyExcludedPorts []string
			localPortsExcludeProxy := os.Getenv("LOCAL_PORTS_EXCLUDE_PROXY")
			if localPortsExcludeProxy != "" {
				proxyExcludedPorts = strings.Split(localPortsExcludeProxy, ",")
			}

			istioInterceptMode := os.Getenv("ISTIO_INTERCEPTION_MODE")
			istioEnabled := istioInterceptMode != ""
			if istioEnabled {
				// istio health and prometheus ports
				proxyExcludedPorts = append(proxyExcludedPorts, "15020", "15021")
			}

			return agent.Run(logr.NewContext(ctx, log), cfg, istioEnabled, proxyExcludedPorts)
		},
	}

	remoteCmd.Flags().StringVarP(&configFile, "config", "c", "/app/config/wg.yml", "path to configuration file")

	rootCmd.AddCommand(remoteCmd)
}
