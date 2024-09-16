## Quickstart

To try out KubeWire locally, install [minikube](https://minikube.sigs.k8s.io/docs/start/) and get started:

1. `minikube start`
2. `kubectl apply -f examples/docs/*.yml`
3. Run kw:
   ```
   $ ip=$(minikube ssh -- getent hosts host.minikube.internal | awk '{print $1}')
   $ kw proxy deploy/hello-world --local-address 192.168.65.254:19070 --service-cidr 10.96.0.0/12
   ```
4. Access the cluster!
   ```
   PGPASSWORD=mysupersecretpassword psql -h postgresql.default -p 5432 -U postgres -c 'create database testdb'
   ```
