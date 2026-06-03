# Sample App with OBI Auto-Instrumentation

A minimal Go HTTP server auto-instrumented with [OBI (eBPF-based instrumentation)](https://github.com/grafana/ebpf-autoinstrument), exporting traces via an OpenTelemetry Collector.

## Project Structure

```
app/                          # Application source
  main.go
  Containerfile
deploy/
  00-prereqs/                 # Cluster prerequisites (apply first)
    cert-manager.yaml
    opentelemetry-operator.yaml
  01-observability/           # Observability stack
    obi.yaml                  # OBI DaemonSet, ConfigMap, RBAC
    otel-collector.yaml       # OpenTelemetryCollector CR
  02-app/                     # Sample application
    app.yaml                  # Namespace, Deployment, Service
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

# On openshift, you'll also need to configure the SCC
oc adm policy add-scc-to-user -n observability -z obi privileged
```

### 3. Sample app

```bash
kubectl apply -f deploy/02-app/app.yaml
kubectl wait -n sample-app --for=condition=Available deployment/sample-app --timeout=120s
```

### Verify

Check collector logs for traces (the pod health check will make requests):

```bash
kubectl logs -n observability deployment/otel-collector
```

## Building the app image

```bash
podman build -t quay.io/<your-user>/sample-app:latest -f app/Containerfile app/
podman push quay.io/<your-user>/sample-app:latest
```

Then update the image reference in `deploy/02-app/app.yaml`.
