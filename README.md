# KubeWire

[![](https://github.com/steved/kubewire/actions/workflows/main.yml/badge.svg)](https://github.com/steved/kubewire/actions)

KubeWire allows easy, direct connections to, and through, a Kubernetes cluster.

## How?

KubeWire uses [WireGuard](https://www.wireguard.com/) to securely and easily connect the two networks.

### Installation

KubeWire currently supports Linux and MacOS.

* Get the [latest release](https://github.com/steved/kubewire/releases/latest) for your operating system
* Unpack and install the binary:
  ```
  tar -C /usr/local/bin -xf kw.tar.gz kw
  ```
  
### Usage

KubeWire requires access to Kubernetes (by default; `~/.kube/config`) and a deployment or statefulset to proxy traffic for and through. 

**Note**: `proxy` will modify the target resource in the cluster. Only execute the command in clusters where restoration is simple (e.g. with `helm`) or changes are not destructive.

**Note**: `proxy` requires root access to modify network resources.

```
$ sudo -E kw proxy deploy/hello-world

2024-09-16T12:33:33.403-0700	INFO	Waiting for load balancer to be ready	{"service": "wg-hello-world", "namespace": "default"}
2024-09-16T12:33:36.316-0700	INFO	Load balancer ready, waiting for DNS to resolve	{"hostname": "example.elb.us-west-2.amazonaws.com"}
2024-09-16T12:36:16.521-0700	INFO	Kubernetes setup complete
2024-09-16T12:36:16.553-0700	INFO	Wireguard device setup complete
2024-09-16T12:36:16.592-0700	INFO	Routing setup complete
2024-09-16T12:36:16.592-0700	INFO	Started. Use Ctrl-C to exit...
```

Optional arguments allow targeting of namespaces (`-n`), containers (`-c`).

When `proxy` exits, created Kubernetes resources such as services or network policies will not be deleted to allow for easier resumption of an existing session.
Resources will be removed at exit with `--keep-resources=false` is passed.

Once connected, access Kubernetes cluster resources directly. Including the K8s API:
```
$ curl -k https://kubernetes.default
{
  "kind": "Status",
  "apiVersion": "v1",
  "metadata": {},
  "status": "Failure",
  "message": "forbidden: User \"system:anonymous\" cannot get path \"/\"",
  "reason": "Forbidden",
  "details": {},
  "code": 403
}
```

See [kw_proxy.md](./docs/kw_proxy.md) for detailed usage information.

#### Direct access

By default, KubeWire will access the pod by using a `LoadBalancer` service. KubeWire has been tested in AWS, GCP, and Azure.

In environments where direct access is allowed from local host to remote pod or vice versa, direct modes can be used.

If the remote pod has direct access to the local host, the accessible address of the local host can be passed to `proxy`.
For example, for minikube:
```
$ ip=$(minikube ssh -- getent hosts host.minikube.internal | awk '{print $1}')
$ sudo -E kw proxy --local-address "$ip:19070" deploy/hello-world
```

See [examples/minikube](./examples/minikube/README.md) for more information.

If the remote pod is accessible to the internet through a NAT that supports Endpoint-Independent mapping, both the local and remote instances can attempt to discover their remote address, coordinate ports, and connect directly.
For example, in AWS with a [NAT instance](https://fck-nat.dev) instead of a NAT gateway:
```
$ sudo -E kw proxy --direct deploy/hello-world
```

### Limitations

* Windows is not supported
* IPv6 is not supported
* Istio support has not been tested with ambient mesh 

### Troubleshooting

#### WireGuard connectivity

`wg` can be used to check WireGuard connectivity locally and in the remote pod:
```
$ wg

interface: utun4
  public key: (hidden)
  private key: (hidden)
  listening port: 19070

peer: (hidden)
  endpoint: (address):19070
  allowed ips: 100.64.0.0/16, 172.20.0.0/16, 10.0.0.0/16, 10.1.0.0/28
  latest handshake: 11 seconds ago
  transfer: 15.44 KiB received, 46.79 KiB sent
  persistent keepalive: every 25 seconds
  
$ kubectl exec -it $(kubectl get po -l app.kubernetes.io/name=hello-world -oname) -- wg

interface: wg0
  public key: (hidden)
  private key: (hidden)
  listening port: 19070

peer: (hidden)
  endpoint: (address):19070
  allowed ips: 100.64.0.0/16, 172.20.0.0/16, 10.0.0.0/16, 10.1.0.0/28
  latest handshake: 21 seconds ago
  transfer: 14.08 KiB received, 22.81 KiB sent
  persistent keepalive: every 25 seconds
```

If "latest handshake" isn't displayed or was a number of minutes ago, the connection may not be established.

### Building from source

KubeWire support Go v1.23. In order to build KubeWire from source:

* Clone this repository
* Build and run the executable:
  ```
  make build
  .bin/kubewire
  ```

## Why?

### Performance

In limited testing in comparison to `mirrord`, `kw` is multiple times less latent.

Setup:
```
PGPASSWORD=mysupersecretpassword psql -h postgresql.default -p 5432 -U postgres -c 'create database testdb'
PGPASSWORD=mysupersecretpassword pgbench -i -s 25 -h postgresql.default -U postgres testdb
PGPASSWORD=mysupersecretpassword pgbench -h postgresql.default -U postgres -c 5 -j 10 -R 25 -T 30 testdb
```

KubeWire:
```
pgbench (14.13 (Homebrew), server 16.4)
starting vacuum...end.
transaction type: <builtin: TPC-B (sort of)>
scaling factor: 1
query mode: simple
number of clients: 5
number of threads: 5
duration: 30 s
number of transactions actually processed: 746
latency average = 13.604 ms
latency stddev = 3.557 ms
rate limit schedule lag: avg 3.242 (max 18.100) ms
initial connection time = 23.029 ms
tps = 24.885951 (without initial connection time)
```

mirrord:
```
$  mirrord exec -s '' -n default -t pod/hello-world-86cfdd5fc6-zqr75 --steal --fs-mode local -- /bin/sh -c "PGPASSWORD=mysupersecretpassword pgbench -h postgresql.default -U postgres -c 5 -j 10 -R 25 -T 30 testdb"
starting vacuum...end.
transaction type: <builtin: TPC-B (sort of)>
scaling factor: 1
query mode: simple
number of clients: 5
number of threads: 5
duration: 30 s
number of transactions actually processed: 745
latency average = 34.005 ms
latency stddev = 25.867 ms
rate limit schedule lag: avg 6.589 (max 114.021) ms
initial connection time = 152.967 ms
tps = 24.910868 (without initial connection time)
```

### Similar projects

* [k8s-insider](https://github.com/TrueGoric/k8s-insider) - no MacOS support
* [kubetunnel](https://github.com/we-dcode/kubetunnel) - operates at the service level
* [mirrord](https://github.com/metalbear-co/mirrord/) - performance

## Contributing

Issues and pull requests are always welcome!

## License

Use of this software is subject to important terms and conditions as set forth in the [LICENSE](./LICENSE) file.
