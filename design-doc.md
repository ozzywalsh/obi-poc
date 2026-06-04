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
	// The discovery.instrument section is reserved — it is generated by the controller
	// from OBIInstrumentation resources and must not be set here.
	// Refer to the OBI configuration documentation for available fields.
	// +optional
	Config string `json:"config,omitempty"`

	// TenantDelegation controls whether OBIInstrumentation resources in tenant
	// namespaces are honoured by the controller.
	// +optional
	TenantDelegation TenantDelegationSpec `json:"tenantDelegation,omitempty"`
}

// TenantDelegationMode defines how OBIInstrumentation resources are collected.
//
// AllowList: only namespaces in NamespacesAllowList may contribute instrumentation.
// AlwaysCollect: all namespaces may contribute instrumentation.
//
// +kubebuilder:validation:Enum=AllowList;AlwaysCollect
type TenantDelegationMode string

const (
	TenantDelegationModeAllowList     TenantDelegationMode = "AllowList"
	TenantDelegationModeAlwaysCollect TenantDelegationMode = "AlwaysCollect"
)

// TenantDelegationSpec defines the policy for OBIInstrumentation delegation.
type TenantDelegationSpec struct {
	// Mode controls which namespaces may contribute OBIInstrumentation resources.
	// Defaults to AllowList.
	// +optional
	// +kubebuilder:default=AllowList
	Mode TenantDelegationMode `json:"mode,omitempty"`

	// NamespacesAllowList is the explicit list of namespaces permitted to create
	// OBIInstrumentation resources. Only used when Mode is AllowList.
	// +optional
	NamespacesAllowList []string `json:"namespacesAllowList,omitempty"`
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

The rendered CRD for `OBIInstrumentation` is available at [`obiinstrumentation-crd.yaml`](./obiinstrumentation-crd.yaml).

```go
// OBIInstrumentationSpec defines the desired state of OBIInstrumentation.
type OBIInstrumentationSpec struct {
	// PodAnnotations defines the pod annotation selectors used to identify which
	// pods in this namespace should be instrumented by OBI.
	//
	// Each key-value pair must be present as an annotation on the pod for it to
	// be selected. Multiple entries are AND'd together.
	//
	// The controller compiles this into an OBI discovery filter scoped to the
	// namespace of this resource. Pods in other namespaces are never selected
	// regardless of their annotations.
	//
	// +optional
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=obiinstr
// +kubebuilder:subresource:status

// OBIInstrumentation is the schema for the obiinstrumentations API.
// It allows namespace-scoped delegation of OBI instrumentation targeting to
// project administrators, without exposing host-level privileges or affecting
// workloads outside the resource's own namespace.
type OBIInstrumentation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OBIInstrumentationSpec   `json:"spec,omitempty"`
	Status OBIInstrumentationStatus `json:"status,omitempty"`
}
```

## 6. Security Posture

OBI instruments the host kernel via eBPF and requires elevated privileges by design. This section defines exactly which privileges are granted, how they are derived, and why.

> **Operator vs. helm chart default:** The upstream helm chart defaults to `privileged: true` — an acknowledged [developer experience shortcut](https://github.com/grafana/beyla/pull/528) for local environments such as Kind and Docker Desktop. The operator deliberately does not inherit this default. It provisions the minimal capability set required for the declared operation mode, which is the intended production posture.

### 6.1 Host Namespaces

| Field | Value | Source | Rationale |
|---|---|---|---|
| `hostPID` | `true` | `CR .spec.hostPID` (default: `true`) | Required for cross-container process visibility; eBPF probes cannot resolve PIDs to workloads without it |

### 6.2 Linux Capabilities

The operator looks up the base capability set for `spec.mode` from a fixed map, then appends `spec.additionalCapabilities`.

```go
var modeCaps = map[EBPFAgentMode][]corev1.Capability{
    EBPFAgentModeApplication: {BPF, PERFMON, NET_RAW, SYS_PTRACE, DAC_READ_SEARCH, CHECKPOINT_RESTORE},
    EBPFAgentModeNetwork:     {BPF, PERFMON, NET_RAW, NET_ADMIN},
    EBPFAgentModeFull:        {BPF, PERFMON, NET_RAW, SYS_PTRACE, DAC_READ_SEARCH, CHECKPOINT_RESTORE, NET_ADMIN},
}

caps := modeCaps[spec.Mode]
caps = append(caps, spec.AdditionalCapabilities...)
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

### 6.3 Volume Mounts

| Volume | Type | Mount path | Rationale |
|---|---|---|---|
| `obi-config` | `ConfigMap` | `/config` (read-only) | Agent configuration passthrough |
| `var-run-obi` | `emptyDir` | `/var/run/obi` | Ephemeral socket between eBPF probe and user-space agent |
| `cgroup` | `hostPath: /sys/fs/cgroup` | `/sys/fs/cgroup` | cgroup membership resolution; maps sockets to container workloads |
| `kernel-security` | `hostPath: /sys/kernel/security` | `/sys/kernel/security` (read-only) | Kernel lockdown mode detection; determines whether `bpf_probe_write_user` is available under Secure Boot |

### 6.4 OpenShift: SecurityContextConstraints

On OpenShift, pod security is governed by SCCs rather than raw Linux capabilities. The operator detects OpenShift at reconcile time and takes a different path: instead of setting `capabilities.add` on the container, it creates and manages a purpose-built `SecurityContextConstraint` and binds it directly to the agent `ServiceAccount`.

This is the established pattern for OpenShift operators that deploy privileged workloads — see the [NetObserv operator](https://github.com/netobserv/netobserv-operator/blob/main/internal/controller/ebpf/internal/permissions/permissions.go) as a reference implementation.

The operator does **not** assign the built-in `privileged` SCC. That SCC permits far more than OBI requires (unrestricted hostNetwork, any user, any volume type). A purpose-built SCC with only the required capabilities is the correct least-privilege approach.

The generated SCC reflects the declared `spec.mode`:

```go
scc := &osv1.SecurityContextConstraints{
    ObjectMeta: metav1.ObjectMeta{Name: cr.Name + "-scc"},
    AllowHostPID: true,
    AllowHostDirVolumePlugin: true,   // required for cgroup and kernel-security hostPath mounts
    AllowedCapabilities: modeCaps[spec.Mode],
    RunAsUser:   osv1.RunAsUserStrategyOptions{Type: osv1.RunAsUserStrategyRunAsAny},
    SELinuxContext: osv1.SELinuxContextStrategyOptions{Type: osv1.SELinuxStrategyRunAsAny},
    Users: []string{
        "system:serviceaccount:" + cr.Namespace + ":" + cr.Name + "-sa",
    },
}
```

The SCC binds to the agent `ServiceAccount` via the `Users` field — the OpenShift-native binding mechanism, distinct from Kubernetes RBAC. `spec.additionalCapabilities` are appended to `AllowedCapabilities` using the same logic as the vanilla Kubernetes path.

## 7. Multi-Tenant Governance Model

### 7.1 Overview

The operator implements a two-CRD hierarchical governance model that separates infrastructure ownership from application targeting:

| CRD | Scope | Owner | Responsibility |
|---|---|---|---|
| `ClusterEBPFAgent` | Cluster | Admin | DaemonSet lifecycle, Linux capabilities, node targeting, delegation policy |
| `OBIInstrumentation` | Namespace | Tenant | Pod annotation selectors for workloads in their own namespace |

Cluster administrators retain exclusive control over host-level privileges. Tenant project administrators get self-service instrumentation opt-in without any path to host access or cross-namespace targeting.

This follows the same hierarchical pattern established by the [NetObserv operator's `FlowCollector` / `FlowCollectorSlice`](https://docs.openshift.com/container-platform/latest/observability/network_observability/flowcollector-api.html) model.

### 7.2 Delegation Modes

The `ClusterEBPFAgent` controls whether `OBIInstrumentation` resources are honoured via `spec.tenantDelegation.mode`:

**`AllowList` (default):** Only namespaces explicitly listed in `spec.tenantDelegation.namespacesAllowList` may create `OBIInstrumentation` resources. Resources in unlisted namespaces are ignored by the controller. This is the recommended default — administrators explicitly onboard tenants.

**`AlwaysCollect`:** Any namespace may create an `OBIInstrumentation` resource and the controller will honour it. Suitable for clusters where all tenants are trusted or where broad observability coverage is desired.

```yaml
spec:
  tenantDelegation:
    mode: AllowList             # AllowList | AlwaysCollect
    namespacesAllowList:
      - tenant-alpha
      - tenant-beta
```

### 7.3 Compilation Loop

During each reconciliation pass the controller:

1. Reads `ClusterEBPFAgent.spec.config` — the global OBI configuration (export endpoints, log levels, network settings).
2. Lists all `OBIInstrumentation` resources across the cluster (or within allowed namespaces, depending on mode).
3. For each `OBIInstrumentation`, generates an instrument item and **injects `k8s_namespace`** set to the resource's own namespace:

```go
for _, instr := range obiInstrumentations {
    item := map[string]any{
        "k8s_namespace":      instr.Namespace,   // always injected by controller
        "k8s_pod_annotations": instr.Spec.PodAnnotations,
    }
    discoveryFilters = append(discoveryFilters, item)
}
```

4. Merges the global config with the compiled discovery filters into a single ConfigMap.
5. The ConfigMap is owned exclusively by the operator ServiceAccount. Tenants have no write path to it.

The result for a two-tenant cluster:

```yaml
discovery:
  instrument:
    - k8s_namespace: tenant-alpha       # injected; from OBIInstrumentation in tenant-alpha
      k8s_pod_annotations:
        obi.instrument: "true"
    - k8s_namespace: tenant-beta        # injected; from OBIInstrumentation in tenant-beta
      k8s_pod_annotations:
        obi.instrument: "true"
```

### 7.4 Namespace Isolation Guarantee

The namespace isolation guarantee is structural rather than policy-based. OBI's discovery filter engine ANDs all fields within a single instrument item — a process must match every field to be instrumented. Because the controller injects `k8s_namespace` into every item, a tenant's pod annotation selector can only ever match pods in their own namespace, regardless of how broad the selector is.

This was verified against the OBI source: `Matcher.matchByAttributes()` (`pkg/appolly/discover/matcher.go:301-333`) returns `false` on the first non-matching field before any eBPF attachment occurs. The namespace value is populated from `pod.Namespace` in the Kubernetes API — it is not user-controllable from within a pod.

> **Dependency:** The isolation guarantee relies on OBI correctly enforcing `k8s_namespace` filter semantics. Correctness of the underlying BPF filter logic remains the responsibility of the eBPF Instrumentation SIG, as defined in the shared responsibility model in §2.