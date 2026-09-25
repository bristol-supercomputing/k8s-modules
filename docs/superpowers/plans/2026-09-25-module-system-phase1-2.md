# Kubernetes Module System Phase 1 + 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the `Module` CRD, its resolving controller, the admission policies that inject modules into pods, a Helm chart, and a kind dev environment, all covered by unit, envtest and kind e2e tests.

**Architecture:** A pure-Go resolver (`internal/resolve`) turns a Module and the catalogue into a flattened `status.resolved`. A controller-runtime reconciler writes that status. Four static admission policies inside kube-apiserver (one MutatingAdmissionPolicy, four ValidatingAdmissionPolicies) read `status.resolved` as params and inject or check pods. No webhook runs.

**Tech Stack:** Go 1.26, k8s.io/* v0.36.5, controller-runtime v0.24.1, controller-tools v0.21.0 (controller-gen), setup-envtest v0.24.1 with kube-apiserver 1.36.2, Helm 4, kind 0.31 on podman, kindest/node v1.36.4.

**Spec:** `docs/superpowers/specs/2026-09-25-module-system-phase1-2-design.md`

## Global Constraints

- Go module path: `github.com/bristol-supercomputing/k8s-modules`.
- API group/version/kind: `modules.brics.ac.uk/v1alpha1`, `Module`, cluster-scoped.
- Label keys: `modules.brics.ac.uk/family`, `modules.brics.ac.uk/default` (value `"true"`).
- Pod annotation keys: `modules.brics.ac.uk/load`, `resolved.modules.brics.ac.uk/<family>`, `env.modules.brics.ac.uk/<NAME>`.
- Mount path is always `/opt/modules/<family>/<version>`.
- Rank: leaf 10; otherwise `10 × (1 + max(dep rank / 10))`.
- All admission policies use `failurePolicy: Fail` and ship in one Helm release.
- Helm defaults: `registryAllowlist: [registry.isambard/modules/]`, `envPrefixAllowlist: [NCCL_, FI_, CXIP_, UCX_, OMPI_]`, `modulesNamespace: module-system`.
- Only `spec.containers` are mutated (not init or ephemeral containers).
- Build with `CGO_ENABLED=0` (the Makefile exports it). This dev machine's cgo linker is broken, and the controller image is static anyway.
- `CONTAINER_CLI` defaults to docker, falling back to podman. `BUILD_ARGS` is passed through to image builds.

## Deviations from the spec found while planning

Verified in an envtest spike against kube-apiserver 1.36.2 source and behaviour:

1. **Two Module-guard policies, not one.** A policy with `paramKind` and `parameterNotFoundAction: Allow` skips *all* validations when no params exist. The registry and env-prefix checks would then not run on the very first Module. So `module-guards.modules.brics.ac.uk` has no params, and the one-default rule lives in `module-default.modules.brics.ac.uk` (param fan-out).
2. **Only the first denying policy's message reaches the user.** kube-apiserver collects every denial but returns only `deniedDecisions[0]`, and it iterates policies in map order. A pod that loads an unresolved module is denied by both `pods.*` ("was not loaded ... (kubectl get module X)") and `pod-consistency.*` (the Resolved condition message). Which message the user sees varies. Tests accept either message. The `pods.*` message already points at `kubectl get module`.
3. **Path-like CRD fields forbid `;` and `:`** (a character-class pattern) so that the `;`- and `:`-separated annotation value can't be broken. The spec asserts this guarantee without a rule that delivers it.
4. **Load entries** are validated as `^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`, at most 61 characters. Module names are DNS subdomains, so the version must in practice be lowercase and free of `_` even though the version pattern allows them.
5. **Pod policies skip `kube-system`** (namespaceSelector) and only fire on pods that carry module annotations (matchConditions), so a missing CRD can't block system pods.
6. **The controller also watches PVCs** in the modules namespace, so a Module waiting on a PVC resolves once the PVC appears.

## Review Focus

1. A plain pod with no module annotations must be admitted untouched, even while Modules exist. Test in Task 6 (`TestAdmissionPlainPodUntouched`).
2. A load list with whitespace, a trailing comma or a duplicate (`"a, a ,"`) must load `a` once. Test in Task 6 (`TestAdmissionLoadListWhitespaceAndDuplicates`).
3. A pod with several containers must get the mounts and env in every container. Test in Task 6 (`TestAdmissionSingleLoad` uses two containers).
4. A maximum-length Module name (40-char family + 20-char version) must produce a valid volume name that the API accepts. Test in Task 6 (`TestAdmissionLongModuleName`).
5. Updates by other controllers (labels, unrelated annotations, finalizers) on a module-loaded pod must still be allowed. Test in Task 6 (`TestAdmissionPodUpdate` checks a label and an unrelated annotation change).

---

## File map

| Path | Responsibility |
|---|---|
| `go.mod`, `go.sum` | module and dependency pins |
| `Makefile` | generate, manifests, build, test, test-envtest, test-e2e, image, kind-up/down |
| `Dockerfile` | static controller image |
| `api/v1alpha1/groupversion_info.go` | scheme registration |
| `api/v1alpha1/module_types.go` | Module types, constants, kubebuilder CEL markers |
| `api/v1alpha1/zz_generated.deepcopy.go` | generated |
| `internal/resolve/errors.go` | typed resolution errors |
| `internal/resolve/names.go` | `MountPath`, `VolumeName`, `AnnotationValue` |
| `internal/resolve/resolve.go` | `Resolve` |
| `internal/resolve/*_test.go` | table-driven unit tests |
| `internal/controller/module_controller.go` | reconciler, PVC check, status computation |
| `cmd/module-controller/main.go` | manager wiring and flags |
| `deploy/helm/k8s-modules/**` | chart: CRD (generated), controller, RBAC, 5 policies + bindings |
| `deploy/kind/kind-config.yaml`, `deploy/kind/setup.sh`, `deploy/kind/teardown.sh` | dev env |
| `test/images/nccl-fake/**` | stub module image |
| `test/envtest/*_test.go` | package `integration`: CRD, controller, admission tests against one envtest apiserver |
| `test/e2e/e2e_test.go` | build tag `e2e`: kind scenarios |

---

### Task 1: Scaffold, API types and generated CRD

**Files:**
- Create: `go.mod`, `Makefile`, `api/v1alpha1/groupversion_info.go`, `api/v1alpha1/module_types.go`
- Generate: `api/v1alpha1/zz_generated.deepcopy.go`, `deploy/helm/k8s-modules/crds/modules.brics.ac.uk_modules.yaml`

**Interfaces:**
- Produces: package `v1alpha1` with `Module`, `ModuleList`, `ModuleSpec`, `ModuleSource`, `ImageSource`, `HostSource`, `HostDiscover`, `PVCSource`, `ModuleEnv`, `PathList`, `ModuleAccess`, `ModuleStatus`, `Resolved`, `ResolvedVolume`, `ResolvedMount`, `ResolvedEnvVar`, `AddToScheme`, `GroupVersion`, plus constants `LabelFamily`, `LabelDefault`, `AnnotationLoad`, `AnnotationResolvedPrefix`, `AnnotationEnvPrefix`, `MountRoot`, `ConditionResolved`, `ConditionConflictFree`, `EnvLDLibraryPath`.

- [ ] **Step 1: go.mod**

```bash
go mod init github.com/bristol-supercomputing/k8s-modules
go mod edit -go=1.26
go get k8s.io/api@v0.36.5 k8s.io/apimachinery@v0.36.5 k8s.io/client-go@v0.36.5 sigs.k8s.io/controller-runtime@v0.24.1
```

- [ ] **Step 2: Makefile**

```make
CONTAINER_CLI ?= $(shell command -v docker >/dev/null 2>&1 && echo docker || echo podman)
BUILD_ARGS ?=
IMG ?= ghcr.io/bristol-supercomputing/module-controller:dev
ENVTEST_K8S_VERSION ?= 1.36.2
CONTROLLER_TOOLS_VERSION ?= v0.21.0
SETUP_ENVTEST_VERSION ?= v0.24.1
LOCALBIN ?= $(CURDIR)/bin
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
SETUP_ENVTEST ?= $(LOCALBIN)/setup-envtest
export CGO_ENABLED ?= 0

.PHONY: all generate manifests fmt vet build test test-envtest test-e2e helm-lint image kind-up kind-down

all: build

generate: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) object paths=./api/...

manifests: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) crd paths=./api/... output:crd:dir=deploy/helm/k8s-modules/crds

fmt:
	go fmt ./...

vet:
	go vet ./...
	go vet -tags e2e ./test/e2e/...

build: generate manifests fmt vet
	go build -o $(LOCALBIN)/module-controller ./cmd/module-controller

test:
	go test ./api/... ./internal/... -count=1

test-envtest: manifests $(SETUP_ENVTEST)
	KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" \
		go test ./test/envtest/... -count=1 -timeout 10m

test-e2e:
	go test -tags e2e ./test/e2e/... -count=1 -timeout 20m -v

helm-lint:
	helm lint deploy/helm/k8s-modules

image:
	$(CONTAINER_CLI) build $(BUILD_ARGS) -t $(IMG) .

kind-up:
	CONTAINER_CLI=$(CONTAINER_CLI) BUILD_ARGS="$(BUILD_ARGS)" deploy/kind/setup.sh

kind-down:
	CONTAINER_CLI=$(CONTAINER_CLI) deploy/kind/teardown.sh

$(CONTROLLER_GEN):
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

$(SETUP_ENVTEST):
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(SETUP_ENVTEST_VERSION)
```

(`cmd` and `test/e2e` don't exist yet, so run only `make generate manifests` until Task 4.)

- [ ] **Step 3: groupversion_info.go**

```go
// Package v1alpha1 contains the v1alpha1 Module API.
// +kubebuilder:object:generate=true
// +groupName=modules.brics.ac.uk
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	GroupVersion  = schema.GroupVersion{Group: "modules.brics.ac.uk", Version: "v1alpha1"}
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}
	AddToScheme   = SchemeBuilder.AddToScheme
)
```

- [ ] **Step 4: module_types.go**

```go
package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	LabelFamily  = "modules.brics.ac.uk/family"
	LabelDefault = "modules.brics.ac.uk/default"

	AnnotationLoad           = "modules.brics.ac.uk/load"
	AnnotationResolvedPrefix = "resolved.modules.brics.ac.uk/"
	AnnotationEnvPrefix      = "env.modules.brics.ac.uk/"

	MountRoot        = "/opt/modules"
	EnvLDLibraryPath = "LD_LIBRARY_PATH"

	ConditionResolved     = "Resolved"
	ConditionConflictFree = "ConflictFree"
)

// Module is a versioned bundle of shared libraries that pods can load.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Family",type=string,JSONPath=`.spec.family`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
// +kubebuilder:printcolumn:name="Resolved",type=string,JSONPath=`.status.conditions[?(@.type=="Resolved")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Resolved")].reason`
// +kubebuilder:validation:XValidation:rule="self.metadata.name == self.spec.family + '-' + self.spec.version",message="metadata.name must be <spec.family>-<spec.version>"
type Module struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModuleSpec   `json:"spec"`
	Status ModuleStatus `json:"status,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.dependsOn) || !((self.family + '-' + self.version) in self.dependsOn)",message="a module cannot depend on itself"
type ModuleSpec struct {
	// +kubebuilder:validation:MaxLength=40
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	Family string `json:"family"`
	// +kubebuilder:validation:MaxLength=20
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`
	Version string `json:"version"`

	Source ModuleSource `json:"source"`

	// DependsOn lists exact Module names.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`
	DependsOn []string `json:"dependsOn,omitempty"`

	// +optional
	Env *ModuleEnv `json:"env,omitempty"`

	// MaskPaths are absolute container paths, applied in phase 3.
	// +optional
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:items:Pattern=`^/[A-Za-z0-9._/+-]*$`
	MaskPaths []string `json:"maskPaths,omitempty"`

	// +optional
	Access *ModuleAccess `json:"access,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="[has(self.image), has(self.host), has(self.pvc)].filter(x, x).size() == 1",message="exactly one of image, host or pvc must be set"
type ModuleSource struct {
	// +optional
	Image *ImageSource `json:"image,omitempty"`
	// +optional
	Host *HostSource `json:"host,omitempty"`
	// +optional
	PVC *PVCSource `json:"pvc,omitempty"`
}

type ImageSource struct {
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^[^;:\s]+(:[0-9]+)?(/[^;:\s]+)*@sha256:[a-f0-9]{64}$`
	Reference string `json:"reference"`
}

type HostSource struct {
	Discover HostDiscover `json:"discover"`
}

type HostDiscover struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^lib[A-Za-z0-9_.+-]+\.so(\.[0-9]+)*$`
	Roots []string `json:"roots"`
}

type PVCSource struct {
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	ClaimName string `json:"claimName"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._+-][A-Za-z0-9._/+-]*$`
	// +kubebuilder:validation:XValidation:rule="!self.split('/').exists(s, s == '..')",message="subPath must not contain '..' segments"
	SubPath string `json:"subPath"`
}

type ModuleEnv struct {
	// PrependPath maps LD_LIBRARY_PATH to paths relative to the mount path.
	// +optional
	// +kubebuilder:validation:MaxProperties=1
	// +kubebuilder:validation:XValidation:rule="self.all(k, k == 'LD_LIBRARY_PATH')",message="only LD_LIBRARY_PATH may be prepended"
	// +kubebuilder:validation:XValidation:rule="self.all(k, self[k].all(p, !p.split('/').exists(s, s == '..')))",message="prependPath entries must not contain '..' segments"
	PrependPath map[string]PathList `json:"prependPath,omitempty"`

	// +optional
	// +kubebuilder:validation:MaxProperties=32
	// +kubebuilder:validation:XValidation:rule="self.all(k, k.size() <= 63 && k.matches('^[A-Za-z][A-Za-z0-9_]*[A-Za-z0-9]$'))",message="env.set names must match ^[A-Za-z][A-Za-z0-9_]*[A-Za-z0-9]$ and be at most 63 characters"
	// +kubebuilder:validation:XValidation:rule="self.all(k, !(k in ['LD_PRELOAD', 'LD_LIBRARY_PATH', 'LD_AUDIT', 'PATH']))",message="env.set may not set LD_PRELOAD, LD_LIBRARY_PATH, LD_AUDIT or PATH"
	Set map[string]string `json:"set,omitempty"`
}

// PathList is a list of relative paths.
// +kubebuilder:validation:MaxItems=16
// +kubebuilder:validation:items:MaxLength=256
// +kubebuilder:validation:items:Pattern=`^[A-Za-z0-9._+-][A-Za-z0-9._/+-]*$`
type PathList []string

type ModuleAccess struct {
	// MatchLabels are namespace labels; empty means every namespace.
	// +optional
	// +kubebuilder:validation:MaxProperties=16
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
}

type ModuleStatus struct {
	// +optional
	Resolved *Resolved `json:"resolved,omitempty"`
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Resolved is the flattened closure of a Module.
type Resolved struct {
	Modules []string `json:"modules"`
	// +optional
	Volumes []ResolvedVolume `json:"volumes,omitempty"`
	// +optional
	Mounts []ResolvedMount `json:"mounts,omitempty"`
	// +optional
	Env         []ResolvedEnvVar  `json:"env,omitempty"`
	Annotations map[string]string `json:"annotations"`
}

type ResolvedVolume struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
}

type ResolvedMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
}

type ResolvedEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// +kubebuilder:object:root=true
type ModuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Module `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Module{}, &ModuleList{})
}
```

- [ ] **Step 5: Generate and compile**

Run: `make generate manifests && go build ./...`
Expected: `api/v1alpha1/zz_generated.deepcopy.go` and `deploy/helm/k8s-modules/crds/modules.brics.ac.uk_modules.yaml` exist. The CRD YAML contains `x-kubernetes-validations` entries for the name, spec-immutable, source and env rules. The build succeeds.

CRD CEL rules are exercised in Task 5 against a real apiserver, which also rejects any rule whose estimated cost is too high.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum Makefile api deploy/helm/k8s-modules/crds
git commit -m "feat(api): Module v1alpha1 types and generated CRD"
```

---

### Task 2: Resolver naming helpers

**Files:**
- Create: `internal/resolve/names.go`, `internal/resolve/names_test.go`

**Interfaces:**
- Consumes: `v1alpha1.Module`
- Produces:
  - `func MountPath(m *v1alpha1.Module) string`
  - `func VolumeName(moduleName string) string`
  - `func AnnotationValue(m *v1alpha1.Module, rank int) string`
  - `func ResolvedAnnotationKey(family string) string`
  - `func EnvAnnotationKey(name string) string`

- [ ] **Step 1: Failing tests**

```go
package resolve

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func mod(family, version string, src v1.ModuleSource, mutate ...func(*v1.Module)) *v1.Module {
	m := &v1.Module{
		ObjectMeta: metav1.ObjectMeta{Name: family + "-" + version},
		Spec:       v1.ModuleSpec{Family: family, Version: version, Source: src},
	}
	for _, f := range mutate {
		f(m)
	}
	return m
}

func image() v1.ModuleSource {
	return v1.ModuleSource{Image: &v1.ImageSource{Reference: "registry.isambard/modules/x@sha256:" + strings.Repeat("a", 64)}}
}

func host() v1.ModuleSource {
	return v1.ModuleSource{Host: &v1.HostSource{Discover: v1.HostDiscover{Roots: []string{"libfabric.so.1"}}}}
}

func pvc() v1.ModuleSource {
	return v1.ModuleSource{PVC: &v1.PVCSource{ClaimName: "spack-apps", SubPath: "nccl/2.29.2"}}
}

func withLib(paths ...string) func(*v1.Module) {
	return func(m *v1.Module) {
		if m.Spec.Env == nil {
			m.Spec.Env = &v1.ModuleEnv{}
		}
		m.Spec.Env.PrependPath = map[string]v1.PathList{v1.EnvLDLibraryPath: paths}
	}
}

func TestVolumeName(t *testing.T) {
	long := strings.Repeat("f", 40) + "-" + strings.Repeat("9", 20)
	cases := []struct{ in, want string }{
		{"nccl-2.29.2", "mod-nccl-2-29-2"},
		{"libfabric-host", "mod-libfabric-host"},
		{"a-1.0_rc", "mod-a-1-0-rc"},
	}
	for _, c := range cases {
		if got := VolumeName(c.in); got != c.want {
			t.Errorf("VolumeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	got := VolumeName(long)
	if len(got) != 63 {
		t.Fatalf("long name length = %d, want 63 (%q)", len(got), got)
	}
	if !strings.HasPrefix(got, ("mod-" + long)[:54]+"-") {
		t.Errorf("long name %q does not keep the 54-char prefix", got)
	}
	if errs := validation.IsDNS1123Label(got); len(errs) > 0 {
		t.Errorf("long name %q is not a DNS label: %v", got, errs)
	}
	if VolumeName(long) != got || VolumeName(long+"x") == got {
		t.Errorf("hash suffix must be deterministic and name-dependent")
	}
}

func TestAnnotationValue(t *testing.T) {
	cases := []struct {
		name string
		m    *v1.Module
		rank int
		want string
	}{
		{"image with lib", mod("nccl", "2.29.2", image(), withLib("lib")), 10,
			"nccl-2.29.2;rank=10;src=image;mount=/opt/modules/nccl/2.29.2;lib=/opt/modules/nccl/2.29.2/lib"},
		{"image without lib", mod("tools", "1", image()), 20,
			"tools-1;rank=20;src=image;mount=/opt/modules/tools/1"},
		{"image two libs in spec order", mod("x", "1", image(), withLib("lib64", "lib")), 10,
			"x-1;rank=10;src=image;mount=/opt/modules/x/1;lib=/opt/modules/x/1/lib64:/opt/modules/x/1/lib"},
		{"host ignores prependPath", mod("libfabric", "host", host(), withLib("other")), 10,
			"libfabric-host;rank=10;src=host;mount=/opt/modules/libfabric/host;lib=/opt/modules/libfabric/host/lib"},
		{"pvc", mod("nccl", "2.29.2", pvc(), withLib("lib")), 10,
			"nccl-2.29.2;rank=10;src=pvc:spack-apps/nccl/2.29.2;mount=/opt/modules/nccl/2.29.2;lib=/opt/modules/nccl/2.29.2/lib"},
		{"mask", mod("x", "1", image(), func(m *v1.Module) { m.Spec.MaskPaths = []string{"/usr/lib/a", "/usr/lib/b"} }), 10,
			"x-1;rank=10;src=image;mount=/opt/modules/x/1;mask=/usr/lib/a:/usr/lib/b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AnnotationValue(c.m, c.rank); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestAnnotationKeys(t *testing.T) {
	if got := ResolvedAnnotationKey("nccl"); got != "resolved.modules.brics.ac.uk/nccl" {
		t.Errorf("got %q", got)
	}
	if got := EnvAnnotationKey("NCCL_NET"); got != "env.modules.brics.ac.uk/NCCL_NET" {
		t.Errorf("got %q", got)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./internal/resolve/` fails with undefined `VolumeName`, `AnnotationValue` and the other helpers.

- [ ] **Step 3: Implement names.go**

```go
// Package resolve computes the flattened closure of a Module. It is pure and
// has no Kubernetes client.
package resolve

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strconv"
	"strings"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

// MountPath is where a module is mounted in every container.
func MountPath(m *v1.Module) string {
	return v1.MountRoot + "/" + m.Spec.Family + "/" + m.Spec.Version
}

// VolumeName turns a Module name into a pod volume name (a DNS label).
func VolumeName(moduleName string) string {
	n := "mod-" + strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, strings.ToLower(moduleName))
	if len(n) <= 63 {
		return n
	}
	sum := sha256.Sum256([]byte(moduleName))
	return n[:54] + "-" + hex.EncodeToString(sum[:])[:8]
}

func ResolvedAnnotationKey(family string) string { return v1.AnnotationResolvedPrefix + family }

func EnvAnnotationKey(name string) string { return v1.AnnotationEnvPrefix + name }

// AnnotationValue is the canonical resolved.modules.brics.ac.uk/<family> value.
func AnnotationValue(m *v1.Module, rank int) string {
	mount := MountPath(m)
	parts := []string{m.Name, "rank=" + strconv.Itoa(rank)}
	src := m.Spec.Source
	switch {
	case src.Image != nil:
		parts = append(parts, "src=image")
	case src.Host != nil:
		parts = append(parts, "src=host")
	case src.PVC != nil:
		parts = append(parts, "src=pvc:"+src.PVC.ClaimName+"/"+src.PVC.SubPath)
	}
	parts = append(parts, "mount="+mount)

	var libs []string
	if src.Host != nil {
		libs = []string{mount + "/lib"}
	} else if m.Spec.Env != nil {
		for _, p := range m.Spec.Env.PrependPath[v1.EnvLDLibraryPath] {
			libs = append(libs, path.Join(mount, p))
		}
	}
	if len(libs) > 0 {
		parts = append(parts, "lib="+strings.Join(libs, ":"))
	}
	if len(m.Spec.MaskPaths) > 0 {
		parts = append(parts, "mask="+strings.Join(m.Spec.MaskPaths, ":"))
	}
	return strings.Join(parts, ";")
}
```

- [ ] **Step 4: Run, expect PASS** — `go test ./internal/resolve/ -run 'TestVolumeName|TestAnnotation' -v`

- [ ] **Step 5: Commit** — `git add internal/resolve && git commit -m "feat(resolve): mount path, volume name and annotation value helpers"`

---

### Task 3: `Resolve`

**Files:**
- Create: `internal/resolve/errors.go`, `internal/resolve/resolve.go`, `internal/resolve/resolve_test.go`

**Interfaces:**
- Consumes: Task 2 helpers.
- Produces:
  - `func Resolve(name string, catalogue map[string]*v1alpha1.Module) (*v1alpha1.Resolved, error)`
  - value-type errors: `ErrDependencyNotFound{Module, Missing string}`, `ErrDependencyCycle{Path []string}`, `ErrFamilyConflict{Family string; Modules []string}`, `ErrEnvConflict{Name string; Modules, Values []string}` (Modules sorted by name, and Values[i] is the value Modules[i] sets). Match them with `errors.As(err, &resolve.ErrFamilyConflict{})`.

- [ ] **Step 1: Failing tests** (`resolve_test.go`, reusing `mod`, `image`, `host` and `withLib` from `names_test.go`)

```go
package resolve

import (
	"errors"
	"reflect"
	"testing"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

func deps(d ...string) func(*v1.Module) { return func(m *v1.Module) { m.Spec.DependsOn = d } }

func env(kv ...string) func(*v1.Module) {
	return func(m *v1.Module) {
		if m.Spec.Env == nil {
			m.Spec.Env = &v1.ModuleEnv{}
		}
		m.Spec.Env.Set = map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			m.Spec.Env.Set[kv[i]] = kv[i+1]
		}
	}
}

func cat(ms ...*v1.Module) map[string]*v1.Module {
	c := map[string]*v1.Module{}
	for _, m := range ms {
		c[m.Name] = m
	}
	return c
}

func TestResolveSingle(t *testing.T) {
	m := mod("nccl", "2.29.2", image(), withLib("lib"), env("NCCL_NET", "AWS Libfabric"))
	got, err := Resolve("nccl-2.29.2", cat(m))
	if err != nil {
		t.Fatal(err)
	}
	want := &v1.Resolved{
		Modules: []string{"nccl-2.29.2"},
		Volumes: []v1.ResolvedVolume{{Name: "mod-nccl-2-29-2", Reference: m.Spec.Source.Image.Reference}},
		Mounts:  []v1.ResolvedMount{{Name: "mod-nccl-2-29-2", MountPath: "/opt/modules/nccl/2.29.2"}},
		Env:     []v1.ResolvedEnvVar{{Name: "NCCL_NET", Value: "AWS Libfabric"}},
		Annotations: map[string]string{
			"resolved.modules.brics.ac.uk/nccl": "nccl-2.29.2;rank=10;src=image;mount=/opt/modules/nccl/2.29.2;lib=/opt/modules/nccl/2.29.2/lib",
			"env.modules.brics.ac.uk/NCCL_NET":  "AWS Libfabric",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestResolveChainAndRanks(t *testing.T) {
	c := cat(
		mod("aws-ofi-nccl", "1.18.0", image(), deps("nccl-2.29.2", "libfabric-host")),
		mod("nccl", "2.29.2", image()),
		mod("libfabric", "host", host()),
		mod("app", "1", image(), deps("aws-ofi-nccl-1.18.0")),
	)
	got, err := Resolve("app-1", c)
	if err != nil {
		t.Fatal(err)
	}
	wantMods := []string{"libfabric-host", "nccl-2.29.2", "aws-ofi-nccl-1.18.0", "app-1"}
	if !reflect.DeepEqual(got.Modules, wantMods) {
		t.Errorf("modules = %v, want %v", got.Modules, wantMods)
	}
	wantRank := map[string]string{"nccl": "rank=10", "libfabric": "rank=10", "aws-ofi-nccl": "rank=20", "app": "rank=30"}
	for fam, r := range wantRank {
		if v := got.Annotations[ResolvedAnnotationKey(fam)]; !containsField(v, r) {
			t.Errorf("%s annotation %q lacks %s", fam, v, r)
		}
	}
	// Host modules get no image volume.
	if len(got.Volumes) != 3 || len(got.Mounts) != 3 {
		t.Errorf("volumes=%v mounts=%v, want 3 each", got.Volumes, got.Mounts)
	}
}

func containsField(v, f string) bool {
	for _, p := range splitSemi(v) {
		if p == f {
			return true
		}
	}
	return false
}

func splitSemi(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func TestResolveRankUsesMaxDependency(t *testing.T) {
	// top -> {mid -> leaf, leaf}: mid=20, top=30 (not 20).
	c := cat(mod("leaf", "1", image()), mod("mid", "1", image(), deps("leaf-1")), mod("top", "1", image(), deps("leaf-1", "mid-1")))
	got, err := Resolve("top-1", c)
	if err != nil {
		t.Fatal(err)
	}
	if !containsField(got.Annotations[ResolvedAnnotationKey("top")], "rank=30") {
		t.Errorf("top annotation = %q", got.Annotations[ResolvedAnnotationKey("top")])
	}
}

func TestResolveDiamond(t *testing.T) {
	c := cat(
		mod("a", "1", image(), deps("b-1", "c-1")),
		mod("b", "1", image(), deps("d-1")),
		mod("c", "1", image(), deps("d-1")),
		mod("d", "1", image()),
	)
	got, err := Resolve("a-1", c)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"d-1", "b-1", "c-1", "a-1"}; !reflect.DeepEqual(got.Modules, want) {
		t.Errorf("modules = %v, want %v", got.Modules, want)
	}
	if len(got.Volumes) != 4 {
		t.Errorf("shared dependency must appear once: %v", got.Volumes)
	}
}

func TestResolveErrors(t *testing.T) {
	cases := []struct {
		name string
		c    map[string]*v1.Module
		root string
		want error
	}{
		{"missing dependency", cat(mod("a", "1", image(), deps("b-1"))), "a-1",
			ErrDependencyNotFound{Module: "a-1", Missing: "b-1"}},
		{"missing root", cat(), "a-1", ErrDependencyNotFound{Module: "a-1", Missing: "a-1"}},
		{"self cycle", cat(mod("a", "1", image(), deps("a-1"))), "a-1",
			ErrDependencyCycle{Path: []string{"a-1", "a-1"}}},
		{"indirect cycle", cat(mod("a", "1", image(), deps("b-1")), mod("b", "1", image(), deps("a-1"))), "a-1",
			ErrDependencyCycle{Path: []string{"a-1", "b-1", "a-1"}}},
		{"cycle below root", cat(mod("r", "1", image(), deps("a-1")), mod("a", "1", image(), deps("b-1")), mod("b", "1", image(), deps("a-1"))), "r-1",
			ErrDependencyCycle{Path: []string{"a-1", "b-1", "a-1"}}},
		{"family conflict", cat(mod("x", "1", image(), deps("nccl-1", "nccl-2")), mod("nccl", "1", image()), mod("nccl", "2", image())), "x-1",
			ErrFamilyConflict{Family: "nccl", Modules: []string{"nccl-1", "nccl-2"}}},
		{"env conflict", cat(mod("x", "1", image(), deps("p-1", "q-1")), mod("p", "1", image(), env("NCCL_ALGO", "ring")), mod("q", "1", image(), env("NCCL_ALGO", "tree"))), "x-1",
			ErrEnvConflict{Name: "NCCL_ALGO", Modules: []string{"p-1", "q-1"}, Values: []string{"ring", "tree"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Resolve(c.root, c.c)
			if !reflect.DeepEqual(err, c.want) {
				t.Errorf("err = %#v\nwant  %#v", err, c.want)
			}
			if err != nil && err.Error() == "" {
				t.Error("empty error message")
			}
		})
	}
}

func TestResolveEnvAgreement(t *testing.T) {
	c := cat(mod("x", "1", image(), deps("p-1", "q-1")), mod("p", "1", image(), env("NCCL_ALGO", "ring")), mod("q", "1", image(), env("NCCL_ALGO", "ring", "FI_PROVIDER", "cxi")))
	got, err := Resolve("x-1", c)
	if err != nil {
		t.Fatal(err)
	}
	want := []v1.ResolvedEnvVar{{Name: "FI_PROVIDER", Value: "cxi"}, {Name: "NCCL_ALGO", Value: "ring"}}
	if !reflect.DeepEqual(got.Env, want) {
		t.Errorf("env = %v, want %v", got.Env, want)
	}
}

func TestResolveErrorsAreMatchable(t *testing.T) {
	_, err := Resolve("a-1", cat(mod("a", "1", image(), deps("b-1"))))
	var nf ErrDependencyNotFound
	if !errors.As(err, &nf) || nf.Missing != "b-1" {
		t.Errorf("errors.As failed: %v", err)
	}
}

func TestResolveDeterministic(t *testing.T) {
	c := cat(mod("a", "1", image(), deps("c-1", "b-1")), mod("b", "1", image()), mod("c", "1", image()))
	first, _ := Resolve("a-1", c)
	for i := 0; i < 20; i++ {
		again, _ := Resolve("a-1", c)
		if !reflect.DeepEqual(first, again) {
			t.Fatal("Resolve is not deterministic")
		}
	}
	if want := []string{"b-1", "c-1", "a-1"}; !reflect.DeepEqual(first.Modules, want) {
		t.Errorf("modules = %v, want %v", first.Modules, want)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./internal/resolve/` fails with undefined `Resolve`.

- [ ] **Step 3: errors.go**

```go
package resolve

import (
	"fmt"
	"strings"
)

type ErrDependencyNotFound struct{ Module, Missing string }

func (e ErrDependencyNotFound) Error() string {
	if e.Module == e.Missing {
		return fmt.Sprintf("module %q not found", e.Module)
	}
	return fmt.Sprintf("dependency %q of module %q not found", e.Missing, e.Module)
}

type ErrDependencyCycle struct{ Path []string }

func (e ErrDependencyCycle) Error() string {
	return "dependency cycle: " + strings.Join(e.Path, " -> ")
}

type ErrFamilyConflict struct {
	Family  string
	Modules []string
}

func (e ErrFamilyConflict) Error() string {
	return fmt.Sprintf("modules %s belong to the same family %q", quoted(e.Modules), e.Family)
}

type ErrEnvConflict struct {
	Name    string
	Modules []string
	Values  []string
}

func (e ErrEnvConflict) Error() string {
	return fmt.Sprintf("modules %s set %s to different values %s", quoted(e.Modules), e.Name, quoted(e.Values))
}

func quoted(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}
```

- [ ] **Step 4: resolve.go**

```go
package resolve

import (
	"sort"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

// Resolve flattens the dependency closure of the named Module.
func Resolve(name string, catalogue map[string]*v1.Module) (*v1.Resolved, error) {
	if _, ok := catalogue[name]; !ok {
		return nil, ErrDependencyNotFound{Module: name, Missing: name}
	}
	w := &walker{catalogue: catalogue, rank: map[string]int{}, onPath: map[string]bool{}}
	if _, err := w.visit(name); err != nil {
		return nil, err
	}

	members := make([]string, 0, len(w.rank))
	for n := range w.rank {
		members = append(members, n)
	}
	sort.Slice(members, func(i, j int) bool {
		a, b := members[i], members[j]
		if w.rank[a] != w.rank[b] {
			return w.rank[a] < w.rank[b]
		}
		return a < b
	})

	if err := familyConflict(members, catalogue); err != nil {
		return nil, err
	}
	env, err := mergeEnv(members, catalogue)
	if err != nil {
		return nil, err
	}

	out := &v1.Resolved{Modules: members, Env: env, Annotations: map[string]string{}}
	for _, n := range members {
		m := catalogue[n]
		out.Annotations[ResolvedAnnotationKey(m.Spec.Family)] = AnnotationValue(m, w.rank[n])
		if img := m.Spec.Source.Image; img != nil {
			vn := VolumeName(n)
			out.Volumes = append(out.Volumes, v1.ResolvedVolume{Name: vn, Reference: img.Reference})
			out.Mounts = append(out.Mounts, v1.ResolvedMount{Name: vn, MountPath: MountPath(m)})
		}
	}
	for _, e := range env {
		out.Annotations[EnvAnnotationKey(e.Name)] = e.Value
	}
	return out, nil
}

type walker struct {
	catalogue map[string]*v1.Module
	rank      map[string]int
	path      []string
	onPath    map[string]bool
}

func (w *walker) visit(name string) (int, error) {
	if r, ok := w.rank[name]; ok {
		return r, nil
	}
	if w.onPath[name] {
		for i, p := range w.path {
			if p == name {
				cycle := append(append([]string{}, w.path[i:]...), name)
				return 0, ErrDependencyCycle{Path: cycle}
			}
		}
	}
	w.onPath[name] = true
	w.path = append(w.path, name)
	maxDep := 0
	for _, dep := range w.catalogue[name].Spec.DependsOn {
		if _, ok := w.catalogue[dep]; !ok {
			return 0, ErrDependencyNotFound{Module: name, Missing: dep}
		}
		r, err := w.visit(dep)
		if err != nil {
			return 0, err
		}
		maxDep = max(maxDep, r)
	}
	w.path = w.path[:len(w.path)-1]
	delete(w.onPath, name)
	rank := 10 * (1 + maxDep/10)
	w.rank[name] = rank
	return rank, nil
}

func familyConflict(members []string, catalogue map[string]*v1.Module) error {
	byFamily := map[string][]string{}
	for _, n := range members {
		f := catalogue[n].Spec.Family
		byFamily[f] = append(byFamily[f], n)
	}
	families := make([]string, 0, len(byFamily))
	for f := range byFamily {
		families = append(families, f)
	}
	sort.Strings(families)
	for _, f := range families {
		if ms := byFamily[f]; len(ms) > 1 {
			sort.Strings(ms)
			return ErrFamilyConflict{Family: f, Modules: ms}
		}
	}
	return nil
}

func mergeEnv(members []string, catalogue map[string]*v1.Module) ([]v1.ResolvedEnvVar, error) {
	type setter struct{ module, value string }
	byName := map[string][]setter{}
	for _, n := range members {
		if e := catalogue[n].Spec.Env; e != nil {
			for k, v := range e.Set {
				byName[k] = append(byName[k], setter{n, v})
			}
		}
	}
	names := make([]string, 0, len(byName))
	for k := range byName {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]v1.ResolvedEnvVar, 0, len(names))
	for _, k := range names {
		ss := byName[k]
		sort.Slice(ss, func(i, j int) bool { return ss[i].module < ss[j].module })
		for _, s := range ss[1:] {
			if s.value != ss[0].value {
				e := ErrEnvConflict{Name: k}
				for _, s := range ss {
					e.Modules = append(e.Modules, s.module)
					e.Values = append(e.Values, s.value)
				}
				return nil, e
			}
		}
		out = append(out, v1.ResolvedEnvVar{Name: k, Value: ss[0].value})
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
```

- [ ] **Step 5: Run, expect PASS** — `go test ./internal/resolve/ -v -count=1`

- [ ] **Step 6: Commit** — `git add internal/resolve && git commit -m "feat(resolve): dependency closure, ranks and conflict detection"`

---

### Task 4: Module controller and binary

**Files:**
- Create: `internal/controller/module_controller.go`, `internal/controller/status_test.go`, `cmd/module-controller/main.go`, `Dockerfile`, `.dockerignore`

**Interfaces:**
- Consumes: `resolve.Resolve` and the resolve error types.
- Produces:
  - `type ModuleReconciler struct { client.Client; ModulesNamespace string }`
  - `func (r *ModuleReconciler) SetupWithManager(mgr ctrl.Manager) error`
  - `func CacheOptions(modulesNamespace string) cache.Options` (restricts the PVC informer to the modules namespace; used by main and envtest)
  - `type ErrPVCInvalid struct{ Module, Namespace, Claim, Problem string }`
  - condition reasons: `Resolved`, `NoConflicts`, `DependencyNotFound`, `DependencyCycle`, `Conflict`, `FamilyConflict`, `EnvConflict`, `PVCInvalid`, `NotEvaluated`.

- [ ] **Step 1: Failing unit test for status computation** (`status_test.go`)

```go
package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	"github.com/bristol-supercomputing/k8s-modules/internal/resolve"
)

func TestSetStatus(t *testing.T) {
	cases := []struct {
		name                         string
		err                          error
		resolved, conflict           metav1.ConditionStatus
		resolvedReason, conflictReason string
	}{
		{"ok", nil, metav1.ConditionTrue, metav1.ConditionTrue, "Resolved", "NoConflicts"},
		{"missing", resolve.ErrDependencyNotFound{Module: "a", Missing: "b"}, metav1.ConditionFalse, metav1.ConditionUnknown, "DependencyNotFound", "NotEvaluated"},
		{"cycle", resolve.ErrDependencyCycle{Path: []string{"a", "a"}}, metav1.ConditionFalse, metav1.ConditionUnknown, "DependencyCycle", "NotEvaluated"},
		{"family", resolve.ErrFamilyConflict{Family: "f", Modules: []string{"f-1", "f-2"}}, metav1.ConditionFalse, metav1.ConditionFalse, "Conflict", "FamilyConflict"},
		{"env", resolve.ErrEnvConflict{Name: "N", Modules: []string{"a", "b"}, Values: []string{"1", "2"}}, metav1.ConditionFalse, metav1.ConditionFalse, "Conflict", "EnvConflict"},
		{"pvc", ErrPVCInvalid{Module: "a", Namespace: "ns", Claim: "c", Problem: "does not exist"}, metav1.ConditionFalse, metav1.ConditionUnknown, "PVCInvalid", "NotEvaluated"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := v1.ModuleStatus{Resolved: &v1.Resolved{Modules: []string{"stale"}}}
			var res *v1.Resolved
			if c.err == nil {
				res = &v1.Resolved{Modules: []string{"a"}}
			}
			setStatus(&st, 7, res, c.err)
			r := meta.FindStatusCondition(st.Conditions, v1.ConditionResolved)
			cf := meta.FindStatusCondition(st.Conditions, v1.ConditionConflictFree)
			if r.Status != c.resolved || r.Reason != c.resolvedReason || cf.Status != c.conflict || cf.Reason != c.conflictReason {
				t.Errorf("got Resolved=%s/%s ConflictFree=%s/%s", r.Status, r.Reason, cf.Status, cf.Reason)
			}
			if r.ObservedGeneration != 7 || cf.ObservedGeneration != 7 {
				t.Error("observedGeneration not set")
			}
			if c.err != nil && (st.Resolved != nil || r.Message != c.err.Error()) {
				t.Errorf("failure must clear resolved and carry the error message; got %+v %q", st.Resolved, r.Message)
			}
			if c.err == nil && st.Resolved != res {
				t.Error("success must set resolved")
			}
		})
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./internal/controller/` fails with undefined `setStatus`.

- [ ] **Step 3: module_controller.go**

```go
// Package controller reconciles Modules into status.resolved.
package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	"github.com/bristol-supercomputing/k8s-modules/internal/resolve"
)

// ErrPVCInvalid reports a pvc-sourced module whose claim is unusable.
type ErrPVCInvalid struct{ Module, Namespace, Claim, Problem string }

func (e ErrPVCInvalid) Error() string {
	return fmt.Sprintf("module %q needs PVC \"%s/%s\", which %s", e.Module, e.Namespace, e.Claim, e.Problem)
}

type ModuleReconciler struct {
	client.Client
	ModulesNamespace string
}

// CacheOptions limits the PVC informer to the modules namespace, which is
// all the controller's Role allows.
func CacheOptions(modulesNamespace string) cache.Options {
	return cache.Options{ByObject: map[client.Object]cache.ByObject{
		&corev1.PersistentVolumeClaim{}: {Namespaces: map[string]cache.Config{modulesNamespace: {}}},
	}}
}

func (r *ModuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("module").
		// Any create, delete or spec change can alter any closure; status
		// writes don't bump generation and are filtered out.
		Watches(&v1.Module{}, handler.EnqueueRequestsFromMapFunc(r.enqueueAll),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&corev1.PersistentVolumeClaim{}, handler.EnqueueRequestsFromMapFunc(r.enqueueAll)).
		Complete(r)
}

func (r *ModuleReconciler) enqueueAll(ctx context.Context, _ client.Object) []reconcile.Request {
	var list v1.ModuleList
	if err := r.List(ctx, &list); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "listing modules")
		return nil
	}
	reqs := make([]reconcile.Request, len(list.Items))
	for i := range list.Items {
		reqs[i] = reconcile.Request{NamespacedName: types.NamespacedName{Name: list.Items[i].Name}}
	}
	return reqs
}

func (r *ModuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var list v1.ModuleList
	if err := r.List(ctx, &list); err != nil {
		return ctrl.Result{}, err
	}
	catalogue := make(map[string]*v1.Module, len(list.Items))
	for i := range list.Items {
		catalogue[list.Items[i].Name] = &list.Items[i]
	}
	m, ok := catalogue[req.Name]
	if !ok {
		return ctrl.Result{}, nil
	}

	resolved, err := resolve.Resolve(m.Name, catalogue)
	if err == nil {
		err = r.checkPVCs(ctx, resolved, catalogue)
		var invalid ErrPVCInvalid
		if err != nil && !errors.As(err, &invalid) {
			return ctrl.Result{}, err
		}
	}

	desired := m.DeepCopy()
	setStatus(&desired.Status, m.Generation, resolved, err)
	if equality.Semantic.DeepEqual(m.Status, desired.Status) {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, r.Status().Patch(ctx, desired, client.MergeFrom(m))
}

func (r *ModuleReconciler) checkPVCs(ctx context.Context, resolved *v1.Resolved, catalogue map[string]*v1.Module) error {
	for _, name := range resolved.Modules {
		src := catalogue[name].Spec.Source.PVC
		if src == nil {
			continue
		}
		var pvc corev1.PersistentVolumeClaim
		err := r.Get(ctx, types.NamespacedName{Namespace: r.ModulesNamespace, Name: src.ClaimName}, &pvc)
		if apierrors.IsNotFound(err) {
			return ErrPVCInvalid{Module: name, Namespace: r.ModulesNamespace, Claim: src.ClaimName, Problem: "does not exist"}
		}
		if err != nil {
			return err
		}
		if !slices.Contains(pvc.Spec.AccessModes, corev1.ReadOnlyMany) {
			return ErrPVCInvalid{Module: name, Namespace: r.ModulesNamespace, Claim: src.ClaimName, Problem: "does not have the ReadOnlyMany access mode"}
		}
	}
	return nil
}

func setStatus(st *v1.ModuleStatus, gen int64, resolved *v1.Resolved, err error) {
	res := metav1.Condition{Type: v1.ConditionResolved, ObservedGeneration: gen}
	cf := metav1.Condition{Type: v1.ConditionConflictFree, ObservedGeneration: gen,
		Status: metav1.ConditionUnknown, Reason: "NotEvaluated",
		Message: "conflicts are checked once dependencies resolve"}

	switch {
	case err == nil:
		res.Status, res.Reason = metav1.ConditionTrue, "Resolved"
		res.Message = fmt.Sprintf("resolved %d modules", len(resolved.Modules))
		cf.Status, cf.Reason, cf.Message = metav1.ConditionTrue, "NoConflicts", "no family or env conflicts"
	case errors.As(err, new(resolve.ErrDependencyNotFound)):
		res.Status, res.Reason = metav1.ConditionFalse, "DependencyNotFound"
	case errors.As(err, new(resolve.ErrDependencyCycle)):
		res.Status, res.Reason = metav1.ConditionFalse, "DependencyCycle"
	case errors.As(err, new(resolve.ErrFamilyConflict)):
		res.Status, res.Reason = metav1.ConditionFalse, "Conflict"
		cf.Status, cf.Reason, cf.Message = metav1.ConditionFalse, "FamilyConflict", err.Error()
	case errors.As(err, new(resolve.ErrEnvConflict)):
		res.Status, res.Reason = metav1.ConditionFalse, "Conflict"
		cf.Status, cf.Reason, cf.Message = metav1.ConditionFalse, "EnvConflict", err.Error()
	case errors.As(err, new(ErrPVCInvalid)):
		res.Status, res.Reason = metav1.ConditionFalse, "PVCInvalid"
	default:
		res.Status, res.Reason = metav1.ConditionFalse, "Error"
	}
	if err != nil {
		res.Message = err.Error()
		resolved = nil
	}
	st.Resolved = resolved
	meta.SetStatusCondition(&st.Conditions, res)
	meta.SetStatusCondition(&st.Conditions, cf)
}
```

- [ ] **Step 4: Run, expect PASS** — `go test ./internal/controller/ -v -count=1`

- [ ] **Step 5: main.go**

```go
package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	"github.com/bristol-supercomputing/k8s-modules/internal/controller"
)

func main() {
	var modulesNamespace, metricsAddr, probeAddr string
	var leaderElect bool
	flag.StringVar(&modulesNamespace, "modules-namespace", "module-system", "Namespace holding PVCs for pvc-sourced modules.")
	flag.BoolVar(&leaderElect, "leader-elect", true, "Enable leader election.")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Metrics endpoint address; 0 disables it.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health probe endpoint address.")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("setup")

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		log.Error(err, "scheme")
		os.Exit(1)
	}
	if err := v1.AddToScheme(scheme); err != nil {
		log.Error(err, "scheme")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "module-controller.modules.brics.ac.uk",
		Cache:                  controller.CacheOptions(modulesNamespace),
	})
	if err != nil {
		log.Error(err, "creating manager")
		os.Exit(1)
	}
	if err := (&controller.ModuleReconciler{Client: mgr.GetClient(), ModulesNamespace: modulesNamespace}).SetupWithManager(mgr); err != nil {
		log.Error(err, "setting up controller")
		os.Exit(1)
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "healthz")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "readyz")
		os.Exit(1)
	}
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "running manager")
		os.Exit(1)
	}
}
```

- [ ] **Step 6: Dockerfile and .dockerignore**

```dockerfile
FROM docker.io/library/golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY internal/ internal/
COPY cmd/ cmd/
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/module-controller ./cmd/module-controller

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/module-controller /module-controller
USER 65532:65532
ENTRYPOINT ["/module-controller"]
```

`.dockerignore`:
```
bin/
deploy/
docs/
test/
.git/
```

- [ ] **Step 7: Build** — Create `test/e2e/doc.go` (`//go:build e2e` / `package e2e`) so the Makefile's tagged vet has a package, then run `make build`. Expected: `bin/module-controller` exists and vet is clean.

- [ ] **Step 8: Commit** — `git add internal/controller cmd Dockerfile .dockerignore && git commit -m "feat(controller): Module reconciler, PVC checks and binary"`

---

### Task 5: Helm chart (controller half) and envtest harness with CRD and controller tests

**Files:**
- Create: `deploy/helm/k8s-modules/Chart.yaml`, `values.yaml`, `templates/_helpers.tpl`, `templates/serviceaccount.yaml`, `templates/rbac.yaml`, `templates/deployment.yaml`
- Create: `test/envtest/suite_test.go`, `test/envtest/helpers_test.go`, `test/envtest/crd_test.go`, `test/envtest/controller_test.go`

**Interfaces:**
- Consumes: `controller.ModuleReconciler`, `controller.CacheOptions`, the generated CRD in `deploy/helm/k8s-modules/crds`.
- Produces (test package `integration`): `k8s client.Client`, `ctx context.Context`, `newFamily(base string) string`, `imageModule(family, version string, opts ...func(*v1.Module)) *v1.Module`, `withDeps(...string)`, `withEnv(kv ...string)`, `withLib(...string)`, `withAccess(map[string]string)`, `withDefault()`, `create(t, obj)`, `waitCondition(t, name, condType string, status metav1.ConditionStatus, reason string) *v1.Module`, `eventually(t, timeout, func() error)`, `const testRegistry = "registry.test/modules/"`.

- [ ] **Step 1: Chart.yaml, values.yaml, _helpers.tpl**

```yaml
# Chart.yaml
apiVersion: v2
name: k8s-modules
description: Module CRD, controller and admission policies for loading shared-library modules into pods
type: application
version: 0.1.0
appVersion: "0.1.0"
kubeVersion: ">=1.36.0-0"
```

```yaml
# values.yaml
image:
  repository: ghcr.io/bristol-supercomputing/module-controller
  tag: ""
  pullPolicy: IfNotPresent
modulesNamespace: module-system
registryAllowlist:
  - registry.isambard/modules/
envPrefixAllowlist: [NCCL_, FI_, CXIP_, UCX_, OMPI_]
controller:
  replicas: 1
  leaderElect: true
  resources: {}
```

```
{{/* _helpers.tpl */}}
{{- define "k8s-modules.labels" -}}
app.kubernetes.io/name: module-controller
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/part-of: k8s-modules
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end }}
{{- define "k8s-modules.selector" -}}
app.kubernetes.io/name: module-controller
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
{{- define "k8s-modules.podPolicyNamespaces" -}}
namespaceSelector:
  matchExpressions:
  - {key: kubernetes.io/metadata.name, operator: NotIn, values: [kube-system]}
{{- end }}
```

- [ ] **Step 2: serviceaccount.yaml, rbac.yaml, deployment.yaml**

```yaml
# serviceaccount.yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: module-controller
  namespace: {{ .Release.Namespace }}
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
```

```yaml
# rbac.yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: {{ .Release.Name }}-module-controller
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
rules:
- apiGroups: [modules.brics.ac.uk]
  resources: [modules]
  verbs: [get, list, watch]
- apiGroups: [modules.brics.ac.uk]
  resources: [modules/status]
  verbs: [update, patch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: {{ .Release.Name }}-module-controller
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: {{ .Release.Name }}-module-controller}
subjects:
- {kind: ServiceAccount, name: module-controller, namespace: {{ .Release.Namespace }}}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: module-controller-pvcs
  namespace: {{ .Values.modulesNamespace }}
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
rules:
- apiGroups: [""]
  resources: [persistentvolumeclaims]
  verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: module-controller-pvcs
  namespace: {{ .Values.modulesNamespace }}
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: module-controller-pvcs}
subjects:
- {kind: ServiceAccount, name: module-controller, namespace: {{ .Release.Namespace }}}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: module-controller-leader-election
  namespace: {{ .Release.Namespace }}
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
rules:
- apiGroups: [coordination.k8s.io]
  resources: [leases]
  verbs: [get, list, watch, create, update, patch, delete]
- apiGroups: [""]
  resources: [events]
  verbs: [create, patch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: module-controller-leader-election
  namespace: {{ .Release.Namespace }}
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: module-controller-leader-election}
subjects:
- {kind: ServiceAccount, name: module-controller, namespace: {{ .Release.Namespace }}}
```

```yaml
# deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: module-controller
  namespace: {{ .Release.Namespace }}
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  replicas: {{ .Values.controller.replicas }}
  selector:
    matchLabels: {{- include "k8s-modules.selector" . | nindent 6 }}
  template:
    metadata:
      labels: {{- include "k8s-modules.labels" . | nindent 8 }}
    spec:
      serviceAccountName: module-controller
      securityContext:
        runAsNonRoot: true
        seccompProfile: {type: RuntimeDefault}
      containers:
      - name: controller
        image: "{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}"
        imagePullPolicy: {{ .Values.image.pullPolicy }}
        args:
        - --modules-namespace={{ .Values.modulesNamespace }}
        - --leader-elect={{ .Values.controller.leaderElect }}
        - --metrics-bind-address=:8080
        - --health-probe-bind-address=:8081
        ports:
        - {name: metrics, containerPort: 8080}
        - {name: probes, containerPort: 8081}
        livenessProbe: {httpGet: {path: /healthz, port: probes}}
        readinessProbe: {httpGet: {path: /readyz, port: probes}}
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities: {drop: [ALL]}
        resources: {{- toYaml .Values.controller.resources | nindent 10 }}
```

Run: `helm lint deploy/helm/k8s-modules && helm template t deploy/helm/k8s-modules -n module-system >/dev/null`
Expected: no lint errors.

- [ ] **Step 3: suite_test.go**

```go
package integration

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	"github.com/bristol-supercomputing/k8s-modules/internal/controller"
)

const modulesNamespace = "module-system"

var (
	k8s    client.Client
	ctx    context.Context
	scheme = runtime.NewScheme()
	chart  = filepath.Join("..", "..", "deploy", "helm", "k8s-modules")
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctrl.SetLogger(zap.New(zap.WriteTo(io.Discard)))
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(chart, "crds")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "envtest start:", err)
		return 1
	}
	defer func() { _ = env.Stop() }()

	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)
	if k8s, err = client.New(cfg, client.Options{Scheme: scheme}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: modulesNamespace}}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Cache:                  controller.CacheOptions(modulesNamespace),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := (&controller.ModuleReconciler{Client: mgr.GetClient(), ModulesNamespace: modulesNamespace}).SetupWithManager(mgr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	go func() { _ = mgr.Start(ctx) }()

	if err := installPolicies(); err != nil {
		fmt.Fprintln(os.Stderr, "installing policies:", err)
		return 1
	}
	return m.Run()
}
```

In Task 5, `installPolicies` is a stub that returns `nil`. Task 6 replaces it.

```go
// policies_test.go (Task 5 stub)
package integration

func installPolicies() error { return nil }
```

- [ ] **Step 4: helpers_test.go**

```go
package integration

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

const testRegistry = "registry.test/modules/"

var familySeq atomic.Int64

// newFamily returns a family name unique within this test run, so tests
// sharing one apiserver never see each other's Modules.
func newFamily(base string) string {
	return fmt.Sprintf("%s-%d", base, familySeq.Add(1))
}

func digest() string { return "@sha256:" + strings.Repeat("a", 64) }

func imageModule(family, version string, opts ...func(*v1.Module)) *v1.Module {
	m := &v1.Module{
		ObjectMeta: metav1.ObjectMeta{Name: family + "-" + version, Labels: map[string]string{v1.LabelFamily: family}},
		Spec: v1.ModuleSpec{Family: family, Version: version,
			Source: v1.ModuleSource{Image: &v1.ImageSource{Reference: testRegistry + family + digest()}}},
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

func withDeps(d ...string) func(*v1.Module) { return func(m *v1.Module) { m.Spec.DependsOn = d } }

func ensureEnv(m *v1.Module) *v1.ModuleEnv {
	if m.Spec.Env == nil {
		m.Spec.Env = &v1.ModuleEnv{}
	}
	return m.Spec.Env
}

func withEnv(kv ...string) func(*v1.Module) {
	return func(m *v1.Module) {
		e := ensureEnv(m)
		e.Set = map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			e.Set[kv[i]] = kv[i+1]
		}
	}
}

func withLib(p ...string) func(*v1.Module) {
	return func(m *v1.Module) { ensureEnv(m).PrependPath = map[string]v1.PathList{v1.EnvLDLibraryPath: p} }
}

func withAccess(labels map[string]string) func(*v1.Module) {
	return func(m *v1.Module) { m.Spec.Access = &v1.ModuleAccess{MatchLabels: labels} }
}

func withDefault() func(*v1.Module) {
	return func(m *v1.Module) { m.Labels[v1.LabelDefault] = "true" }
}

func create(t *testing.T, obj client.Object) {
	t.Helper()
	if err := k8s.Create(ctx, obj); err != nil {
		t.Fatalf("create %T %s: %v", obj, obj.GetName(), err)
	}
	t.Cleanup(func() { _ = k8s.Delete(ctx, obj) })
}

func eventually(t *testing.T, timeout time.Duration, f func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		err := f()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s: %v", timeout, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitCondition(t *testing.T, name, condType string, status metav1.ConditionStatus, reason string) *v1.Module {
	t.Helper()
	m := &v1.Module{}
	eventually(t, 15*time.Second, func() error {
		if err := k8s.Get(ctx, client.ObjectKey{Name: name}, m); err != nil {
			return err
		}
		c := meta.FindStatusCondition(m.Status.Conditions, condType)
		if c == nil || c.Status != status || c.Reason != reason || c.ObservedGeneration != m.Generation {
			return fmt.Errorf("module %s condition %s = %+v, want %s/%s", name, condType, c, status, reason)
		}
		return nil
	})
	return m
}

func waitResolved(t *testing.T, name string) *v1.Module {
	t.Helper()
	return waitCondition(t, name, v1.ConditionResolved, metav1.ConditionTrue, "Resolved")
}

func expectCreateError(t *testing.T, obj client.Object, substr string) {
	t.Helper()
	err := k8s.Create(ctx, obj)
	if err == nil {
		_ = k8s.Delete(ctx, obj)
		t.Fatalf("create %s succeeded, want error containing %q", obj.GetName(), substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("create %s: error %q does not contain %q", obj.GetName(), err, substr)
	}
	_ = apierrors.IsInvalid(err)
}
```

- [ ] **Step 5: crd_test.go** (failing if any CRD rule is missing or wrong)

```go
package integration

import (
	"strings"
	"testing"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

func TestCRDValidation(t *testing.T) {
	ok := func(f string) *v1.Module { return imageModule(f, "1.0") }
	cases := []struct {
		name   string
		mutate func(*v1.Module)
		want   string // "" means the create must succeed
	}{
		{"valid image", func(m *v1.Module) {}, ""},
		{"valid host", func(m *v1.Module) {
			m.Spec.Source = v1.ModuleSource{Host: &v1.HostSource{Discover: v1.HostDiscover{Roots: []string{"libfabric.so.1", "libcxi.so"}}}}
		}, ""},
		{"valid pvc", func(m *v1.Module) {
			m.Spec.Source = v1.ModuleSource{PVC: &v1.PVCSource{ClaimName: "spack-apps", SubPath: "nccl/2.29.2"}}
		}, ""},
		{"name mismatch", func(m *v1.Module) { m.Name = m.Name + "x" }, "metadata.name must be <spec.family>-<spec.version>"},
		{"tag not digest", func(m *v1.Module) { m.Spec.Source.Image.Reference = testRegistry + "x:latest" }, "spec.source.image.reference"},
		{"uppercase digest", func(m *v1.Module) { m.Spec.Source.Image.Reference = testRegistry + "x@sha256:" + strings.Repeat("A", 64) }, "spec.source.image.reference"},
		{"two sources", func(m *v1.Module) {
			m.Spec.Source.Host = &v1.HostSource{Discover: v1.HostDiscover{Roots: []string{"libx.so"}}}
		}, "exactly one of image, host or pvc"},
		{"no source", func(m *v1.Module) { m.Spec.Source = v1.ModuleSource{} }, "exactly one of image, host or pvc"},
		{"empty roots", func(m *v1.Module) {
			m.Spec.Source = v1.ModuleSource{Host: &v1.HostSource{Discover: v1.HostDiscover{Roots: []string{}}}}
		}, "spec.source.host.discover.roots"},
		{"root with slash", func(m *v1.Module) {
			m.Spec.Source = v1.ModuleSource{Host: &v1.HostSource{Discover: v1.HostDiscover{Roots: []string{"/usr/lib/libx.so"}}}}
		}, "spec.source.host.discover.roots"},
		{"pvc absolute subPath", func(m *v1.Module) {
			m.Spec.Source = v1.ModuleSource{PVC: &v1.PVCSource{ClaimName: "c", SubPath: "/abs"}}
		}, "spec.source.pvc.subPath"},
		{"pvc dotdot subPath", func(m *v1.Module) {
			m.Spec.Source = v1.ModuleSource{PVC: &v1.PVCSource{ClaimName: "c", SubPath: "a/../b"}}
		}, "'..' segments"},
		{"pvc bad claim", func(m *v1.Module) {
			m.Spec.Source = v1.ModuleSource{PVC: &v1.PVCSource{ClaimName: "Bad_Claim", SubPath: "a"}}
		}, "spec.source.pvc.claimName"},
		{"prependPath key PATH", func(m *v1.Module) {
			m.Spec.Env = &v1.ModuleEnv{PrependPath: map[string]v1.PathList{"PATH": {"bin"}}}
		}, "only LD_LIBRARY_PATH may be prepended"},
		{"prependPath absolute", func(m *v1.Module) { withLib("/lib")(m) }, "spec.env.prependPath"},
		{"prependPath dotdot", func(m *v1.Module) { withLib("lib/../..")(m) }, "'..' segments"},
		{"prependPath colon", func(m *v1.Module) { withLib("lib:evil")(m) }, "spec.env.prependPath"},
		{"env LD_PRELOAD", func(m *v1.Module) { withEnv("LD_PRELOAD", "x")(m) }, "may not set LD_PRELOAD"},
		{"env PATH", func(m *v1.Module) { withEnv("PATH", "x")(m) }, "may not set LD_PRELOAD"},
		{"env bad name", func(m *v1.Module) { withEnv("1NCCL", "x")(m) }, "env.set names must match"},
		{"self dependency", func(m *v1.Module) { m.Spec.DependsOn = []string{m.Name} }, "cannot depend on itself"},
		{"relative maskPath", func(m *v1.Module) { m.Spec.MaskPaths = []string{"usr/lib"} }, "spec.maskPaths"},
		{"uppercase family", func(m *v1.Module) { m.Spec.Family = "NCCL"; m.Name = "NCCL-1.0" }, "spec.family"},
		{"version too long", func(m *v1.Module) {
			m.Spec.Version = strings.Repeat("1", 21)
			m.Name = m.Spec.Family + "-" + m.Spec.Version
		}, "spec.version"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := ok(newFamily("crd"))
			c.mutate(m)
			if c.want == "" {
				create(t, m)
				return
			}
			expectCreateError(t, m, c.want)
		})
	}
}

func TestCRDSpecImmutable(t *testing.T) {
	m := imageModule(newFamily("immut"), "1.0", withEnv("NCCL_DEBUG", "INFO"))
	create(t, m)
	m.Spec.Env.Set["NCCL_DEBUG"] = "WARN"
	err := k8s.Update(ctx, m)
	if err == nil || !strings.Contains(err.Error(), "spec is immutable") {
		t.Fatalf("update: %v, want spec is immutable", err)
	}
}
```

Run: `make test-envtest`
Expected: all CRD cases pass. If the apiserver rejects the CRD for CEL cost, add `MaxLength` to the offending string or map value in `module_types.go` and rerun `make manifests`. For map values, use `// +kubebuilder:validation:items:MaxLength` on a named type, or `+kubebuilder:validation:XValidation` with `size()`.

- [ ] **Step 6: controller_test.go**

```go
package integration

import (
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

func TestControllerResolvesChain(t *testing.T) {
	a, b, c := newFamily("chain-a"), newFamily("chain-b"), newFamily("chain-c")
	create(t, imageModule(c, "1"))
	create(t, imageModule(b, "1", withDeps(c+"-1")))
	create(t, imageModule(a, "1", withDeps(b+"-1")))
	m := waitResolved(t, a+"-1")
	if want := []string{c + "-1", b + "-1", a + "-1"}; !reflect.DeepEqual(m.Status.Resolved.Modules, want) {
		t.Errorf("modules = %v, want %v", m.Status.Resolved.Modules, want)
	}
	if v := m.Status.Resolved.Annotations[v1.AnnotationResolvedPrefix+a]; !strings.Contains(v, ";rank=30;") {
		t.Errorf("annotation %q, want rank=30", v)
	}
	waitCondition(t, a+"-1", v1.ConditionConflictFree, metav1.ConditionTrue, "NoConflicts")
}

func TestControllerLateDependency(t *testing.T) {
	a, dep := newFamily("late"), newFamily("late-dep")
	create(t, imageModule(a, "1", withDeps(dep+"-1")))
	m := waitCondition(t, a+"-1", v1.ConditionResolved, metav1.ConditionFalse, "DependencyNotFound")
	if m.Status.Resolved != nil {
		t.Error("unresolved module must have no status.resolved")
	}
	waitCondition(t, a+"-1", v1.ConditionConflictFree, metav1.ConditionUnknown, "NotEvaluated")
	create(t, imageModule(dep, "1"))
	waitResolved(t, a+"-1")
}

func TestControllerDeletedDependency(t *testing.T) {
	a, dep := newFamily("del"), newFamily("del-dep")
	d := imageModule(dep, "1")
	create(t, d)
	create(t, imageModule(a, "1", withDeps(dep+"-1")))
	waitResolved(t, a+"-1")
	if err := k8s.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}
	waitCondition(t, a+"-1", v1.ConditionResolved, metav1.ConditionFalse, "DependencyNotFound")
}

func TestControllerCycle(t *testing.T) {
	a, b := newFamily("cyc-a"), newFamily("cyc-b")
	create(t, imageModule(a, "1", withDeps(b+"-1")))
	create(t, imageModule(b, "1", withDeps(a+"-1")))
	m := waitCondition(t, a+"-1", v1.ConditionResolved, metav1.ConditionFalse, "DependencyCycle")
	c := findCond(m, v1.ConditionResolved)
	if !strings.Contains(c.Message, "dependency cycle") {
		t.Errorf("message %q", c.Message)
	}
}

func TestControllerConflicts(t *testing.T) {
	fam := newFamily("cf")
	create(t, imageModule(fam, "1"))
	create(t, imageModule(fam, "2"))
	both := newFamily("cf-both")
	create(t, imageModule(both, "1", withDeps(fam+"-1", fam+"-2")))
	waitCondition(t, both+"-1", v1.ConditionResolved, metav1.ConditionFalse, "Conflict")
	m := waitCondition(t, both+"-1", v1.ConditionConflictFree, metav1.ConditionFalse, "FamilyConflict")
	if msg := findCond(m, v1.ConditionConflictFree).Message; !strings.Contains(msg, fam) {
		t.Errorf("message %q does not name family %s", msg, fam)
	}

	p, q, top := newFamily("ec-p"), newFamily("ec-q"), newFamily("ec-top")
	create(t, imageModule(p, "1", withEnv("NCCL_ALGO", "ring")))
	create(t, imageModule(q, "1", withEnv("NCCL_ALGO", "tree")))
	create(t, imageModule(top, "1", withDeps(p+"-1", q+"-1")))
	m = waitCondition(t, top+"-1", v1.ConditionConflictFree, metav1.ConditionFalse, "EnvConflict")
	if msg := findCond(m, v1.ConditionConflictFree).Message; !strings.Contains(msg, "NCCL_ALGO") {
		t.Errorf("message %q does not name the variable", msg)
	}
}

func TestControllerPVC(t *testing.T) {
	missing, rwo, rox, dependent := newFamily("pvc-missing"), newFamily("pvc-rwo"), newFamily("pvc-rox"), newFamily("pvc-dep")
	pvcModule := func(fam, claim string) *v1.Module {
		m := imageModule(fam, "1")
		m.Spec.Source = v1.ModuleSource{PVC: &v1.PVCSource{ClaimName: claim, SubPath: "x"}}
		return m
	}
	pvc := func(name string, mode corev1.PersistentVolumeAccessMode) *corev1.PersistentVolumeClaim {
		return &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: modulesNamespace},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{mode},
				Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")}},
			},
		}
	}

	create(t, pvcModule(missing, missing))
	m := waitCondition(t, missing+"-1", v1.ConditionResolved, metav1.ConditionFalse, "PVCInvalid")
	if msg := findCond(m, v1.ConditionResolved).Message; !strings.Contains(msg, "does not exist") {
		t.Errorf("message %q", msg)
	}

	create(t, pvc(rwo, corev1.ReadWriteOnce))
	create(t, pvcModule(rwo, rwo))
	m = waitCondition(t, rwo+"-1", v1.ConditionResolved, metav1.ConditionFalse, "PVCInvalid")
	if msg := findCond(m, v1.ConditionResolved).Message; !strings.Contains(msg, "ReadOnlyMany") {
		t.Errorf("message %q", msg)
	}

	create(t, pvc(rox, corev1.ReadOnlyMany))
	create(t, pvcModule(rox, rox))
	waitResolved(t, rox+"-1")

	// A dependent of a pvc module is checked too, and recovers when the PVC appears.
	create(t, imageModule(dependent, "1", withDeps(missing+"-1")))
	waitCondition(t, dependent+"-1", v1.ConditionResolved, metav1.ConditionFalse, "PVCInvalid")
	create(t, pvc(missing, corev1.ReadOnlyMany))
	waitResolved(t, dependent+"-1")
	waitResolved(t, missing+"-1")
}

func findCond(m *v1.Module, typ string) *metav1.Condition {
	for i := range m.Status.Conditions {
		if m.Status.Conditions[i].Type == typ {
			return &m.Status.Conditions[i]
		}
	}
	return &metav1.Condition{}
}
```

- [ ] **Step 7: Run** — `make test-envtest`. Expected: PASS.

- [ ] **Step 8: Commit** — `git add deploy/helm test/envtest && git commit -m "feat(chart): controller deployment and RBAC; envtest CRD and controller tests"`

---

### Task 6: Admission policies and their envtest tests

**Files:**
- Create: `deploy/helm/k8s-modules/templates/admission-mutating.yaml`, `admission-pods.yaml`, `admission-pod-consistency.yaml`, `admission-module-guards.yaml`, `admission-module-default.yaml`
- Modify: `test/envtest/policies_test.go` (replace the stub)
- Create: `test/envtest/admission_test.go`

**Interfaces:**
- Consumes: Task 5 helpers; `resolve.VolumeName`, `resolve.AnnotationValue`.
- Produces: the policy names `modules.brics.ac.uk` (MAP), `pods.modules.brics.ac.uk`, `pod-consistency.modules.brics.ac.uk`, `module-guards.modules.brics.ac.uk`, `module-default.modules.brics.ac.uk`. Each binding has the same name as its policy.

- [ ] **Step 1: admission-mutating.yaml** (validated in the planning spike)

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicy
metadata:
  name: modules.brics.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  failurePolicy: Fail
  paramKind: {apiVersion: modules.brics.ac.uk/v1alpha1, kind: Module}
  matchConstraints:
    {{- include "k8s-modules.podPolicyNamespaces" . | nindent 4 }}
    resourceRules:
    - {apiGroups: [""], apiVersions: [v1], operations: [CREATE], resources: [pods]}
  matchConditions:
  - name: has-load
    expression: "has(object.metadata.annotations) && 'modules.brics.ac.uk/load' in object.metadata.annotations"
  variables:
  - name: requested
    expression: "object.metadata.annotations['modules.brics.ac.uk/load'].split(',').map(s, s.trim()).filter(s, s != '')"
  - name: isDefault
    expression: "has(params.metadata.labels) && 'modules.brics.ac.uk/default' in params.metadata.labels && params.metadata.labels['modules.brics.ac.uk/default'] == 'true'"
  - name: isRequested
    expression: "params.metadata.name in variables.requested || (params.spec.family in variables.requested && variables.isDefault)"
  - name: nsAllowed
    expression: >-
      !has(params.spec.access) || !has(params.spec.access.matchLabels) ||
      params.spec.access.matchLabels.all(k, has(namespaceObject.metadata.labels) &&
        k in namespaceObject.metadata.labels && namespaceObject.metadata.labels[k] == params.spec.access.matchLabels[k])
  - name: applies
    expression: "has(params.status) && has(params.status.resolved) && variables.isRequested && variables.nsAllowed"
  - name: volumes
    expression: "variables.applies && has(params.status.resolved.volumes) ? params.status.resolved.volumes : []"
  - name: mounts
    expression: "variables.applies && has(params.status.resolved.mounts) ? params.status.resolved.mounts : []"
  - name: env
    expression: "variables.applies && has(params.status.resolved.env) ? params.status.resolved.env : []"
  reinvocationPolicy: IfNeeded
  mutations:
  - patchType: ApplyConfiguration
    applyConfiguration:
      expression: >-
        !variables.applies ? Object{} :
        Object{
          metadata: Object.metadata{annotations: params.status.resolved.annotations},
          spec: Object.spec{
            volumes: variables.volumes.map(v, Object.spec.volumes{
              name: v.name,
              image: Object.spec.volumes.image{reference: v.reference, pullPolicy: "IfNotPresent"}
            }),
            containers: object.spec.containers.map(c, Object.spec.containers{
              name: c.name,
              volumeMounts: variables.mounts.map(m, Object.spec.containers.volumeMounts{name: m.name, mountPath: m.mountPath, readOnly: true}),
              env: variables.env.filter(e, !has(c.env) || !c.env.exists(ce, ce.name == e.name)).map(e, Object.spec.containers.env{name: e.name, value: e.value})
            })
          }
        }
---
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicyBinding
metadata:
  name: modules.brics.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  policyName: modules.brics.ac.uk
  paramRef: {selector: {}, parameterNotFoundAction: Allow}
```

- [ ] **Step 2: admission-pods.yaml**

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: pods.modules.brics.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  failurePolicy: Fail
  matchConstraints:
    {{- include "k8s-modules.podPolicyNamespaces" . | nindent 4 }}
    resourceRules:
    - {apiGroups: [""], apiVersions: [v1], operations: [CREATE, UPDATE], resources: [pods]}
  matchConditions:
  - name: has-module-annotations
    expression: >-
      (has(object.metadata.annotations) && object.metadata.annotations.exists(k,
        k == 'modules.brics.ac.uk/load' || k.startsWith('resolved.modules.brics.ac.uk/') || k.startsWith('env.modules.brics.ac.uk/'))) ||
      (request.operation == 'UPDATE' && has(oldObject.metadata.annotations) && oldObject.metadata.annotations.exists(k,
        k == 'modules.brics.ac.uk/load' || k.startsWith('resolved.modules.brics.ac.uk/') || k.startsWith('env.modules.brics.ac.uk/')))
  variables:
  - name: anns
    expression: "has(object.metadata.annotations) ? object.metadata.annotations : {}"
  - name: oldAnns
    expression: "request.operation == 'UPDATE' && has(oldObject.metadata.annotations) ? oldObject.metadata.annotations : {}"
  - name: requested
    expression: "'modules.brics.ac.uk/load' in variables.anns ? variables.anns['modules.brics.ac.uk/load'].split(',').map(s, s.trim()).filter(s, s != '') : []"
  - name: badNames
    expression: "variables.requested.filter(r, r.size() > 61 || !r.matches('^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$'))"
  - name: unknown
    expression: >-
      variables.requested.filter(r,
        !(('resolved.modules.brics.ac.uk/' + r) in variables.anns) &&
        !variables.anns.exists(k, k.startsWith('resolved.modules.brics.ac.uk/') && variables.anns[k].startsWith(r + ';')))
  validations:
  - expression: "request.operation != 'CREATE' || variables.requested.size() <= 32"
    message: "modules.brics.ac.uk/load lists more than 32 modules"
  - expression: "request.operation != 'CREATE' || variables.badNames.size() == 0"
    messageExpression: "'invalid module name \"' + variables.badNames[0] + '\" in modules.brics.ac.uk/load'"
  - expression: "request.operation != 'CREATE' || variables.badNames.size() > 0 || variables.unknown.size() == 0"
    messageExpression: >-
      'module "' + variables.unknown[0] + '" was not loaded: not found, not resolved, or not permitted in namespace "' +
      request.namespace + '" (kubectl get module ' + variables.unknown[0] + ')'
  - expression: >-
      request.operation != 'UPDATE' || (
        variables.oldAnns.all(k,
          !(k == 'modules.brics.ac.uk/load' || k.startsWith('resolved.modules.brics.ac.uk/') || k.startsWith('env.modules.brics.ac.uk/')) ||
          (k in variables.anns && variables.anns[k] == variables.oldAnns[k])) &&
        variables.anns.all(k,
          !(k == 'modules.brics.ac.uk/load' || k.startsWith('resolved.modules.brics.ac.uk/') || k.startsWith('env.modules.brics.ac.uk/')) ||
          k in variables.oldAnns))
    message: "module annotations (modules.brics.ac.uk/load, resolved.modules.brics.ac.uk/*, env.modules.brics.ac.uk/*) cannot be changed after the pod is created"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: pods.modules.brics.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  policyName: pods.modules.brics.ac.uk
  validationActions: [Deny]
```

- [ ] **Step 3: admission-pod-consistency.yaml**

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: pod-consistency.modules.brics.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  failurePolicy: Fail
  paramKind: {apiVersion: modules.brics.ac.uk/v1alpha1, kind: Module}
  matchConstraints:
    {{- include "k8s-modules.podPolicyNamespaces" . | nindent 4 }}
    resourceRules:
    - {apiGroups: [""], apiVersions: [v1], operations: [CREATE], resources: [pods]}
  matchConditions:
  - name: has-module-annotations
    expression: >-
      has(object.metadata.annotations) && object.metadata.annotations.exists(k,
        k == 'modules.brics.ac.uk/load' || k.startsWith('resolved.modules.brics.ac.uk/'))
  variables:
  - name: anns
    expression: "object.metadata.annotations"
  - name: requested
    expression: "'modules.brics.ac.uk/load' in variables.anns ? variables.anns['modules.brics.ac.uk/load'].split(',').map(s, s.trim()).filter(s, s != '') : []"
  - name: isDefault
    expression: "has(params.metadata.labels) && 'modules.brics.ac.uk/default' in params.metadata.labels && params.metadata.labels['modules.brics.ac.uk/default'] == 'true'"
  - name: familyKey
    expression: "'resolved.modules.brics.ac.uk/' + params.spec.family"
  - name: isRequested
    expression: "params.metadata.name in variables.requested || (params.spec.family in variables.requested && variables.isDefault)"
  - name: references
    expression: >-
      variables.isRequested ||
      (variables.familyKey in variables.anns && variables.anns[variables.familyKey].startsWith(params.metadata.name + ';'))
  - name: isResolved
    expression: "has(params.status) && has(params.status.resolved)"
  - name: nsAllowed
    expression: >-
      !has(params.spec.access) || !has(params.spec.access.matchLabels) ||
      params.spec.access.matchLabels.all(k, has(namespaceObject.metadata.labels) &&
        k in namespaceObject.metadata.labels && namespaceObject.metadata.labels[k] == params.spec.access.matchLabels[k])
  - name: mismatched
    expression: >-
      variables.references && variables.isResolved ?
        params.status.resolved.annotations.filter(k, !(k in variables.anns) || variables.anns[k] != params.status.resolved.annotations[k]) : []
  validations:
  - expression: "!variables.isRequested || variables.isResolved"
    messageExpression: >-
      'module "' + params.metadata.name + '" is not resolved: ' +
      (has(params.status) && has(params.status.conditions) && params.status.conditions.exists(c, c.type == 'Resolved') ?
        params.status.conditions.filter(c, c.type == 'Resolved')[0].message : 'the module controller has not processed it yet')
  - expression: "!variables.references || variables.nsAllowed"
    messageExpression: "'module \"' + params.metadata.name + '\" is not permitted in namespace \"' + namespaceObject.metadata.name + '\"'"
  - expression: "variables.mismatched.size() == 0"
    messageExpression: >-
      'module "' + params.metadata.name + '" requires ' + variables.mismatched[0] + '=' +
      params.status.resolved.annotations[variables.mismatched[0]] + ', but the pod has ' +
      (variables.mismatched[0] in variables.anns ? variables.anns[variables.mismatched[0]] : '<unset>')
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: pod-consistency.modules.brics.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  policyName: pod-consistency.modules.brics.ac.uk
  validationActions: [Deny]
  paramRef: {selector: {}, parameterNotFoundAction: Allow}
```

- [ ] **Step 4: admission-module-guards.yaml and admission-module-default.yaml**

```yaml
# admission-module-guards.yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: module-guards.modules.brics.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
    - {apiGroups: [modules.brics.ac.uk], apiVersions: [v1alpha1], operations: [CREATE, UPDATE], resources: [modules]}
  variables:
  - name: registries
    expression: |-
      {{ toJson .Values.registryAllowlist }}
  - name: envPrefixes
    expression: |-
      {{ toJson .Values.envPrefixAllowlist }}
  validations:
  - expression: "!has(object.spec.source.image) || variables.registries.exists(p, object.spec.source.image.reference.startsWith(p))"
    messageExpression: "'image ' + object.spec.source.image.reference + ' is not in an allowed registry (' + variables.registries.join(', ') + ')'"
  - expression: "!has(object.spec.env) || !has(object.spec.env.set) || object.spec.env.set.all(k, variables.envPrefixes.exists(p, k.startsWith(p)))"
    messageExpression: >-
      'env.set name ' + object.spec.env.set.filter(k, !variables.envPrefixes.exists(p, k.startsWith(p)))[0] +
      ' must start with one of ' + variables.envPrefixes.join(', ')
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: module-guards.modules.brics.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  policyName: module-guards.modules.brics.ac.uk
  validationActions: [Deny]
```

```yaml
# admission-module-default.yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: module-default.modules.brics.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  failurePolicy: Fail
  paramKind: {apiVersion: modules.brics.ac.uk/v1alpha1, kind: Module}
  matchConstraints:
    resourceRules:
    - {apiGroups: [modules.brics.ac.uk], apiVersions: [v1alpha1], operations: [CREATE, UPDATE], resources: [modules]}
  matchConditions:
  - name: is-default
    expression: "has(object.metadata.labels) && 'modules.brics.ac.uk/default' in object.metadata.labels && object.metadata.labels['modules.brics.ac.uk/default'] == 'true'"
  validations:
  - expression: >-
      params.metadata.name == object.metadata.name || params.spec.family != object.spec.family ||
      !(has(params.metadata.labels) && 'modules.brics.ac.uk/default' in params.metadata.labels && params.metadata.labels['modules.brics.ac.uk/default'] == 'true')
    messageExpression: "'module \"' + params.metadata.name + '\" is already the default for family \"' + object.spec.family + '\"'"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: module-default.modules.brics.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  policyName: module-default.modules.brics.ac.uk
  validationActions: [Deny]
  paramRef: {selector: {}, parameterNotFoundAction: Allow}
```

Run: `helm lint deploy/helm/k8s-modules && helm template t deploy/helm/k8s-modules -n module-system | grep -A2 'name: registries'`
Expected: lint passes, and the expression renders as `["registry.isambard/modules/"]`.

- [ ] **Step 5: policies_test.go** (replaces the stub; installs the rendered policies and waits until every one is live)

```go
package integration

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

const probeNS = "policy-probe"

func installPolicies() error {
	out, err := exec.Command("helm", "template", "k8s-modules", chart,
		"--namespace", modulesNamespace,
		"--set", "registryAllowlist[0]="+testRegistry).Output()
	if err != nil {
		return fmt.Errorf("helm template: %w", err)
	}
	dec := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(out), 4096)
	for {
		u := &unstructured.Unstructured{}
		if err := dec.Decode(&u.Object); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}
		if !strings.HasPrefix(u.GetAPIVersion(), "admissionregistration.k8s.io/") {
			continue
		}
		if err := k8s.Create(ctx, u); err != nil {
			return fmt.Errorf("create %s %s: %w", u.GetKind(), u.GetName(), err)
		}
	}
	return waitForPolicies()
}

// waitForPolicies blocks until each policy and its param informer is live,
// so tests never race policy start-up.
func waitForPolicies() error {
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: probeNS}}); err != nil {
		return err
	}
	probe := imageModule("probe", "1", withDefault())
	if err := k8s.Create(ctx, probe); err != nil {
		return err
	}
	probePod := func(anns map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "probe-", Namespace: probeNS, Annotations: anns},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "busybox"}}},
		}
	}
	checks := []struct {
		name string
		try  func() (bool, error)
	}{
		{"mutating policy", func() (bool, error) {
			p := probePod(map[string]string{v1.AnnotationLoad: "probe-1"})
			if err := k8s.Create(ctx, p); err != nil {
				return false, nil
			}
			defer k8s.Delete(ctx, p)
			_, ok := p.Annotations[v1.AnnotationResolvedPrefix+"probe"]
			return ok, nil
		}},
		{"pods policy", func() (bool, error) { return rejected(probePod(map[string]string{v1.AnnotationLoad: "Bad!"})), nil }},
		{"pod-consistency policy", func() (bool, error) {
			return rejected(probePod(map[string]string{v1.AnnotationResolvedPrefix + "probe": "probe-1;rank=99"})), nil
		}},
		{"module-guards policy", func() (bool, error) {
			m := imageModule("probe", "2")
			m.Spec.Source.Image.Reference = "evil.test/x" + digest()
			return rejected(m), nil
		}},
		{"module-default policy", func() (bool, error) { return rejected(imageModule("probe", "3", withDefault())), nil }},
	}
	for _, c := range checks {
		deadline := time.Now().Add(60 * time.Second)
		for {
			ok, err := c.try()
			if err != nil {
				return err
			}
			if ok {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%s never became active", c.name)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	return nil
}

// rejected creates obj and reports whether admission refused it. An accepted
// object is deleted again.
func rejected(obj client.Object) bool {
	if err := k8s.Create(ctx, obj); err != nil {
		return true
	}
	_ = k8s.Delete(ctx, obj)
	return false
}
```

- [ ] **Step 6: admission_test.go**

```go
package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	"github.com/bristol-supercomputing/k8s-modules/internal/resolve"
)

var nsSeq int

func newNamespace(t *testing.T, labels map[string]string) string {
	t.Helper()
	nsSeq++
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("adm-%d", nsSeq), Labels: labels}}
	create(t, ns)
	return ns.Name
}

func newPod(ns, load string, containers ...corev1.Container) *corev1.Pod {
	if len(containers) == 0 {
		containers = []corev1.Container{{Name: "main", Image: "busybox"}}
	}
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "p-", Namespace: ns, Annotations: map[string]string{}},
		Spec:       corev1.PodSpec{Containers: containers},
	}
	if load != "" {
		p.Annotations[v1.AnnotationLoad] = load
	}
	return p
}

// admit creates the pod, retrying briefly while admission's param informer
// catches up with Modules the test just created, until ready(pod) holds.
func admit(t *testing.T, build func() *corev1.Pod, ready func(*corev1.Pod) error) *corev1.Pod {
	t.Helper()
	var p *corev1.Pod
	eventually(t, 10*time.Second, func() error {
		p = build()
		if err := k8s.Create(ctx, p); err != nil {
			return err
		}
		if err := ready(p); err != nil {
			_ = k8s.Delete(ctx, p)
			return err
		}
		return nil
	})
	t.Cleanup(func() { _ = k8s.Delete(ctx, p) })
	return p
}

// reject expects admission to refuse the pod with a message containing any of substrs.
func reject(t *testing.T, build func() *corev1.Pod, substrs ...string) {
	t.Helper()
	eventually(t, 10*time.Second, func() error {
		p := build()
		err := k8s.Create(ctx, p)
		if err == nil {
			_ = k8s.Delete(ctx, p)
			return fmt.Errorf("pod was admitted with annotations %v", p.Annotations)
		}
		for _, s := range substrs {
			if strings.Contains(err.Error(), s) {
				return nil
			}
		}
		return fmt.Errorf("error %q contains none of %q", err, substrs)
	})
}

func hasAnnotations(want map[string]string) func(*corev1.Pod) error {
	return func(p *corev1.Pod) error {
		for k, v := range want {
			if p.Annotations[k] != v {
				return fmt.Errorf("annotation %s = %q, want %q", k, p.Annotations[k], v)
			}
		}
		return nil
	}
}

func envValue(c corev1.Container, name string) (string, bool) {
	for _, e := range c.Env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

func TestAdmissionSingleLoad(t *testing.T) {
	fam := newFamily("single")
	m := imageModule(fam, "2.29.2", withLib("lib"), withEnv("NCCL_DEBUG", "INFO"))
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)

	p := admit(t, func() *corev1.Pod {
		return newPod(ns, m.Name, corev1.Container{Name: "a", Image: "busybox"}, corev1.Container{Name: "b", Image: "busybox"})
	}, hasAnnotations(m.Status.Resolved.Annotations))

	vn := resolve.VolumeName(m.Name)
	if len(p.Spec.Volumes) != 1 || p.Spec.Volumes[0].Name != vn || p.Spec.Volumes[0].Image == nil ||
		p.Spec.Volumes[0].Image.Reference != m.Spec.Source.Image.Reference ||
		p.Spec.Volumes[0].Image.PullPolicy != corev1.PullIfNotPresent {
		t.Errorf("volumes = %+v", p.Spec.Volumes)
	}
	for _, c := range p.Spec.Containers {
		if len(c.VolumeMounts) != 1 || c.VolumeMounts[0].Name != vn || !c.VolumeMounts[0].ReadOnly ||
			c.VolumeMounts[0].MountPath != "/opt/modules/"+fam+"/2.29.2" {
			t.Errorf("container %s mounts = %+v", c.Name, c.VolumeMounts)
		}
		if v, _ := envValue(c, "NCCL_DEBUG"); v != "INFO" {
			t.Errorf("container %s NCCL_DEBUG = %q", c.Name, v)
		}
	}
	want := resolve.AnnotationValue(m, 10)
	if got := p.Annotations[v1.AnnotationResolvedPrefix+fam]; got != want {
		t.Errorf("annotation %q, want %q", got, want)
	}
}

func TestAdmissionDiamond(t *testing.T) {
	a, b, c, d := newFamily("dia-a"), newFamily("dia-b"), newFamily("dia-c"), newFamily("dia-d")
	create(t, imageModule(d, "1", withEnv("NCCL_PROTO", "simple")))
	create(t, imageModule(b, "1", withDeps(d+"-1")))
	create(t, imageModule(c, "1", withDeps(d+"-1")))
	create(t, imageModule(a, "1", withDeps(b+"-1", c+"-1")))
	top := waitResolved(t, a+"-1")
	waitResolved(t, b+"-1")
	waitResolved(t, c+"-1")
	ns := newNamespace(t, nil)

	p := admit(t, func() *corev1.Pod { return newPod(ns, a+"-1") }, hasAnnotations(top.Status.Resolved.Annotations))
	if len(p.Spec.Volumes) != 4 {
		t.Errorf("want 4 volumes, got %+v", p.Spec.Volumes)
	}
	// Requesting both sides separately merges the shared dependency.
	p = admit(t, func() *corev1.Pod { return newPod(ns, b+"-1,"+c+"-1") }, func(p *corev1.Pod) error {
		if len(p.Spec.Volumes) != 3 {
			return fmt.Errorf("want 3 volumes, got %d", len(p.Spec.Volumes))
		}
		return nil
	})
	if n := len(p.Spec.Containers[0].Env); n != 1 {
		t.Errorf("shared env must appear once, got %+v", p.Spec.Containers[0].Env)
	}
}

func TestAdmissionUserEnvWins(t *testing.T) {
	fam := newFamily("userenv")
	m := imageModule(fam, "1", withEnv("NCCL_DEBUG", "INFO"))
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	p := admit(t, func() *corev1.Pod {
		return newPod(ns, m.Name, corev1.Container{Name: "a", Image: "busybox", Env: []corev1.EnvVar{{Name: "NCCL_DEBUG", Value: "TRACE"}}})
	}, hasAnnotations(m.Status.Resolved.Annotations))
	if v, _ := envValue(p.Spec.Containers[0], "NCCL_DEBUG"); v != "TRACE" || len(p.Spec.Containers[0].Env) != 1 {
		t.Errorf("env = %+v, want only the user's NCCL_DEBUG=TRACE", p.Spec.Containers[0].Env)
	}
}

func TestAdmissionFamilyConflictAcrossRequests(t *testing.T) {
	nccl, p, q := newFamily("fc-nccl"), newFamily("fc-p"), newFamily("fc-q")
	create(t, imageModule(nccl, "1"))
	create(t, imageModule(nccl, "2"))
	create(t, imageModule(p, "1", withDeps(nccl+"-1")))
	create(t, imageModule(q, "1", withDeps(nccl+"-2")))
	for _, n := range []string{p + "-1", q + "-1", nccl + "-1", nccl + "-2"} {
		waitResolved(t, n)
	}
	ns := newNamespace(t, nil)
	reject(t, func() *corev1.Pod { return newPod(ns, p+"-1,"+q+"-1") }, "requires resolved.modules.brics.ac.uk/"+nccl)
	reject(t, func() *corev1.Pod { return newPod(ns, nccl+"-1,"+nccl+"-2") }, "requires resolved.modules.brics.ac.uk/"+nccl)
}

func TestAdmissionEnvConflictAcrossRequests(t *testing.T) {
	p, q := newFamily("envc-p"), newFamily("envc-q")
	create(t, imageModule(p, "1", withEnv("NCCL_ALGO", "ring")))
	create(t, imageModule(q, "1", withEnv("NCCL_ALGO", "tree")))
	waitResolved(t, p+"-1")
	waitResolved(t, q+"-1")
	ns := newNamespace(t, nil)
	reject(t, func() *corev1.Pod { return newPod(ns, p+"-1,"+q+"-1") }, "requires env.modules.brics.ac.uk/NCCL_ALGO")
}

func TestAdmissionForgedAnnotation(t *testing.T) {
	fam := newFamily("forge")
	m := imageModule(fam, "1")
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	key := v1.AnnotationResolvedPrefix + fam
	forged := m.Name + ";rank=10;src=image;mount=/etc"

	reject(t, func() *corev1.Pod {
		p := newPod(ns, "")
		p.Annotations[key] = forged
		return p
	}, "requires "+key)

	// Loading the module overwrites the forged value with the real one.
	admit(t, func() *corev1.Pod {
		p := newPod(ns, m.Name)
		p.Annotations[key] = forged
		return p
	}, hasAnnotations(m.Status.Resolved.Annotations))
}

func TestAdmissionAccess(t *testing.T) {
	fam := newFamily("acl")
	m := imageModule(fam, "1", withAccess(map[string]string{"team": "hpc"}))
	create(t, m)
	m = waitResolved(t, m.Name)
	denied := newNamespace(t, map[string]string{"team": "other"})
	allowed := newNamespace(t, map[string]string{"team": "hpc"})
	reject(t, func() *corev1.Pod { return newPod(denied, m.Name) }, "not permitted in namespace \""+denied+"\"")
	admit(t, func() *corev1.Pod { return newPod(allowed, m.Name) }, hasAnnotations(m.Status.Resolved.Annotations))

	// Access is enforced through dependencies too.
	top := newFamily("acl-top")
	create(t, imageModule(top, "1", withDeps(m.Name)))
	waitResolved(t, top+"-1")
	reject(t, func() *corev1.Pod { return newPod(denied, top+"-1") }, "not permitted in namespace \""+denied+"\"")
}

func TestAdmissionUnknownAndInvalid(t *testing.T) {
	ns := newNamespace(t, nil)
	reject(t, func() *corev1.Pod { return newPod(ns, "doesnotexist-1.0") }, `module "doesnotexist-1.0" was not loaded`)
	reject(t, func() *corev1.Pod { return newPod(ns, "Bad_Name") }, `invalid module name "Bad_Name"`)
	many := make([]string, 33)
	for i := range many {
		many[i] = fmt.Sprintf("m%d", i)
	}
	reject(t, func() *corev1.Pod { return newPod(ns, strings.Join(many, ",")) }, "more than 32 modules")
}

func TestAdmissionUnresolved(t *testing.T) {
	fam := newFamily("unres")
	create(t, imageModule(fam, "1", withDeps("missing-9.9")))
	waitCondition(t, fam+"-1", v1.ConditionResolved, metav1.ConditionFalse, "DependencyNotFound")
	ns := newNamespace(t, nil)
	// Either policy may report first (see plan deviation 2).
	reject(t, func() *corev1.Pod { return newPod(ns, fam+"-1") },
		`is not resolved: dependency "missing-9.9"`, `module "`+fam+`-1" was not loaded`)
}

func TestAdmissionFamilyDefault(t *testing.T) {
	fam := newFamily("deflt")
	def := imageModule(fam, "2", withDefault())
	create(t, def)
	create(t, imageModule(fam, "1"))
	def = waitResolved(t, def.Name)
	waitResolved(t, fam+"-1")
	ns := newNamespace(t, nil)
	admit(t, func() *corev1.Pod { return newPod(ns, fam) }, hasAnnotations(def.Status.Resolved.Annotations))
}

func TestAdmissionPodUpdate(t *testing.T) {
	fam := newFamily("upd")
	m := imageModule(fam, "1")
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	p := admit(t, func() *corev1.Pod { return newPod(ns, m.Name) }, hasAnnotations(m.Status.Resolved.Annotations))

	update := func(f func(*corev1.Pod)) error {
		cur := &corev1.Pod{}
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(p), cur); err != nil {
			return err
		}
		f(cur)
		return k8s.Update(ctx, cur)
	}
	if err := update(func(p *corev1.Pod) { p.Labels = map[string]string{"x": "y"}; p.Annotations["other"] = "ok" }); err != nil {
		t.Errorf("unrelated update rejected: %v", err)
	}
	for name, f := range map[string]func(*corev1.Pod){
		"change resolved": func(p *corev1.Pod) { p.Annotations[v1.AnnotationResolvedPrefix+fam] = "x;" },
		"remove resolved": func(p *corev1.Pod) { delete(p.Annotations, v1.AnnotationResolvedPrefix+fam) },
		"change load":     func(p *corev1.Pod) { p.Annotations[v1.AnnotationLoad] = "other-1" },
		"add env":         func(p *corev1.Pod) { p.Annotations[v1.AnnotationEnvPrefix+"NCCL_X"] = "1" },
	} {
		if err := update(f); err == nil || !strings.Contains(err.Error(), "cannot be changed after the pod is created") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestAdmissionPlainPodUntouched(t *testing.T) {
	ns := newNamespace(t, nil)
	p := newPod(ns, "")
	create(t, p)
	if len(p.Spec.Volumes) != 0 || len(p.Annotations) != 0 || len(p.Spec.Containers[0].Env) != 0 {
		t.Errorf("plain pod was mutated: %+v", p)
	}
}

func TestAdmissionLoadListWhitespaceAndDuplicates(t *testing.T) {
	fam := newFamily("ws")
	m := imageModule(fam, "1")
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	p := admit(t, func() *corev1.Pod { return newPod(ns, " "+m.Name+" , "+m.Name+" ,") }, hasAnnotations(m.Status.Resolved.Annotations))
	if len(p.Spec.Volumes) != 1 || len(p.Spec.Containers[0].VolumeMounts) != 1 {
		t.Errorf("duplicate load must inject once: %+v", p.Spec.Volumes)
	}
}

func TestAdmissionLongModuleName(t *testing.T) {
	// 36 + "-N" stays within the 40-char family limit; with a 20-char version
	// the name exceeds 59 chars, so the volume name takes the hash path.
	fam := newFamily(strings.Repeat("l", 36))
	m := imageModule(fam, strings.Repeat("9", 20))
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	p := admit(t, func() *corev1.Pod { return newPod(ns, m.Name) }, hasAnnotations(m.Status.Resolved.Annotations))
	if got := p.Spec.Volumes[0].Name; len(got) != 63 || got != resolve.VolumeName(m.Name) {
		t.Errorf("volume name %q", got)
	}
}

func TestModuleGuards(t *testing.T) {
	bad := imageModule(newFamily("guard"), "1")
	bad.Spec.Source.Image.Reference = "docker.io/evil/nccl" + digest()
	expectCreateError(t, bad, "is not in an allowed registry")

	expectCreateError(t, imageModule(newFamily("guard"), "1", withEnv("MY_VAR", "x")), "env.set name MY_VAR must start with one of")
}

func TestModuleOneDefaultPerFamily(t *testing.T) {
	fam := newFamily("onedef")
	create(t, imageModule(fam, "1", withDefault()))
	expectCreateError(t, imageModule(fam, "2", withDefault()), "is already the default for family")

	second := imageModule(fam, "3")
	create(t, second)
	eventually(t, 5*time.Second, func() error {
		cur := &v1.Module{}
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(second), cur); err != nil {
			return err
		}
		cur.Labels[v1.LabelDefault] = "true"
		err := k8s.Update(ctx, cur)
		if err == nil {
			return fmt.Errorf("label update to a second default was admitted")
		}
		if !strings.Contains(err.Error(), "is already the default for family") {
			return err
		}
		return nil
	})
}
```

- [ ] **Step 7: Run** — `make test-envtest`. Expected: PASS. If a policy fails to compile, `k8s.Create` in `installPolicies` returns the apiserver's CEL error. Fix the expression in the template and rerun. If a CEL type error comes from `{}` literals (`has(...) ? object.metadata.annotations : {}`), switch to `object.metadata.?annotations.orValue({})`.

- [ ] **Step 8: Commit** — `git add deploy/helm test/envtest && git commit -m "feat(chart): admission policies for pod injection, consistency and module guards"`

---

### Task 7: kind dev environment and stub module image

**Files:**
- Create: `deploy/kind/kind-config.yaml`, `deploy/kind/setup.sh`, `deploy/kind/teardown.sh`, `test/images/nccl-fake/Dockerfile`, `test/images/nccl-fake/VERSION`, `test/images/nccl-fake/lib/libnccl.so.2`

**Interfaces:**
- Produces: kind cluster `k8s-modules` (kube context `kind-k8s-modules`), registry `kind-registry:5000` (host `localhost:5001`), chart release `k8s-modules` in `module-system`, and Module `nccl-2.29.2` (env `NCCL_DEBUG=WARN`, `prependPath LD_LIBRARY_PATH: [lib]`). The Module's image reference is the single source of the nccl-fake digest for e2e.

- [ ] **Step 1: Stub image**

`test/images/nccl-fake/Dockerfile`:
```dockerfile
FROM scratch
COPY VERSION /VERSION
COPY lib/libnccl.so.2 /lib/libnccl.so.2
```
`VERSION`: `2.29.2`
`lib/libnccl.so.2`: `stub libnccl for e2e tests`

- [ ] **Step 2: kind-config.yaml**

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
featureGates:
  ImageVolume: true
nodes:
- role: control-plane
- role: worker
```

(kind ≥ 0.27 node images already set containerd `config_path = "/etc/containerd/certs.d"`, so setup.sh only writes a `hosts.toml`. Before relying on this, check it against kind's local-registry docs via ctx7.)

- [ ] **Step 3: setup.sh** (`chmod +x`)

```bash
#!/usr/bin/env bash
# Creates a kind cluster with a local registry, builds and pushes the
# controller and nccl-fake images, installs the chart and one Module.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
CLUSTER=${KIND_CLUSTER:-k8s-modules}
NODE_IMAGE=${KIND_NODE_IMAGE:-kindest/node:v1.36.4}
REG_NAME=kind-registry
REG_PORT=${REG_PORT:-5001}
BUILD_ARGS=${BUILD_ARGS:-}
if [[ -z "${CONTAINER_CLI:-}" ]]; then
  if command -v docker >/dev/null 2>&1; then CONTAINER_CLI=docker; else CONTAINER_CLI=podman; fi
fi
PUSH_FLAGS=()
if [[ "$CONTAINER_CLI" == podman ]]; then
  export KIND_EXPERIMENTAL_PROVIDER=podman
  PUSH_FLAGS=(--tls-verify=false)
fi

step() { printf '\n==> %s\n' "$*"; }

step "1. cluster and registry"
if ! $CONTAINER_CLI inspect "$REG_NAME" >/dev/null 2>&1; then
  $CONTAINER_CLI run -d --restart=always -p "127.0.0.1:${REG_PORT}:5000" --name "$REG_NAME" docker.io/library/registry:2
fi
if ! kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  kind create cluster --name "$CLUSTER" --image "$NODE_IMAGE" --config "$ROOT/deploy/kind/kind-config.yaml"
fi
$CONTAINER_CLI network connect kind "$REG_NAME" 2>/dev/null || true
for node in $(kind get nodes --name "$CLUSTER"); do
  $CONTAINER_CLI exec "$node" mkdir -p "/etc/containerd/certs.d/${REG_NAME}:5000"
  printf '[host."http://%s:5000"]\n' "$REG_NAME" |
    $CONTAINER_CLI exec -i "$node" tee "/etc/containerd/certs.d/${REG_NAME}:5000/hosts.toml" >/dev/null
done
kubectl config use-context "kind-$CLUSTER" >/dev/null

step "2. build and push images"
# shellcheck disable=SC2086
$CONTAINER_CLI build $BUILD_ARGS -t "localhost:${REG_PORT}/module-controller:dev" "$ROOT"
$CONTAINER_CLI push "${PUSH_FLAGS[@]}" "localhost:${REG_PORT}/module-controller:dev"
# shellcheck disable=SC2086
$CONTAINER_CLI build $BUILD_ARGS -t "localhost:${REG_PORT}/modules/nccl-fake:2.29.2" "$ROOT/test/images/nccl-fake"
$CONTAINER_CLI push "${PUSH_FLAGS[@]}" "localhost:${REG_PORT}/modules/nccl-fake:2.29.2"
DIGEST=$(curl -fsSI \
  -H 'Accept: application/vnd.oci.image.manifest.v1+json' \
  -H 'Accept: application/vnd.docker.distribution.manifest.v2+json' \
  "http://localhost:${REG_PORT}/v2/modules/nccl-fake/manifests/2.29.2" |
  awk -F': ' 'tolower($1)=="docker-content-digest"{print $2}' | tr -d '\r')
[[ "$DIGEST" == sha256:* ]] || { echo "could not read nccl-fake digest" >&2; exit 1; }

step "3. install chart"
helm upgrade --install k8s-modules "$ROOT/deploy/helm/k8s-modules" \
  --namespace module-system --create-namespace \
  --set image.repository="${REG_NAME}:5000/module-controller" \
  --set image.tag=dev --set image.pullPolicy=Always \
  --set "registryAllowlist={${REG_NAME}:5000/modules/}" \
  --wait --timeout 3m
kubectl -n module-system rollout restart deployment/module-controller
kubectl -n module-system rollout status deployment/module-controller --timeout=2m

step "4. create nccl-2.29.2"
kubectl apply -f - <<EOF
apiVersion: modules.brics.ac.uk/v1alpha1
kind: Module
metadata:
  name: nccl-2.29.2
  labels:
    modules.brics.ac.uk/family: nccl
    modules.brics.ac.uk/default: "true"
spec:
  family: nccl
  version: 2.29.2
  source:
    image:
      reference: ${REG_NAME}:5000/modules/nccl-fake@${DIGEST}
  env:
    prependPath:
      LD_LIBRARY_PATH: [lib]
    set:
      NCCL_DEBUG: WARN
EOF
kubectl wait module/nccl-2.29.2 --for=condition=Resolved --timeout=2m
echo "ready: kubectl --context kind-$CLUSTER get modules"
```

(Spec immutability means a rerun whose digest changed fails on `kubectl apply`. Run `make kind-down` first in that case.)

`teardown.sh`:
```bash
#!/usr/bin/env bash
set -euo pipefail
CLUSTER=${KIND_CLUSTER:-k8s-modules}
if [[ -z "${CONTAINER_CLI:-}" ]]; then
  if command -v docker >/dev/null 2>&1; then CONTAINER_CLI=docker; else CONTAINER_CLI=podman; fi
fi
[[ "$CONTAINER_CLI" == podman ]] && export KIND_EXPERIMENTAL_PROVIDER=podman
kind delete cluster --name "$CLUSTER"
$CONTAINER_CLI rm -f kind-registry >/dev/null 2>&1 || true
```

- [ ] **Step 4: Run** — `make kind-up`
Expected: ends with `ready:`. `kubectl get module nccl-2.29.2` shows `Resolved True`. This is the phase 1 gate check that the chart installs and MutatingAdmissionPolicy v1 is served. If `kindest/node:v1.36.4` can't be pulled, report it and do not substitute another version silently.

- [ ] **Step 5: Commit** — `git add deploy/kind test/images && git commit -m "feat(dev): kind environment with local registry and nccl-fake module image"`

---

### Task 8: kind e2e tests

**Files:**
- Create: `test/e2e/e2e_test.go`

**Interfaces:**
- Consumes: the Task 7 cluster (current kube context), Module `nccl-2.29.2`.

- [ ] **Step 1: e2e_test.go**

```go
//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

var ctx = context.Background()

func newClient(t *testing.T) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = v1.AddToScheme(s)
	c, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: s})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// ncclImage returns the digest-pinned nccl-fake reference setup.sh installed.
func ncclImage(t *testing.T, c client.Client) string {
	m := &v1.Module{}
	if err := c.Get(ctx, client.ObjectKey{Name: "nccl-2.29.2"}, m); err != nil {
		t.Fatalf("nccl-2.29.2 missing; run make kind-up: %v", err)
	}
	return m.Spec.Source.Image.Reference
}

func namespace(t *testing.T, c client.Client, name string) string {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	_ = c.Delete(ctx, ns)
	waitFor(t, 2*time.Minute, func() error {
		return c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})
	})
	t.Cleanup(func() { _ = c.Delete(ctx, ns) })
	return name
}

func module(t *testing.T, c client.Client, family, version, image string, deps ...string) *v1.Module {
	m := &v1.Module{
		ObjectMeta: metav1.ObjectMeta{Name: family + "-" + version},
		Spec: v1.ModuleSpec{Family: family, Version: version, DependsOn: deps,
			Source: v1.ModuleSource{Image: &v1.ImageSource{Reference: image}},
			Env:    &v1.ModuleEnv{PrependPath: map[string]v1.PathList{v1.EnvLDLibraryPath: {"lib"}}}},
	}
	_ = c.Delete(ctx, m)
	waitFor(t, 30*time.Second, func() error { return c.Create(ctx, m.DeepCopy()) })
	t.Cleanup(func() { _ = c.Delete(ctx, m) })
	return m
}

func waitFor(t *testing.T, d time.Duration, f func() error) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		err := f()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %v", err)
		}
		time.Sleep(time.Second)
	}
}

func waitReason(t *testing.T, c client.Client, name, reason string) {
	t.Helper()
	waitFor(t, time.Minute, func() error {
		m := &v1.Module{}
		if err := c.Get(ctx, client.ObjectKey{Name: name}, m); err != nil {
			return err
		}
		if cond := meta.FindStatusCondition(m.Status.Conditions, v1.ConditionResolved); cond == nil || cond.Reason != reason {
			return fmt.Errorf("%s Resolved condition = %+v, want reason %s", name, cond, reason)
		}
		return nil
	})
}

// runPod runs a shell script in busybox with the given modules loaded and
// waits for it to succeed.
func runPod(t *testing.T, c client.Client, ns, load, script string) {
	t.Helper()
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "check", Namespace: ns, Annotations: map[string]string{v1.AnnotationLoad: load}},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{
			Name: "check", Image: "docker.io/library/busybox:1.37", Command: []string{"sh", "-ec", script},
		}}},
	}
	waitFor(t, 30*time.Second, func() error { return c.Create(ctx, p) })
	waitFor(t, 3*time.Minute, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			return err
		}
		switch p.Status.Phase {
		case corev1.PodSucceeded:
			return nil
		case corev1.PodFailed:
			t.Fatalf("pod failed: %+v", p.Status.ContainerStatuses)
		}
		return fmt.Errorf("pod phase %s", p.Status.Phase)
	})
}

func expectRejected(t *testing.T, c client.Client, ns, load string, substrs ...string) {
	t.Helper()
	waitFor(t, 30*time.Second, func() error {
		p := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "reject-", Namespace: ns, Annotations: map[string]string{v1.AnnotationLoad: load}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "docker.io/library/busybox:1.37"}}},
		}
		err := c.Create(ctx, p)
		if err == nil {
			_ = c.Delete(ctx, p)
			return fmt.Errorf("pod loading %q was admitted", load)
		}
		for _, s := range substrs {
			if strings.Contains(err.Error(), s) {
				return nil
			}
		}
		return fmt.Errorf("error %q contains none of %q", err, substrs)
	})
}

func TestSingleModule(t *testing.T) {
	c := newClient(t)
	ns := namespace(t, c, "e2e-single")
	runPod(t, c, ns, "nccl-2.29.2", `
test -f /opt/modules/nccl/2.29.2/lib/libnccl.so.2
test "$(cat /opt/modules/nccl/2.29.2/VERSION)" = 2.29.2
test "$NCCL_DEBUG" = WARN
! touch /opt/modules/nccl/2.29.2/x 2>/dev/null`)
}

func TestFamilyDefault(t *testing.T) {
	c := newClient(t)
	ns := namespace(t, c, "e2e-default")
	runPod(t, c, ns, "nccl", `test -f /opt/modules/nccl/2.29.2/lib/libnccl.so.2`)
}

func TestDiamond(t *testing.T) {
	c := newClient(t)
	img := ncclImage(t, c)
	module(t, c, "e2e-left", "1", img, "nccl-2.29.2")
	module(t, c, "e2e-right", "1", img, "nccl-2.29.2")
	module(t, c, "e2e-top", "1", img, "e2e-left-1", "e2e-right-1")
	waitReason(t, c, "e2e-top-1", "Resolved")
	ns := namespace(t, c, "e2e-diamond")
	runPod(t, c, ns, "e2e-top-1", `
for m in nccl/2.29.2 e2e-left/1 e2e-right/1 e2e-top/1; do test -f /opt/modules/$m/lib/libnccl.so.2; done`)
}

func TestConflict(t *testing.T) {
	c := newClient(t)
	img := ncclImage(t, c)
	module(t, c, "nccl", "2.28.0", img)
	module(t, c, "e2e-both", "1", img, "nccl-2.29.2", "nccl-2.28.0")
	waitReason(t, c, "e2e-both-1", "Conflict")
	waitReason(t, c, "nccl-2.28.0", "Resolved")
	ns := namespace(t, c, "e2e-conflict")
	expectRejected(t, c, ns, "nccl-2.29.2,nccl-2.28.0", "requires resolved.modules.brics.ac.uk/nccl")
	expectRejected(t, c, ns, "e2e-both-1", "is not resolved", "was not loaded")
}

func TestCycle(t *testing.T) {
	c := newClient(t)
	img := ncclImage(t, c)
	module(t, c, "e2e-cyca", "1", img, "e2e-cycb-1")
	module(t, c, "e2e-cycb", "1", img, "e2e-cyca-1")
	waitReason(t, c, "e2e-cyca-1", "DependencyCycle")
	ns := namespace(t, c, "e2e-cycle")
	expectRejected(t, c, ns, "e2e-cyca-1", "dependency cycle", "was not loaded")
}
```

- [ ] **Step 2: Run** — `make test-e2e` (with the Task 7 cluster up)
Expected: all five tests PASS. This is the phase 1 and phase 2 kind gate.

- [ ] **Step 3: Full verification** — `make build test test-envtest helm-lint test-e2e` all pass.

- [ ] **Step 4: Commit** — `git add test/e2e && git commit -m "test(e2e): kind single, default, diamond, conflict and cycle scenarios"`

---

## Self-review notes

- **Spec coverage:**
  - CRD rules: Task 1 and Task 5.
  - Resolver: Tasks 2 and 3.
  - Controller, conditions table and PVC: Tasks 4 and 5.
  - MAP and the pods, consistency and guards policies: Task 6, including the full admission test list from the spec.
  - Helm values: Tasks 5 and 6.
  - kind: Task 7.
  - e2e: Task 8.
  - Gates: Task 7 Step 4 and Task 8 Step 3.
