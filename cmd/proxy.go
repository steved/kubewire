package cmd

import (
	"context"
	goflag "flag"
	"fmt"
	"net/netip"
	"os"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/steved/kubewire/pkg/config"
	"github.com/steved/kubewire/pkg/kuberneteshelpers"
	"github.com/steved/kubewire/pkg/proxy"
)

func init() {
	var (
		kubeconfig, overlayPrefix string
		directAccess              bool
	)

	cfg := config.NewConfig()

	proxyCmd := &cobra.Command{
		Use:   "proxy [target]",
		Short: "Proxy cluster access to the target Kubernetes object.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			client, restConfig, err := kuberneteshelpers.ClientConfig(kubeconfig)
			if err != nil {
				return fmt.Errorf("failed to create kubernetes client: %w", err)
			}

			obj, err := kuberneteshelpers.ResolveObject(kubeconfig, cfg.Namespace, args)
			if err != nil {
				return fmt.Errorf("failed to resolve target kubernetes object: %w", err)
			}

			cfg.TargetObject = obj

			if err := proxy.ResolveWireguardConfig(ctx, cfg, client, overlayPrefix, directAccess); err != nil {
				return fmt.Errorf("unable to create wireguard config: %w", err)
			}

			return proxy.Run(logr.NewContext(ctx, log), cfg, client, restConfig)
		},
	}

	proxyCmd.Flags().StringVarP(&kubeconfig, "kubeconfig", "", os.Getenv("KUBECONFIG"), "Kubernetes cfg file")
	proxyCmd.Flags().StringVarP(&cfg.Namespace, "namespace", "n", "default", "Namespace of the target object")
	proxyCmd.Flags().StringVarP(&cfg.Container, "container", "c", "", "Name of the container to replace")
	proxyCmd.Flags().StringVarP(&overlayPrefix, "overlay", "o", "", "Specify the overlay CIDR for Wireguard. Useful if auto-detection fails")
	proxyCmd.Flags().BoolVarP(&directAccess, "direct", "p", false, "Whether to try NAT hole punching (true) or use a load balancer for access to the pod")
	proxyCmd.Flags().StringVarP(&cfg.AgentImage, "agent-image", "i", fmt.Sprintf("ghcr.io/steved/kubewire:%s", config.Version), "Agent image to use")
	proxyCmd.Flags().BoolVarP(&cfg.KeepResources, "keep-resources", "k", true, "Keep created resources running when exiting")

	// Workaround for lack of "TextVar" support in pflag / cobra
	goflag.TextVar(&cfg.KubernetesClusterDetails.ServiceCIDR, "service-cidr", netip.Prefix{}, "Kubernetes Service CIDR")
	goflag.TextVar(&cfg.KubernetesClusterDetails.NodeCIDR, "node-cidr", netip.Prefix{}, "Kubernetes node CIDR")
	goflag.TextVar(&cfg.KubernetesClusterDetails.PodCIDR, "pod-cidr", netip.Prefix{}, "Kubernetes pod CIDR")
	goflag.TextVar(&cfg.Wireguard.LocalAddress, "local-address", netip.AddrPort{}, "Local address accessible from remote agent")
	proxyCmd.Flags().AddGoFlagSet(goflag.CommandLine)

	rootCmd.AddCommand(proxyCmd)
}
