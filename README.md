# OBI Auto-Instrumentation Demo

A distributed tracing demo using [OBI (eBPF-based instrumentation)](https://github.com/grafana/ebpf-autoinstrument) to auto-instrument Go services without code changes, exporting traces via an OpenTelemetry Collector to Jaeger.

Two simulated tenants (`tenant-alpha`, `tenant-beta`) each run an `api-gateway` → `processor` call chain. OBI instruments both namespaces and the collector's transform processor prefixes `service.name` with the namespace (e.g. `tenant-alpha/api-gateway`) so traces from each tenant are distinguishable in Jaeger.

## Project Structure

```
api-gateway/                  # HTTP frontend — calls processor on /hello
  main.go
  Containerfile
processor/                    # HTTP backend — simulates work on /processing
  main.go
  Containerfile
deploy/
  00-prereqs/                 # Cluster prerequisites (apply first)
    cert-manager.yaml
    opentelemetry-operator.yaml
  01-observability/           # Observability stack
    obi.yaml                  # OBI DaemonSet, ConfigMap, RBAC
    otel-collector.yaml       # OpenTelemetryCollector CR (with transform processor)
    jaeger.yaml               # Jaeger all-in-one + OpenShift Route
  02-app/                     # Tenant workloads
    tenant-alpha.yaml         # api-gateway + processor in tenant-alpha namespace
    tenant-beta.yaml          # api-gateway + processor in tenant-beta namespace
```

## Setup

Apply in order, waiting for each step to be ready before proceeding.

### 1. Prerequisites

```bash
kubectl apply -f deploy/00-prereqs/cert-manager.yaml
kubectl wait -n cert-manager --for=condition=Available deployment/cert-manager-webhook --timeout=120s

kubectl apply -f deploy/00-prereqs/opentelemetry-operator.yaml
kubectl wait -n opentelemetry-operator-system --for=condition=Available deployment/opentelemetry-operator-controller-manager --timeout=120s
```

### 2. Observability stack

```bash
kubectl apply -f deploy/01-observability/obi.yaml
kubectl apply -f deploy/01-observability/otel-collector.yaml
kubectl apply -f deploy/01-observability/jaeger.yaml

# On OpenShift, grant OBI the privileged SCC
oc adm policy add-scc-to-user -n observability -z obi privileged
```

### 3. Tenant apps

```bash
kubectl apply -f deploy/02-app/tenant-alpha.yaml
kubectl apply -f deploy/02-app/tenant-beta.yaml

kubectl wait -n tenant-alpha --for=condition=Available deployment/api-gateway --timeout=120s
kubectl wait -n tenant-beta  --for=condition=Available deployment/api-gateway --timeout=120s
```

### Verify

Check that traces are flowing through the collector:

```bash
kubectl logs -n observability deployment/otel-collector
```

Open the Jaeger UI (OpenShift Route created automatically):

```bash
oc get route -n observability jaeger-ui
```

Service names in Jaeger will appear as `tenant-alpha/api-gateway`, `tenant-alpha/processor`, etc.

## Building the app images

```bash
podman build -t quay.io/<your-user>/api-gateway:latest -f api-gateway/Containerfile api-gateway/
podman push quay.io/<your-user>/api-gateway:latest

podman build -t quay.io/<your-user>/processor:latest -f processor/Containerfile processor/
podman push quay.io/<your-user>/processor:latest
```

Then update the image references in `deploy/02-app/tenant-alpha.yaml` and `deploy/02-app/tenant-beta.yaml`.
