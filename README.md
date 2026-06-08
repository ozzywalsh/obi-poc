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
    jaeger.yaml               # Jaeger all-in-one for trace visualization
  02-app/                     # Sample applications (multi-tenant)
    tenant-alpha.yaml          # Tenant Alpha namespace, deployments, services
    tenant-beta.yaml           # Tenant Beta namespace, deployments, services
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

### 3. Sample apps (multi-tenant)

The sample apps are deployed across two namespaces (`tenant-alpha` and `tenant-beta`) to demonstrate multi-tenant instrumentation. Each namespace runs its own set of services (api-gateway, processor).

```bash
kubectl apply -f deploy/02-app/tenant-alpha.yaml
kubectl apply -f deploy/02-app/tenant-beta.yaml

kubectl wait -n tenant-alpha --for=condition=Available deployment/api-gateway --timeout=120s
kubectl wait -n tenant-beta --for=condition=Available deployment/api-gateway --timeout=120s
```

### Verify

Check collector logs for traces (the pod health check will make requests):

```bash
kubectl logs -n observability deployment/otel-collector
```

View traces in Jaeger:

```bash
kubectl port-forward -n observability svc/jaeger 16686:16686
# Open http://localhost:16686
```

## Building the app image

```bash
podman build -t quay.io/<your-user>/sample-app:latest -f app/Containerfile app/
podman push quay.io/<your-user>/sample-app:latest
```

Then update the image references in `deploy/02-app/tenant-alpha.yaml` and `deploy/02-app/tenant-beta.yaml`.

## Known Limitations

OBI does not work in Kubernetes-in-Docker setups (e.g. kind). OBI's eBPF instrumentation requires access to the host kernel, which is not available when Kubernetes nodes run as Docker containers. Use a VM-based cluster (e.g. minikube with the kvm2 driver, or a bare-metal/cloud cluster) instead.
