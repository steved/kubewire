## Quickstart

To try out KubeWire locally, install [minikube](https://minikube.sigs.k8s.io/docs/start/) and get started:

1. `minikube start`
2. `kubectl apply -f examples/minikube/hello-world.yml -f examples/minikube/postgresql.yml`
3. Run kw:
   ```
   $ ip=$(minikube ssh -- getent hosts host.minikube.internal | awk '{print $1}')
   $ sudo -E kw proxy deploy/hello-world --local-address "$ip:19070" --service-cidr 10.96.0.0/12
   ```
4. Access the cluster!
   ```
   PGPASSWORD=mysupersecretpassword psql -h postgresql.default -p 5432 -U postgres -c 'create database testdb'
   ```
