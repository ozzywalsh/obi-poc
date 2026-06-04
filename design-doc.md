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
    CR["<b>ClusterEBPFAgent</b><br/>spec.image<br/>spec.nodeSelector<br/>spec.hostPID<br/>spec.mode<br/>spec.additionalCapabilities<br/>spec.config"]
    DEF["<b>Operator Defaults</b><br/>volumeMounts<br/>readOnlyRootFilesystem<br/>RBAC rules"]

    CR -->|image, nodeSelector, hostPID| DS["<b>DaemonSet</b><br/>podSpec"]
    CR -->|mode + additionalCapabilities| CAPS["capability set\n(see §5)"]
    CR -->|config passthrough| CM["<b>ConfigMap</b>"]
    CAPS --> DS
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
| DaemonSet | `spec.template.spec.containers[0].securityContext.capabilities` | `CR .spec.mode` + `CR .spec.additionalCapabilities` | Derived deterministically; see §5 |
| DaemonSet | `spec.template.spec.volumes` | Operator default | `emptyDir` (var-run-obi), `hostPath` (cgroup), `configMap` (config) |
| DaemonSet | `spec.template.spec.containers[0].securityContext.readOnlyRootFilesystem` | Operator default | Always `true` |
| DaemonSet | `spec.template.spec.containers[0].securityContext.runAsUser` | Operator default | Always `0` |
| ConfigMap | `data["obi-config.yml"]` | `CR .spec.config` | Untyped passthrough; operator does not interpret content |
| ServiceAccount | `metadata.name` | CR name (prefixed) | e.g. `<cr-name>-sa` |
| ClusterRole | `rules` | Operator default | `get/list/watch` on pods, nodes, services, workload resources |
| ClusterRoleBinding | `subjects[0]` | Derived from ServiceAccount | |

## 5. API Specification

The Go type definition below is the canonical API surface for `ClusterEBPFAgent`, expressed using kubebuilder markers. The rendered CRD manifest generated from this file is available at [`clusterebpfagent-crd.yaml`](./clusterebpfagent-crd.yaml).

```go
// EBPFAgentMode defines the set of OBI tracers to activate, which determines
// the Linux capabilities provisioned by the operator.
//
// application: HTTP/gRPC application observability via uprobes.
// network:     Network flow observability via TC programs.
// full:        Both application and network observability.
//
// +kubebuilder:validation:Enum=application;network;full
type EBPFAgentMode string

const (
	EBPFAgentModeApplication EBPFAgentMode = "application"
	EBPFAgentModeNetwork     EBPFAgentMode = "network"
	EBPFAgentModeFull        EBPFAgentMode = "full"
)

// ClusterEBPFAgentSpec defines the desired state of ClusterEBPFAgent.
type ClusterEBPFAgentSpec struct {
	// Image is the container image to use for the OBI DaemonSet.
	// Defaults to the operator's bundled OBI image version.
	// +optional
	Image string `json:"image,omitempty"`

	// ImagePullPolicy defines the pull policy for the OBI container image.
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// ImagePullSecrets is a list of references to secrets for pulling the OBI image.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// Mode selects the OBI operation mode and determines the base Linux capability
	// set provisioned by the operator. Defaults to application.
	//
	// application: provisions capabilities for HTTP/gRPC uprobe-based tracing.
	// network:     provisions capabilities for TC-based network flow observability.
	// full:        union of application and network capability sets.
	//
	// +optional
	// +kubebuilder:default=application
	Mode EBPFAgentMode `json:"mode,omitempty"`

	// AdditionalCapabilities supplements the base capability set derived from Mode.
	// Use this for capabilities that are not required by the declared mode but are
	// needed for specific kernel configurations or optional OBI features, for example:
	//
	//   - SYS_ADMIN: required for Go library-level trace context propagation
	//                (bpf_probe_write_user). Also required on AKS and EKS where
	//                kernel.perf_event_paranoid > 1 by default.
	//
	// +optional
	AdditionalCapabilities []corev1.Capability `json:"additionalCapabilities,omitempty"`

	// HostPID controls whether the DaemonSet pods share the host PID namespace.
	// Required for OBI to resolve eBPF probe PIDs to container workloads.
	// Defaults to true.
	// +optional
	// +kubebuilder:default=true
	HostPID bool `json:"hostPID,omitempty"`

	// NodeSelector constrains which nodes the DaemonSet pods are scheduled to.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations allow the DaemonSet pods to be scheduled on nodes with matching taints.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Resources defines compute resource requests and limits for the OBI container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Config is the raw OBI configuration passed through verbatim to the agent ConfigMap.
	// The operator does not interpret or validate the content; it is mounted at
	// /config/obi-config.yml inside the DaemonSet pod.
	// Refer to the OBI configuration documentation for available fields.
	// +optional
	Config string `json:"config,omitempty"`
}

// ClusterEBPFAgentStatus defines the observed state of ClusterEBPFAgent.
type ClusterEBPFAgentStatus struct {
	// Conditions represent the latest available observations of the ClusterEBPFAgent state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the most recent generation observed for this resource.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Image is the OBI container image resolved and applied to the DaemonSet.
	// +optional
	Image string `json:"image,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=cebpfa
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Mode",type="string",JSONPath=".spec.mode",description="OBI operation mode"
// +kubebuilder:printcolumn:name="Image",type="string",JSONPath=".status.image",description="Applied OBI image"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ClusterEBPFAgent is the schema for the clusterebpfagents API.
// Each instance provisions an OBI DaemonSet and associated RBAC across the cluster.
type ClusterEBPFAgent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterEBPFAgentSpec   `json:"spec,omitempty"`
	Status ClusterEBPFAgentStatus `json:"status,omitempty"`
}
```

## 6. Security Posture

OBI instruments the host kernel via eBPF and requires elevated privileges by design. This section defines exactly which privileges are granted, how they are derived, and why.

> **Operator vs. helm chart default:** The upstream helm chart defaults to `privileged: true` — an acknowledged [developer experience shortcut](https://github.com/grafana/beyla/pull/528) for local environments such as Kind and Docker Desktop. The operator deliberately does not inherit this default. It provisions the minimal capability set required for the declared operation mode, which is the intended production posture.

### 5.1 Host Namespaces

| Field | Value | Source | Rationale |
|---|---|---|---|
| `hostPID` | `true` | `CR .spec.hostPID` (default: `true`) | Required for cross-container process visibility; eBPF probes cannot resolve PIDs to workloads without it |

### 5.2 Linux Capabilities

The capability set is derived from two inputs: `spec.mode` selects the base set for the declared operation mode; `spec.additionalCapabilities` adds any user-supplied extras. The final set is their union.

```
BASE = { BPF, PERFMON, NET_RAW }

APP  = BASE ∪ { SYS_PTRACE, DAC_READ_SEARCH, CHECKPOINT_RESTORE }
NET  = BASE ∪ { NET_ADMIN }
FULL = APP  ∪ NET

final_capabilities = mode_set(spec.mode) ∪ spec.additionalCapabilities
```

| Capability | application | network | full | Rationale |
|---|:---:|:---:|:---:|---|
| `BPF` | ✓ | ✓ | ✓ | Load and attach eBPF programs |
| `PERFMON` | ✓ | ✓ | ✓ | Access perf events; load BPF programs (kernel ≥ 5.8) |
| `NET_RAW` | ✓ | ✓ | ✓ | `AF_PACKET` raw sockets for socket filter programs |
| `SYS_PTRACE` | ✓ | — | ✓ | Access `/proc/pid/exe`; inspect ELF binaries across container namespaces |
| `DAC_READ_SEARCH` | ✓ | — | ✓ | Access `/proc/self/mem` and ELF files across UID boundaries |
| `CHECKPOINT_RESTORE` | ✓ | — | ✓ | Access `/proc` symlinks for process and system info |
| `NET_ADMIN` | — | ✓ | ✓ | TC (`BPF_PROG_TYPE_SCHED_CLS`) programs for network monitoring and context propagation |
| `SYS_ADMIN` | — | — | — | Via `spec.additionalCapabilities` only; see note below |

> **`SYS_ADMIN`:** Required for Go library-level trace context propagation via `bpf_probe_write_user`. Also required on AKS and EKS, where `kernel.perf_event_paranoid > 1` by default, making `PERFMON` alone insufficient. Users on self-managed clusters who do not need Go distributed tracing can omit it. The operator sets `OTEL_EBPF_ENFORCE_SYS_CAPS=1` unconditionally so that any capability mismatch surfaces immediately as a pod failure rather than silently degraded telemetry.

### 5.3 Volume Mounts

| Volume | Type | Mount path | Rationale |
|---|---|---|---|
| `obi-config` | `ConfigMap` | `/config` (read-only) | Agent configuration passthrough |
| `var-run-obi` | `emptyDir` | `/var/run/obi` | Ephemeral socket between eBPF probe and user-space agent |
| `cgroup` | `hostPath: /sys/fs/cgroup` | `/sys/fs/cgroup` | cgroup membership resolution; maps sockets to container workloads |
| `kernel-security` | `hostPath: /sys/kernel/security` | `/sys/kernel/security` (read-only) | Kernel lockdown mode detection; determines whether `bpf_probe_write_user` is available under Secure Boot |