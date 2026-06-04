# Design Document: Introducing the ClusterEBPFAgent Controller

## 1. Summary

This proposal introduces a new cluster-scoped Custom Resource Definition (CRD) named `ClusterEBPFAgent` to the OpenTelemetry Operator. The goal is to automate the declarative provisioning and lifecycle management of OpenTelemetry’s eBPF out-of-process tracing engine (`otel/ebpf-instrumentation`). 

By standardizing this infrastructure layer into a dedicated controller, platform teams can unlock frictionless, language-agnostic HTTP/gRPC tracing and network topology tracking across application workloads without mutating tenant configurations, altering application binaries.

## 2. Motivation & Scope

The OpenTelemetry Operator currently handles application-level runtime SDK injections via the namespaced `Instrumentation` CRD and ingestion pipelines via `OpenTelemetryCollector`. However, it lacks a first-class, cluster-scoped automation pattern for host-level kernel telemetry generation.

Deploying and scaling eBPF daemons manually requires platform teams to handle complex Linux capability configurations, coordinate multi-tenant namespace filters, and manage specific vendor security permissions (such as Security Context Constraints on Red Hat OpenShift).

### Out of Scope / Boundaries of Responsibility
To ensure project scope safety and decouple maintenance loops, this architecture implements a **Shared Responsibility Model**:
* **Operator Domain:** The operator is exclusively responsible for the Kubernetes lifecycle automation (DaemonSet state, RBAC provisioning, platform-specific security matching).
* **eBPF SIG Domain:** The verification safety of the underlying BPF bytecode, user-space protocol translation efficiency, and Linux kernel version compatibility matrix remain strictly under the authority of the upstream eBPF Instrumentation SIG.

## 3. Architecture & Topology

The controller implements a **Standalone / Decoupled Deployment Topology**. The operator does not build or distribute a custom Collector binary. It deploys the official `otel/ebpf-instrumentation` daemon pool, which hooks the host kernel and streams raw OTLP metrics/traces natively over network sockets to a standard instance of the `OpenTelemetryCollector`.

```mermaid
graph TD

    Ctrl["<b>ClusterOBIAgent Controller</b>"]:::controller

    CR1["<b>ClusterOBIAgent</b><br>nodepool-alpha"]:::crd
    CR2["<b>ClusterOBIAgent</b><br>nodepool-beta"]:::crd
    CR1 --> Ctrl
    CR2 --> Ctrl

    subgraph Alpha ["Infrastructure Footprint: nodepool-alpha"]
        DS1["<b>DaemonSet:</b> nodepool-alpha-agent"]:::resource
        CM1["<b>ConfigMap:</b> nodepool-alpha-config"]:::resource
        SA1["<b>ServiceAccount:</b> nodepool-alpha-sa"]:::resource
        ClusterRole1["<b>ClusterRole:</b> nodepool-alpha-cluster-role"]:::resource
        ClusterRoleBinding1["<b>ClusterRoleBinding:</b> nodepool-alpha-cluster-role-binding"]:::resource
    end

    subgraph Beta ["Infrastructure Footprint: nodepool-beta"]
        DS2["<b>DaemonSet:</b> nodepool-beta-agent"]:::resource
        CM2["<b>ConfigMap:</b> nodepool-beta-config"]:::resource
        SA2["<b>ServiceAccount:</b> nodepool-beta-sa"]:::resource
        ClusterRole2["<b>ClusterRole:</b> nodepool-beta-cluster-role"]:::resource
        ClusterRoleBinding2["<b>ClusterRoleBinding:</b> nodepool-beta-cluster-role-binding"]:::resource
    end

    Ctrl --> Alpha
    Ctrl --> Beta
```

## 4. Resource Configuration Model

Each `ClusterEBPFAgent` CR instance is reconciled into a deterministic set of child resources. The controller derives the final resource manifests from three input streams: the CR spec, an environment probe (OpenShift vs. vanilla Kubernetes), and hardcoded operator defaults.

```mermaid
flowchart LR
    CR["<b>ClusterEBPFAgent</b><br/>spec.image<br/>spec.nodeSelector<br/>spec.hostPID<br/>spec.config"]
    ENV["<b>Environment Probe</b><br/>IsOpenShift?"]
    DEF["<b>Operator Defaults</b><br/>volumeMounts<br/>readOnlyRootFilesystem<br/>RBAC rules"]

    CR -->|image, nodeSelector,\nhostPID| DS["<b>DaemonSet</b><br/>podSpec"]
    CR -->|config passthrough| CM["<b>ConfigMap</b>"]
    ENV -->|"false → capabilities:\n[BPF, SYS_PTRACE, NET_RAW\nPERFMON, SYS_ADMIN, ...]"| DS
    ENV -->|"true → SCC:\nprivileged"| DS
    DEF -->|volumes, securityContext\nbaselines| DS
    DEF -->|get/list/watch rules| RBAC["<b>ClusterRole /\nClusterRoleBinding</b>"]
    CR -->|name, namespace| SA["<b>ServiceAccount</b>"]
    SA --> RBAC
```

### Field Mapping

| Output resource | Field | Source | Notes |
|---|---|---|---|
| DaemonSet | `spec.template.spec.containers[0].image` | `CR .spec.image` | Defaults to `otel/ebpf-instrument:v0.9.0` |
| DaemonSet | `spec.template.spec.nodeSelector` | `CR .spec.nodeSelector` | Empty map if unset |
| DaemonSet | `spec.template.spec.hostPID` | `CR .spec.hostPID` | Defaults to `true` |
| DaemonSet | `spec.template.spec.containers[0].securityContext.capabilities` | Environment probe | Omitted on OpenShift; SCC used instead |
| DaemonSet | `spec.template.spec.volumes` | Operator default | `emptyDir` (var-run-obi), `hostPath` (cgroup), `configMap` (config) |
| DaemonSet | `spec.template.spec.containers[0].securityContext.readOnlyRootFilesystem` | Operator default | Always `true` |
| DaemonSet | `spec.template.spec.containers[0].securityContext.runAsUser` | Operator default | Always `0` |
| ConfigMap | `data["obi-config.yml"]` | `CR .spec.config` | Untyped passthrough; operator does not interpret content |
| ServiceAccount | `metadata.name` | CR name (prefixed) | e.g. `<cr-name>-sa` |
| ClusterRole | `rules` | Operator default | `get/list/watch` on pods, nodes, services, workload resources |
| ClusterRoleBinding | `subjects[0]` | Derived from ServiceAccount | |