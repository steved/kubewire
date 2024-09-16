## kw proxy

Proxy cluster access to the target Kubernetes object.

```
kw proxy [target] [flags]
```

### Options

```
  -i, --agent-image string   Agent image to use (default "ghcr.io/steved/kubewire:latest")
  -c, --container string     Name of the container to replace
  -p, --direct               Whether to try NAT hole punching (true) or use a load balancer for access to the pod
  -h, --help                 help for proxy
  -k, --keep-resources       Keep created resources running when exiting (default true)
      --kubeconfig string    Kubernetes cfg file
      --local-address text   Local address accessible from remote agent
  -n, --namespace string     Namespace of the target object (default "default")
      --node-cidr text       Kubernetes node CIDR
  -o, --overlay string       Specify the overlay CIDR for Wireguard. Useful if auto-detection fails
      --pod-cidr text        Kubernetes pod CIDR
      --service-cidr text    Kubernetes Service CIDR
```

### Options inherited from parent commands

```
  -d, --debug   Toggle debug logging
```

### SEE ALSO

* [kw](kw.md)	 - KubeWire allows easy, direct connections to, and through, a Kubernetes cluster.

