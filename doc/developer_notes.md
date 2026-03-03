# Development Notes

## Quick Reference

| Task | Command |
|------|---------|
| Build binary | `make build
| Run all tests | `make test` |
| Run tests (skip staticcheck) | `make test-skip-static` |
| Run single package tests | `go test -v ./pkg/allocate/` |
| Update dependencies | `make update-deps` |
| Regenerate API code | `make generate-api` |
| Update clientsets/informers/listers | `./hack/update-codegen.sh` |
| Verify generated code | `./hack/verify-codegen.sh` |
| Create local cluster | `make kind` |

## Dependencies

```
go mod tidy
go mod vendor
go mod verify
```

Or use the convenience target: `make update-deps`

## Static Analysis

The project uses `go vet` and [staticcheck](https://staticcheck.io/). Both are
run by `make test`.  To skip staticcheck for faster iteration:

```
make test-skip-static
```

## Testing Framework

* **Unit tests** use [Ginkgo v2](https://onsi.github.io/ginkgo/) + [Gomega](https://onsi.github.io/gomega/) with dot-imports.
* Some packages (e.g., `pkg/storage/kubernetes/`) use standard `testing.T` table-driven tests.
* controller-runtime `envtest` is used for reconciler and webhook integration tests.
* Fake Kubernetes clients (`fake.NewClientset(...)`) are used for unit testing without a cluster.

## Running with CNI's `docker-run.sh`


Put plugins in `/opt/cni/bin` and configs in `/etc/cni/net.d` -- README config should be fine.

```
export CNI_PATH=/opt/cni/bin/
export NETCONFPATH=/etc/cni/net.d
CNI_PATH=$CNI_PATH ./docker-run.sh --rm busybox:latest ifconfig
```

## Running in Kube

Create the config...

```
cat <<EOF | kubectl create -f -
apiVersion: "k8s.cni.cncf.io/v1"
kind: NetworkAttachmentDefinition
metadata:
  name: macvlan-conf
spec:
  config: '{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28",
        "log_file" : "/tmp/whereabouts.log",
        "log_level" : "debug",
        "gateway": "192.168.2.1"
      }
    }'
EOF
```

Kick off a pod...

```
cat <<EOF | kubectl create -f -
apiVersion: v1
kind: Pod
metadata:
  name: samplepod
  annotations:
    k8s.v1.cni.cncf.io/networks: macvlan-conf
spec:
  containers:
  - name: samplepod
    command: ["/bin/bash", "-c", "sleep 2000000000000"]
    image: dougbtv/centos-network
EOF
```

## Using the scale script  `/scripts/scale-test.sh`

1. This will not work unless you have a running cluster 
   A simple way to spin a cluster to use this with is by using 
  ```
  ./hack/e2e-setup-kind-cluster -n 3
  ```
2. This script leverages the `whereaboutsScaleNAD` and `scaleTestDeployment` yamls in /yamls
3. To modify the number of pods spun by the script, change the replicas value in the `scaleTestDeployment` yaml

## Running whereabouts e2e locally

1. To run whereabouts e2e locally you need the godotenv package installed
   run `go install github.com/joho/godotenv/cmd/godotenv@latest`
   godot env allows you to pass an env file to go test
2. In the whereabouts dir, run 'make kind' -> this will create a kind cluster running whereabouts
3. cd to the /e2e dir and create a .env file with this value `KUBECONFIG=$HOME/.kube/config` -> this is where kind writes the kubeconfig by default
4. run [[ ! -z "$KUBECONFIG" ]] && echo "$KUBECONFIG" || echo "$HOME/.kube/config" to find the location of your kubeconfig 
5. add KUBECONFIG=<path/to/kubeconfig> to your .env
6. run godotenv -f <path/to/.env> go test -v . -timeout=1h
