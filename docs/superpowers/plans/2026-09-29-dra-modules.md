# Per-container Modules with DRA Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Every code block that creates a file is preceded by `<!-- file: <path> -->`; blocks without that marker are commands or excerpts.

**Goal:** Turn each Module into a shareable DRA device that containers request as an extended resource. This gives module selection per container and per module, and scheduling that knows which nodes can run a host module. `load` stays as a shortcut.

**Architecture:**
- **module-nri gains a DRA kubelet plugin (`internal/dra`).**
  - It publishes one `allowMultipleAllocations` device per Module whose closure can run on the node.
  - At prepare it writes a CDI spec with the binds, the module-hook and a `BRICS_MODULE_*` marker for each closure member.
- **The NRI plugin only reads markers.** It builds `LD_LIBRARY_PATH`, applies masks and sets `BRICS_MODULES`.
- **The controller adds a DeviceClass per Module and per family.**
- **Admission works per container.**
  - The mutating policy is driven by container limits, `load` and `load-containers`.
  - The validating policies also check limit keys.
  - A new policy stops users creating their own claims for module DeviceClasses.

**Tech Stack:**
- Go 1.26 (`CGO_ENABLED=0`).
- `k8s.io/dynamic-resource-allocation` v0.36.5 (`kubeletplugin`, `resourceslice`).
- `k8s.io/api/resource/v1`.
- containerd NRI v0.12.0.
- controller-runtime v0.24.1.
- Helm, ValidatingAdmissionPolicy and MutatingAdmissionPolicy (CEL).
- kind v1.36.4 with containerd 2.3.4 on podman.

**Spec:** `docs/superpowers/specs/2026-09-29-dra-modules-design.md`. The spike is in `spike/dra/README.md`.

## Global Constraints

- **Names:**
  - Driver and device-attribute domain: `modules.isambard.ac.uk`.
  - Exact resource: `modules.isambard.ac.uk/<module>`.
  - Family resource: `family.modules.isambard.ac.uk/<family>`.
  - Module DeviceClass: `<module>.modules.isambard.ac.uk`.
  - Family DeviceClass: `<family>.family.modules.isambard.ac.uk`.
  - DeviceClass label: `modules.isambard.ac.uk/managed: "true"`.
  - Device attributes: `module` (string), `family` (string), `default` (bool).
  - New annotation: `modules.isambard.ac.uk/load-containers`.
  - Marker env prefix: `BRICS_MODULE_`.
  - CDI kind: `modules.isambard.ac.uk/module`.
  - CDI file: `/var/run/cdi/modules.isambard.ac.uk-<claim uid>.json`.
- **Quantity:** module resources are always quantity `1`. The mutating policy writes `"1"` as a plain string, because `quantity("1")` is rejected.
- **Mount options:**
  - host file binds: `bind,ro,nosuid,nodev,rprivate`;
  - PVC binds: `rbind,ro,nosuid,nodev,rprivate`;
  - masks: tmpfs `ro,nosuid,nodev,noexec,size=0`.
- **Slices:** at most 64 devices per ResourceSlice. The pool name is the node name.
- **Build and toolchain:**
  - `CGO_ENABLED=0` for every Go command; the Makefile exports it.
  - podman only, with `KIND_EXPERIMENTAL_PROVIDER=podman`.
  - envtest 1.36.2 through `make test-envtest`.
- **Admission CEL:**
  - Native pod fields such as `c.resources.limits` produce "undefined field" type-check *warnings* on policy creation. These are harmless at runtime, so don't "fix" them.
  - `string(c.resources.limits[k]) == '1'` is how quantities are compared.
- **Commits:** end every commit message with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Driver restart between prepare and container create:** the CDI file is on disk, and a repeated prepare must write the same spec. Pinned by `TestPrepareIdempotent` (Task 5).
2. **Same module in two containers of one pod:** each container gets its own edits on its own request. Pinned by `TestPlanSameModuleTwoContainers` (Task 4).
3. **Module deleted after admission, before prepare:** prepare fails with a message naming the device, not a panic. Pinned by `TestPlanUnknownDevice` (Task 4).
4. **A marker the user forged with no module:** NRI does only env and masks, with no binds and no hook. Pinned by `TestAdjustForgedMarkerOnlyEnvAndMasks` (Task 2).
5. **`load` on a pod whose containers already have CPU limits:** the existing limits are kept. Pinned by `TestAdmissionLoadAddsLimits` (Task 8).

---

## File map

| Path | Responsibility | Task |
|---|---|---|
| `api/v1alpha1/module_types.go` | DRA constants; `AnnotationContainers` removed in Task 8 | 1, 8 |
| `internal/resolve/value.go` (moved from `internal/nri/value.go`) | `ModuleValue`, `ParseValue` | 1 |
| `internal/resolve/names.go` | `DeviceName`, `MarkerKey`, class and resource name helpers | 1 |
| `internal/nri/adjust.go` | marker-driven `Adjust(ctr)` | 2 |
| `internal/nri/plugin.go` | calls `Adjust` | 2 |
| `internal/dra/types.go` | `Catalogue`, `HostModules`, `PVCs` interfaces | 3 |
| `internal/dra/devices.go` | `Ready`, `Devices`, `Resources` | 3 |
| `internal/dra/plan.go` | per-container CDI edit planning | 4 |
| `internal/dra/cdi.go` | CDI spec writing and removal | 4 |
| `internal/dra/driver.go` | `kubeletplugin.DRAPlugin` implementation | 5 |
| `internal/dra/publisher.go` | debounced ResourceSlice publishing | 5 |
| `internal/nri/catalogue.go`, `mountinfo.go`, `report.go` | `List`, `Mounted`, `Discoverer.OnUpdate` | 6 |
| `cmd/module-nri/main.go` | starts the kubelet plugin and publisher | 6 |
| `internal/controller/deviceclass_controller.go` | DeviceClasses from Modules | 7 |
| `cmd/module-controller/main.go`, `test/envtest/suite_test.go` | register the reconciler | 7 |
| `deploy/helm/k8s-modules/templates/admission-{mutating,pods,pod-consistency}.yaml` | per-container admission | 8 |
| `deploy/helm/k8s-modules/templates/admission-resourceclaims.yaml` | claim guard | 9 |
| `deploy/helm/k8s-modules/templates/{nri-daemonset,nri-rbac,rbac}.yaml` | mounts, RBAC | 10 |
| `deploy/kind/setup.sh`, `test/images/host-libs/src/build.sh` | late library, discovery interval | 10 |
| `test/e2e/dra_test.go`, `test/e2e/nri_test.go` | e2e | 11 |
| `docs/writing-modules.md`, `deploy/helm/k8s-modules/README.md` | user docs | 12 |

---

### Task 1: Names, constants and `ParseValue` in `resolve`

**Files:**
- Modify: `api/v1alpha1/module_types.go` (const block)
- Move: `internal/nri/value.go` → `internal/resolve/value.go`
- Modify: `internal/resolve/names.go`
- Test: `internal/resolve/names_test.go`, `internal/resolve/value_test.go`
- Modify: `internal/nri/adjust.go`, `internal/nri/adjust_test.go`. These are the callers of `ParseValue`: switch them to `resolve.ParseValue` and move `TestParseValue` out.

**Interfaces:**
- Produces:
  - `v1.AnnotationLoadContainers`, `v1.DriverName`, `v1.ResourcePrefix`, `v1.FamilyResourcePrefix`, `v1.DeviceClassSuffix`, `v1.FamilyClassSuffix`, `v1.LabelManaged`, `v1.AttrModule`, `v1.AttrFamily`, `v1.AttrDefault`, `v1.EnvMarkerPrefix`, `v1.EnvModules`;
  - `resolve.ModuleValue{Name string; Rank int; Src, Mount string; Lib, Mask []string}` and `resolve.ParseValue(string) (ModuleValue, error)`;
  - `resolve.DeviceName(module string) string`, `resolve.MarkerKey(module string) string`, `resolve.DeviceClassName(module string) string`, `resolve.FamilyClassName(family string) string`, `resolve.ResourceName(module string) string`, `resolve.FamilyResourceName(family string) string`.

- [ ] **Step 1: Add the constants**

Append to the `const (...)` block in `api/v1alpha1/module_types.go`, after `LabelHostPrefix`:

```go
	AnnotationLoadContainers = "modules.isambard.ac.uk/load-containers"

	// DRA: every Module is a shareable device of this driver, requested as
	// ResourcePrefix+<module> or FamilyResourcePrefix+<family>.
	DriverName           = "modules.isambard.ac.uk"
	ResourcePrefix       = "modules.isambard.ac.uk/"
	FamilyResourcePrefix = "family.modules.isambard.ac.uk/"
	DeviceClassSuffix    = ".modules.isambard.ac.uk"
	FamilyClassSuffix    = ".family.modules.isambard.ac.uk"
	LabelManaged         = "modules.isambard.ac.uk/managed"
	AttrModule           = "module"
	AttrFamily           = "family"
	AttrDefault          = "default"

	// EnvMarkerPrefix starts the env var the DRA driver's CDI edits set for
	// each module a container gets; its value is the module's resolved
	// annotation value. EnvModules lists them for the workload.
	EnvMarkerPrefix = "BRICS_MODULE_"
	EnvModules      = "BRICS_MODULES"
```

- [ ] **Step 2: Move `value.go` into `resolve`**

```bash
git mv internal/nri/value.go internal/resolve/value.go
```

In `internal/resolve/value.go`:
- replace the package clause and package doc (the first 4 lines) with just `package resolve`;
- replace the `ModuleValue` comment with `// ModuleValue is one parsed resolved.modules.isambard.ac.uk/<family> value, the inverse of AnnotationValue.`

The `resolve` package doc stays in `names.go`.

Create `internal/resolve/value_test.go` holding `TestParseValue`, moved verbatim from `internal/nri/adjust_test.go`, with `package resolve` and imports `reflect`, `testing`. Delete `TestParseValue` from `internal/nri/adjust_test.go`.

In `internal/nri/adjust.go`, change `mods := make([]ModuleValue, 0, len(keys))` to `mods := make([]resolve.ModuleValue, 0, len(keys))` and `mv, err := ParseValue(v)` to `mv, err := resolve.ParseValue(v)`. Add the import `"github.com/isambard-sc/k8s-modules/internal/resolve"`. Task 2 rewrites this file; this step only keeps the build green.

- [ ] **Step 3: Write the failing name tests**

Append to `internal/resolve/names_test.go`:

```go
func TestDeviceName(t *testing.T) {
	if got := DeviceName("libfabric-host"); got != "libfabric-host" {
		t.Errorf("DeviceName(libfabric-host) = %q", got)
	}
	a, b := DeviceName("nccl-2.29.2"), DeviceName("nccl-2-29.2")
	if a == b {
		t.Errorf("lossy names must differ: %q", a)
	}
	if !strings.HasPrefix(a, "nccl-2-29-2-") || len(a) != len("nccl-2-29-2-")+8 {
		t.Errorf("DeviceName(nccl-2.29.2) = %q", a)
	}
	long := strings.Repeat("a", 30) + "." + strings.Repeat("b", 32)
	if got := DeviceName(long); len(validation.IsDNS1123Label(got)) != 0 {
		t.Errorf("DeviceName(%q) = %q is not a DNS label", long, got)
	}
}

func TestMarkerKey(t *testing.T) {
	if got := MarkerKey("libfabric-host"); got != "BRICS_MODULE_LIBFABRIC_HOST" {
		t.Errorf("MarkerKey(libfabric-host) = %q", got)
	}
	a, b := MarkerKey("nccl-2.29.2"), MarkerKey("nccl-2-29.2")
	if a == b || !strings.HasPrefix(a, "BRICS_MODULE_NCCL_2_29_2_") || len(a) != len("BRICS_MODULE_NCCL_2_29_2_")+8 {
		t.Errorf("MarkerKey: %q, %q", a, b)
	}
	for _, k := range []string{a, b} {
		for _, r := range k {
			if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
				t.Fatalf("MarkerKey %q has %q", k, r)
			}
		}
	}
}

func TestClassAndResourceNames(t *testing.T) {
	for got, want := range map[string]string{
		DeviceClassName("nccl-2.29.2"): "nccl-2.29.2.modules.isambard.ac.uk",
		FamilyClassName("nccl"):        "nccl.family.modules.isambard.ac.uk",
		ResourceName("nccl-2.29.2"):    "modules.isambard.ac.uk/nccl-2.29.2",
		FamilyResourceName("nccl"):     "family.modules.isambard.ac.uk/nccl",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
```

- [ ] **Step 4: Run them to verify they fail**

Run: `go test ./internal/resolve/ -run 'TestDeviceName|TestMarkerKey|TestClassAndResourceNames|TestParseValue' -count=1`
Expected: FAIL to compile, `undefined: DeviceName`.

- [ ] **Step 5: Implement the helpers**

In `internal/resolve/names.go`, replace `VolumeName` with:

```go
// VolumeName turns a Module name into a pod volume name (a DNS label).
func VolumeName(moduleName string) string { return dnsLabel("mod-", moduleName) }

// DeviceName turns a Module name into its DRA device name (a DNS label).
func DeviceName(moduleName string) string { return dnsLabel("", moduleName) }

// dnsLabel prefixes a sanitised name. Sanitising is lossy (nccl-2.29.2 and
// nccl-2-29.2 both become nccl-2-29-2), so any changed or overlong name gets
// a hash suffix.
func dnsLabel(prefix, name string) string {
	n := prefix + strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, strings.ToLower(name))
	if len(n) <= 63 && n == prefix+name {
		return n
	}
	return n[:min(len(n), 54)] + "-" + shortHash(name)
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// MarkerKey is the env var the DRA driver sets for a module. '-' maps
// reversibly to '_' (Module names have no '_'); anything else, such as a
// '.', is lossy and adds a hash suffix.
func MarkerKey(moduleName string) string {
	var b strings.Builder
	lossy := false
	for _, r := range moduleName {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-':
			b.WriteByte('_')
		default:
			lossy = true
			b.WriteByte('_')
		}
	}
	k := v1.EnvMarkerPrefix + b.String()
	if lossy {
		k += "_" + shortHash(moduleName)
	}
	return k
}

func DeviceClassName(moduleName string) string { return moduleName + v1.DeviceClassSuffix }
func FamilyClassName(family string) string     { return family + v1.FamilyClassSuffix }
func ResourceName(moduleName string) string    { return v1.ResourcePrefix + moduleName }
func FamilyResourceName(family string) string  { return v1.FamilyResourcePrefix + family }
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/... -count=1`
Expected: PASS. `TestVolumeName` still passes, because `dnsLabel` computes the same hash suffix.

- [ ] **Step 7: Commit**

```bash
git add api/v1alpha1/module_types.go internal/resolve internal/nri
git commit -m "refactor(resolve): DRA names, marker keys and ParseValue

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: NRI reads markers

**Files:**
- Modify: `internal/nri/adjust.go` (full rewrite)
- Modify: `internal/nri/plugin.go`
- Test: `internal/nri/adjust_test.go` (full rewrite, keeping `TestPrependLibraryPath`)

**Interfaces:**
- Consumes: `resolve.ParseValue`, `resolve.MarkerKey`, `resolve.AnnotationValue`, `v1.EnvMarkerPrefix`, `v1.EnvModules` (Task 1).
- Produces: `nri.Adjust(ctr *api.Container) (*api.ContainerAdjustment, error)`. `nri.Plugin{Log logr.Logger}` has no `Adjuster` field.
- Removes: `nri.Adjuster`, and the `nri.Catalogue`, `nri.HostModules` and `nri.PVCs` interfaces. `internal/dra` defines its own in Task 3. `cmd/module-nri` stops compiling until Task 6. That is expected: run only `./internal/...` until then.

- [ ] **Step 1: Write the failing tests**

<!-- file: internal/nri/adjust_test.go -->
```go
package nri

import (
	"reflect"
	"strings"
	"testing"

	api "github.com/containerd/nri/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

// marker is the env entry the DRA driver's CDI edit sets for a module.
func marker(family, version string, rank int, mutate ...func(*v1.Module)) string {
	m := &v1.Module{ObjectMeta: metav1.ObjectMeta{Name: family + "-" + version},
		Spec: v1.ModuleSpec{Family: family, Version: version,
			Source: v1.ModuleSource{Image: &v1.ImageSource{Reference: "r/x@sha256:" + strings.Repeat("a", 64)}}}}
	for _, f := range mutate {
		f(m)
	}
	return resolve.MarkerKey(m.Name) + "=" + resolve.AnnotationValue(m, rank)
}

func lib(p ...string) func(*v1.Module) {
	return func(m *v1.Module) {
		m.Spec.Env = &v1.ModuleEnv{PrependPath: map[string]v1.PathList{v1.EnvLDLibraryPath: p}}
	}
}

func mask(p ...string) func(*v1.Module) { return func(m *v1.Module) { m.Spec.MaskPaths = p } }

func ctr(name string, env ...string) *api.Container { return &api.Container{Name: name, Env: env} }

func envOf(adj *api.ContainerAdjustment) map[string]string {
	m := map[string]string{}
	for _, kv := range adj.GetEnv() {
		m[kv.Key] = kv.Value
	}
	return m
}

func TestAdjustOrdersByRank(t *testing.T) {
	adj, err := Adjust(ctr("main",
		"LD_LIBRARY_PATH=/usr/local/cuda/lib64:/opt/modules/nccl/2/lib",
		marker("nccl", "2", 10, lib("lib")),
		marker("ofi", "1", 20, lib("lib")),
	))
	if err != nil {
		t.Fatal(err)
	}
	env := envOf(adj)
	if want := "/opt/modules/ofi/1/lib:/opt/modules/nccl/2/lib:/usr/local/cuda/lib64"; env["LD_LIBRARY_PATH"] != want {
		t.Errorf("LD_LIBRARY_PATH = %q\nwant %q", env["LD_LIBRARY_PATH"], want)
	}
	if env[v1.EnvModules] != "ofi-1:nccl-2" {
		t.Errorf("BRICS_MODULES = %q", env[v1.EnvModules])
	}
}

func TestAdjustMasks(t *testing.T) {
	adj, err := Adjust(ctr("main", marker("ofi", "1", 10, mask("/opt/amazon"))))
	if err != nil {
		t.Fatal(err)
	}
	ms := adj.GetMounts()
	if len(ms) != 1 || ms[0].Destination != "/opt/amazon" || ms[0].Type != "tmpfs" ||
		!reflect.DeepEqual(ms[0].Options, []string{"ro", "nosuid", "nodev", "noexec", "size=0"}) {
		t.Errorf("mounts = %+v", ms)
	}
	if _, ok := envOf(adj)["LD_LIBRARY_PATH"]; ok {
		t.Error("no lib dirs: LD_LIBRARY_PATH must not be set")
	}
}

// Markers are untrusted: a forged one may only change the container's own
// env and mask its own paths, never bind host files or add a hook.
func TestAdjustForgedMarkerOnlyEnvAndMasks(t *testing.T) {
	forged := "BRICS_MODULE_EVIL=evil-1;rank=10;src=host;mount=/opt/modules/evil/1;lib=/opt/modules/evil/1/lib;mask=/etc/x"
	adj, err := Adjust(ctr("main", forged))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range adj.GetMounts() {
		if m.Type != "tmpfs" {
			t.Errorf("non-mask mount %+v", m)
		}
	}
	if len(adj.GetHooks().GetCreateContainer()) != 0 {
		t.Error("NRI must never add hooks")
	}
}

func TestAdjustDuplicateMarkersKeepHighestRank(t *testing.T) {
	a := marker("nccl", "2", 10, lib("lib"))
	b := resolve.MarkerKey("nccl-2") + "2=" + strings.SplitN(marker("nccl", "2", 30, lib("lib")), "=", 2)[1]
	adj, err := Adjust(ctr("main", a, b, marker("ofi", "1", 20, lib("lib"))))
	if err != nil {
		t.Fatal(err)
	}
	if got := envOf(adj)[v1.EnvModules]; got != "nccl-2:ofi-1" {
		t.Errorf("BRICS_MODULES = %q", got)
	}
}

func TestAdjustNoMarkers(t *testing.T) {
	adj, err := Adjust(ctr("main", "PATH=/bin", "BRICS_MODULES=stale"))
	if adj != nil || err != nil {
		t.Errorf("no markers: %v %v", adj, err)
	}
}

func TestAdjustMalformedMarker(t *testing.T) {
	_, err := Adjust(ctr("main", "BRICS_MODULE_X=x-1;rank=oops"))
	if err == nil || !strings.Contains(err.Error(), "BRICS_MODULE_X") {
		t.Errorf("err = %v", err)
	}
}

func TestAdjustDeterministic(t *testing.T) {
	c := ctr("main", marker("a", "1", 10, lib("l")), marker("b", "1", 10, lib("l")), marker("c", "1", 20, mask("/m")))
	first, _ := Adjust(c)
	for range 20 {
		again, _ := Adjust(c)
		if !reflect.DeepEqual(first, again) {
			t.Fatal("Adjust is not deterministic")
		}
	}
}

func TestPrependLibraryPath(t *testing.T) {
	cases := []struct {
		env  []string
		dirs []string
		want string
	}{
		{nil, []string{"/m/lib"}, "/m/lib"},
		{[]string{"LD_LIBRARY_PATH=/a:/m/lib:/b"}, []string{"/m/lib", "/n/lib"}, "/m/lib:/n/lib:/a:/b"},
		{[]string{"LD_LIBRARY_PATH=/first", "LD_LIBRARY_PATH=/second"}, []string{"/m"}, "/m:/first"},
		{[]string{"LD_LIBRARY_PATH=::/a:"}, []string{"/m", "/m", ""}, "/m:/a"},
	}
	for _, c := range cases {
		if got := PrependLibraryPath(c.env, c.dirs); got != c.want {
			t.Errorf("PrependLibraryPath(%v, %v) = %q, want %q", c.env, c.dirs, got, c.want)
		}
	}
}
```

In `TestAdjustDuplicateMarkersKeepHighestRank`, the second marker for `nccl-2` has rank 30 under a different key: the driver can emit the same module once per request. The rank-30 entry wins, so `nccl-2` (30) sorts before `ofi-1` (20).

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/nri/ -count=1`
Expected: FAIL to compile, `undefined: Adjust` (the old code has `Adjuster.Adjust`).

- [ ] **Step 3: Rewrite `adjust.go`**

<!-- file: internal/nri/adjust.go -->
```go
package nri

import (
	"fmt"
	"sort"
	"strings"

	api "github.com/containerd/nri/pkg/api"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

var maskOptions = []string{"ro", "nosuid", "nodev", "noexec", "size=0"}

// Adjust applies the modules named by a container's BRICS_MODULE_* markers,
// which the DRA driver's CDI edits set, and returns nil for containers
// without markers. A user can set markers too, so Adjust only does what a
// container may do to itself: order LD_LIBRARY_PATH, mask paths and set
// BRICS_MODULES. Host and PVC binds and module-hook come only from CDI. A
// malformed marker fails container creation.
func Adjust(ctr *api.Container) (*api.ContainerAdjustment, error) {
	byName := map[string]resolve.ModuleValue{}
	for _, e := range ctr.GetEnv() {
		k, v, ok := strings.Cut(e, "=")
		if !ok || !strings.HasPrefix(k, v1.EnvMarkerPrefix) {
			continue
		}
		mv, err := resolve.ParseValue(v)
		if err != nil {
			return nil, fmt.Errorf("marker %s: %w", k, err)
		}
		if cur, ok := byName[mv.Name]; !ok || mv.Rank > cur.Rank {
			byName[mv.Name] = mv
		}
	}
	if len(byName) == 0 {
		return nil, nil
	}
	mods := make([]resolve.ModuleValue, 0, len(byName))
	for _, mv := range byName {
		mods = append(mods, mv)
	}
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].Rank != mods[j].Rank {
			return mods[i].Rank > mods[j].Rank
		}
		return mods[i].Name < mods[j].Name
	})

	adj := &api.ContainerAdjustment{}
	var libDirs, names []string
	for _, m := range mods {
		names = append(names, m.Name)
		libDirs = append(libDirs, m.Lib...)
		for _, p := range m.Mask {
			adj.AddMount(&api.Mount{Destination: p, Type: "tmpfs", Source: "tmpfs", Options: maskOptions})
		}
	}
	if len(libDirs) > 0 {
		adj.AddEnv(v1.EnvLDLibraryPath, PrependLibraryPath(ctr.GetEnv(), libDirs))
	}
	adj.AddEnv(v1.EnvModules, strings.Join(names, ":"))
	return adj, nil
}
```

- [ ] **Step 4: Update `plugin.go`**

In `internal/nri/plugin.go`, remove the `Adjuster *Adjuster` field from `Plugin`. Change `adj, err := p.Adjuster.Adjust(pod, ctr)` to `adj, err := Adjust(ctr)`, and `envValue(adj, "BRICS_MODULES")` to `envValue(adj, v1.EnvModules)`. Add the import `v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"`. Update the package doc in `internal/nri/report.go` or wherever `// Package nri` now lives: `value.go` moved in Task 1, so add the package doc at the top of `adjust.go`:

```go
// Package nri is the module system's NRI plugin: at container creation it
// applies the modules the DRA driver's CDI markers name, and it runs
// host-module discovery.
package nri
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/nri
git commit -m "feat(nri)!: apply modules from CDI markers, not pod annotations

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: `internal/dra` devices and slices

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/dra/types.go`, `internal/dra/devices.go`
- Test: `internal/dra/devices_test.go`, `internal/dra/fakes_test.go`

**Interfaces:**
- Consumes: `resolve.DeviceName`, `resolve.Resolve`, `v1.Attr*`, `v1.LabelDefault` (Task 1).
- Produces:
  - `dra.Catalogue interface { Module(name string) (*v1.Module, bool); List() []v1.Module }`;
  - `dra.HostModules interface { HostModule(name string) (discovery.Result, bool) }`;
  - `dra.PVCs interface { HostPath(claim, subPath string) (string, error); Mounted(claim string) bool }`;
  - `dra.Ready(m *v1.Module, cat Catalogue, hosts HostModules, pvcs PVCs) (bool, string)`;
  - `dra.Devices(cat Catalogue, hosts HostModules, pvcs PVCs) []resourceapi.Device`;
  - `dra.Resources(pool string, devs []resourceapi.Device) resourceslice.DriverResources`;
  - `dra.MaxDevicesPerSlice = 64`.

- [ ] **Step 1: Add the dependency**

Run: `go get k8s.io/dynamic-resource-allocation@v0.36.5 && go mod tidy`
Expected: `go.mod` gains `k8s.io/dynamic-resource-allocation v0.36.5`, and `k8s.io/client-go` moves to v0.36.5.

- [ ] **Step 2: Write the fakes and the failing tests**

<!-- file: internal/dra/fakes_test.go -->
```go
package dra

import (
	"errors"
	"path"
	"sort"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/discovery"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

type fakeCatalogue map[string]*v1.Module

func (c fakeCatalogue) Module(n string) (*v1.Module, bool) { m, ok := c[n]; return m, ok }

func (c fakeCatalogue) List() []v1.Module {
	var out []v1.Module
	for _, m := range c {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type fakeHosts map[string]discovery.Result

func (h fakeHosts) HostModule(n string) (discovery.Result, bool) { r, ok := h[n]; return r, ok }

type fakePVCs map[string]string

func (p fakePVCs) HostPath(c, sub string) (string, error) {
	if hp, ok := p[c]; ok {
		return path.Join(hp, sub), nil
	}
	return "", errors.New("pvc claim " + c + " is not mounted by module-nri; add it to nri.pvcs")
}

func (p fakePVCs) Mounted(c string) bool { _, ok := p[c]; return ok }

var (
	img = v1.ModuleSource{Image: &v1.ImageSource{Reference: "r/x@sha256:" + strings.Repeat("a", 64)}}
	hst = v1.ModuleSource{Host: &v1.HostSource{Discover: v1.HostDiscover{Roots: []string{"libfab.so.1"}}}}
	pvc = v1.ModuleSource{PVC: &v1.PVCSource{ClaimName: "spack-apps", SubPath: "nccl/1"}}
)

func mod(family, version string, src v1.ModuleSource, deps ...string) *v1.Module {
	return &v1.Module{ObjectMeta: metav1.ObjectMeta{Name: family + "-" + version, Labels: map[string]string{}},
		Spec: v1.ModuleSpec{Family: family, Version: version, Source: src, DependsOn: deps}}
}

func withLib(m *v1.Module) *v1.Module {
	m.Spec.Env = &v1.ModuleEnv{PrependPath: map[string]v1.PathList{v1.EnvLDLibraryPath: {"lib"}}}
	return m
}

// catalogue resolves every module so status is the real format.
func catalogue(t *testing.T, ms ...*v1.Module) fakeCatalogue {
	t.Helper()
	c := fakeCatalogue{}
	for _, m := range ms {
		c[m.Name] = m
	}
	for _, m := range ms {
		r, err := resolve.Resolve(m.Name, map[string]*v1.Module(c))
		if err != nil {
			t.Fatal(err)
		}
		m.Status.Resolved = r
	}
	return c
}

func hostResult() discovery.Result {
	return discovery.Result{Ready: true, GlibcRequired: "2.36",
		Lib:  []discovery.Library{{Soname: "libfab.so.1", Path: "/usr/lib64/libfab.so.1.2"}},
		Deps: []discovery.Library{{Soname: "libdep.so.1", Path: "/usr/lib64/libdep.so.1.0"}}}
}
```

<!-- file: internal/dra/devices_test.go -->
```go
package dra

import (
	"fmt"
	"testing"

	resourceapi "k8s.io/api/resource/v1"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/discovery"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

func names(devs []resourceapi.Device) []string {
	var out []string
	for _, d := range devs {
		out = append(out, *d.Attributes[v1.AttrModule].StringValue)
	}
	return out
}

func TestDevicesReadiness(t *testing.T) {
	fab := mod("fab", "host", hst)
	top := mod("ofi", "1", img, "fab-host")
	sp := mod("sp", "1", pvc)
	other := mod("other", "1", v1.ModuleSource{PVC: &v1.PVCSource{ClaimName: "unmounted", SubPath: "x"}})
	c := catalogue(t, fab, top, sp, other)
	unresolved := mod("new", "1", img)
	c[unresolved.Name] = unresolved

	cases := map[string]struct {
		hosts fakeHosts
		want  string
	}{
		"host ready":     {fakeHosts{"fab-host": hostResult()}, "[fab-host ofi-1 sp-1]"},
		"host not ready": {fakeHosts{"fab-host": discovery.Result{Message: "missing"}}, "[sp-1]"},
		"not discovered": {fakeHosts{}, "[sp-1]"},
	}
	for name, tc := range cases {
		got := fmt.Sprint(names(Devices(c, tc.hosts, fakePVCs{"spack-apps": "/pv"})))
		if got != tc.want {
			t.Errorf("%s: devices %s, want %s", name, got, tc.want)
		}
	}
}

func TestDeviceShape(t *testing.T) {
	m := mod("nccl", "2.29.2", img)
	m.Labels[v1.LabelDefault] = "true"
	devs := Devices(catalogue(t, m), fakeHosts{}, fakePVCs{})
	if len(devs) != 1 {
		t.Fatalf("devices = %+v", devs)
	}
	d := devs[0]
	if d.Name != resolve.DeviceName("nccl-2.29.2") || d.AllowMultipleAllocations == nil || !*d.AllowMultipleAllocations {
		t.Errorf("device = %+v", d)
	}
	if *d.Attributes[v1.AttrFamily].StringValue != "nccl" || !*d.Attributes[v1.AttrDefault].BoolValue {
		t.Errorf("attributes = %+v", d.Attributes)
	}
}

func TestReadyReason(t *testing.T) {
	c := catalogue(t, mod("fab", "host", hst))
	ok, why := Ready(c["fab-host"], c, fakeHosts{}, fakePVCs{})
	if ok || why != "host module fab-host is not ready on this node" {
		t.Errorf("Ready = %v %q", ok, why)
	}
}

func TestResourcesChunks(t *testing.T) {
	devs := make([]resourceapi.Device, 130)
	for i := range devs {
		devs[i].Name = fmt.Sprintf("d%d", i)
	}
	r := Resources("node-1", devs)
	p, ok := r.Pools["node-1"]
	if !ok || len(p.Slices) != 3 || len(p.Slices[0].Devices) != 64 || len(p.Slices[2].Devices) != 2 {
		t.Errorf("pools = %+v", r.Pools)
	}
	if empty := Resources("n", nil); len(empty.Pools["n"].Slices) != 1 || len(empty.Pools["n"].Slices[0].Devices) != 0 {
		t.Errorf("no devices: want one empty slice, got %+v", empty.Pools)
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./internal/dra/ -count=1`
Expected: FAIL to compile, `undefined: Devices`.

- [ ] **Step 4: Implement it**

<!-- file: internal/dra/types.go -->
```go
// Package dra is the module system's DRA kubelet plugin. Every Module is a
// shareable device that containers request as an extended resource; at
// prepare the plugin writes CDI edits that bind host and PVC files, add
// module-hook and set a BRICS_MODULE_* marker for each module.
package dra

import (
	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/discovery"
)

// Catalogue reads Modules, normally from an informer cache.
type Catalogue interface {
	Module(name string) (*v1.Module, bool)
	List() []v1.Module
}

// HostModules returns this node's latest discovery result for a module.
type HostModules interface {
	HostModule(name string) (discovery.Result, bool)
}

// PVCs locates files inside the claims module-nri mounts.
type PVCs interface {
	HostPath(claim, subPath string) (string, error)
	Mounted(claim string) bool
}
```

<!-- file: internal/dra/devices.go -->
```go
package dra

import (
	"sort"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	"k8s.io/utils/ptr"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

// MaxDevicesPerSlice keeps slices well under the API limit.
const MaxDevicesPerSlice = 64

// Ready reports whether every member of m's closure can run on this node,
// and if not, why.
func Ready(m *v1.Module, cat Catalogue, hosts HostModules, pvcs PVCs) (bool, string) {
	if m.Status.Resolved == nil {
		return false, "module " + m.Name + " is not resolved"
	}
	for _, n := range m.Status.Resolved.Modules {
		dep, ok := cat.Module(n)
		if !ok {
			return false, "module " + n + " is not in the catalogue"
		}
		switch src := dep.Spec.Source; {
		case src.Host != nil:
			if r, ok := hosts.HostModule(n); !ok || !r.Ready {
				return false, "host module " + n + " is not ready on this node"
			}
		case src.PVC != nil:
			if !pvcs.Mounted(src.PVC.ClaimName) {
				return false, "pvc claim " + src.PVC.ClaimName + " is not mounted by module-nri"
			}
		}
	}
	return true, ""
}

// Devices lists one shareable device per Module that is Ready, sorted by
// name, so the scheduler only places pods where their modules can run.
func Devices(cat Catalogue, hosts HostModules, pvcs PVCs) []resourceapi.Device {
	var out []resourceapi.Device
	for _, m := range cat.List() {
		if ok, _ := Ready(&m, cat, hosts, pvcs); !ok {
			continue
		}
		out = append(out, resourceapi.Device{
			Name:                     resolve.DeviceName(m.Name),
			AllowMultipleAllocations: ptr.To(true),
			Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				v1.AttrModule:  {StringValue: ptr.To(m.Name)},
				v1.AttrFamily:  {StringValue: ptr.To(m.Spec.Family)},
				v1.AttrDefault: {BoolValue: ptr.To(m.Labels[v1.LabelDefault] == "true")},
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Resources puts devs in one pool, split into slices of MaxDevicesPerSlice.
func Resources(pool string, devs []resourceapi.Device) resourceslice.DriverResources {
	var ss []resourceslice.Slice
	for len(devs) > MaxDevicesPerSlice {
		ss = append(ss, resourceslice.Slice{Devices: devs[:MaxDevicesPerSlice]})
		devs = devs[MaxDevicesPerSlice:]
	}
	ss = append(ss, resourceslice.Slice{Devices: devs})
	return resourceslice.DriverResources{Pools: map[string]resourceslice.Pool{pool: {Slices: ss}}}
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/dra/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/dra
git commit -m "feat(dra): publish one shareable device per ready Module

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Per-container CDI edits

**Files:**
- Create: `internal/dra/plan.go`, `internal/dra/cdi.go`
- Test: `internal/dra/plan_test.go`, `internal/dra/cdi_test.go`

**Interfaces:**
- Consumes: Task 3's interfaces and fakes; `resolve.ParseValue`, `resolve.MarkerKey`, `resolve.ResolvedAnnotationKey`, `resolve.DeviceName`, `resolve.FamilyResourceName`, `resolve.ResourceName`.
- Produces:
  - `dra.Mount{HostPath, ContainerPath, Type string; Options []string}`;
  - `dra.Hook{HookName, Path string; Args []string}`;
  - `dra.ContainerEdits{Env []string; Mounts []Mount; Hooks []Hook}`;
  - `dra.Planner{Catalogue; Hosts; PVCs; NodeName, HookPath string}` with `(*Planner).Plan(claim *resourceapi.ResourceClaim, pod *corev1.Pod) (map[string]*ContainerEdits, error)`. The map key is the request name; `nil` means the request gets no CDI device;
  - `dra.CDIKind`, `dra.CDIDeviceID(claim types.UID, request string) string`, `dra.SpecPath(dir string, claim types.UID) string`, `dra.WriteSpec(dir string, claim types.UID, edits map[string]*ContainerEdits) error`, `dra.RemoveSpec(dir string, claim types.UID) error`.

- [ ] **Step 1: Write the failing plan tests**

<!-- file: internal/dra/plan_test.go -->
```go
package dra

import (
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

// req is one request of the scheduler's claim: container, resource, device module.
type req struct{ container, resource, module string }

// claimAndPod builds an allocated claim and its pod, as the scheduler and
// admission leave them. Request names follow the scheduler's form.
func claimAndPod(c fakeCatalogue, reqs ...req) (*resourceapi.ResourceClaim, *corev1.Pod) {
	claim := &resourceapi.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: "p-extended-resources-x", Namespace: "ns", UID: "cuid"},
		Status: resourceapi.ResourceClaimStatus{Allocation: &resourceapi.AllocationResult{}}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", Annotations: map[string]string{}},
		Status: corev1.PodStatus{ExtendedResourceClaimStatus: &corev1.PodExtendedResourceClaimStatus{ResourceClaimName: claim.Name}}}
	perContainer := map[string]int{}
	containerIdx := map[string]int{}
	for _, r := range reqs {
		if _, ok := containerIdx[r.container]; !ok {
			containerIdx[r.container] = len(containerIdx)
		}
		name := "container-" + itoa(containerIdx[r.container]) + "-request-" + itoa(perContainer[r.container])
		perContainer[r.container]++
		claim.Status.Allocation.Devices.Results = append(claim.Status.Allocation.Devices.Results, resourceapi.DeviceRequestAllocationResult{
			Request: name, Driver: v1.DriverName, Pool: "n1", Device: resolve.DeviceName(r.module)})
		pod.Status.ExtendedResourceClaimStatus.RequestMappings = append(pod.Status.ExtendedResourceClaimStatus.RequestMappings,
			corev1.ContainerExtendedResourceRequest{ContainerName: r.container, ResourceName: r.resource, RequestName: name})
		for k, v := range c[r.module].Status.Resolved.Annotations {
			pod.Annotations[k] = v
		}
	}
	return claim, pod
}

func itoa(i int) string { return string(rune('0' + i)) }

func planner(c fakeCatalogue, h fakeHosts) *Planner {
	return &Planner{Catalogue: c, Hosts: h, PVCs: fakePVCs{"spack-apps": "/var/lib/pv"}, NodeName: "n1", HookPath: "/opt/brics-modules/bin/module-hook"}
}

func envMap(e *ContainerEdits) map[string]string {
	m := map[string]string{}
	for _, kv := range e.Env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func mountAt(e *ContainerEdits, dst string) *Mount {
	for i := range e.Mounts {
		if e.Mounts[i].ContainerPath == dst {
			return &e.Mounts[i]
		}
	}
	return nil
}

func TestPlanHostAndImageClosure(t *testing.T) {
	nccl := withLib(mod("nccl", "2", img))
	fab := mod("fab", "host", hst)
	top := withLib(mod("ofi", "1", img, "nccl-2", "fab-host"))
	c := catalogue(t, nccl, fab, top)
	claim, pod := claimAndPod(c, req{"main", resolve.ResourceName("ofi-1"), "ofi-1"})

	out, err := planner(c, fakeHosts{"fab-host": hostResult()}).Plan(claim, pod)
	if err != nil {
		t.Fatal(err)
	}
	e := out["container-0-request-0"]
	if e == nil {
		t.Fatalf("plan = %+v", out)
	}
	env := envMap(e)
	for _, n := range []string{"ofi-1", "nccl-2", "fab-host"} {
		if !strings.HasPrefix(env[resolve.MarkerKey(n)], n+";") {
			t.Errorf("marker for %s = %q", n, env[resolve.MarkerKey(n)])
		}
	}
	if m := mountAt(e, "/opt/modules/fab/host/lib/libfab.so.1"); m == nil || m.HostPath != "/usr/lib64/libfab.so.1.2" || m.Type != "bind" ||
		!reflect.DeepEqual(m.Options, []string{"bind", "ro", "nosuid", "nodev", "rprivate"}) {
		t.Errorf("lib bind = %+v", m)
	}
	if m := mountAt(e, "/opt/modules/fab/host/deps/libdep.so.1"); m == nil || m.HostPath != "/usr/lib64/libdep.so.1.0" {
		t.Errorf("deps bind = %+v", m)
	}
	want := []Hook{{HookName: "createContainer", Path: "/opt/brics-modules/bin/module-hook",
		Args: []string{"module-hook", "--deps", "/opt/modules/fab/host/deps", "--glibc", "fab-host=2.36"}}}
	if !reflect.DeepEqual(e.Hooks, want) {
		t.Errorf("hooks = %+v", e.Hooks)
	}
}

func TestPlanPVC(t *testing.T) {
	c := catalogue(t, mod("sp", "1", pvc))
	claim, pod := claimAndPod(c, req{"main", resolve.ResourceName("sp-1"), "sp-1"})
	out, err := planner(c, nil).Plan(claim, pod)
	if err != nil {
		t.Fatal(err)
	}
	m := mountAt(out["container-0-request-0"], "/opt/modules/sp/1")
	if m == nil || m.HostPath != "/var/lib/pv/nccl/1" || !reflect.DeepEqual(m.Options, []string{"rbind", "ro", "nosuid", "nodev", "rprivate"}) {
		t.Errorf("pvc bind = %+v", m)
	}
}

// A container requesting a module and one of its dependencies gets each
// file once, all on its first request.
func TestPlanOverlappingClosures(t *testing.T) {
	fab := mod("fab", "host", hst)
	top := mod("user", "1", img, "fab-host")
	c := catalogue(t, fab, top)
	claim, pod := claimAndPod(c,
		req{"main", resolve.ResourceName("user-1"), "user-1"},
		req{"main", resolve.ResourceName("fab-host"), "fab-host"})
	out, err := planner(c, fakeHosts{"fab-host": hostResult()}).Plan(claim, pod)
	if err != nil {
		t.Fatal(err)
	}
	first, second := out["container-0-request-0"], out["container-0-request-1"]
	if first == nil || second != nil {
		t.Fatalf("edits must all be on the first request: %+v", out)
	}
	seen := map[string]bool{}
	for _, m := range first.Mounts {
		if seen[m.ContainerPath] {
			t.Errorf("duplicate mount %s", m.ContainerPath)
		}
		seen[m.ContainerPath] = true
	}
	if len(first.Hooks) != 1 {
		t.Errorf("hooks = %+v", first.Hooks)
	}
	// The dependency's marker keeps its rank inside user-1's closure (10),
	// and user-1 (20) comes first.
	if first.Env[0] != resolve.MarkerKey("user-1")+"="+c["user-1"].Status.Resolved.Annotations[resolve.ResolvedAnnotationKey("user")] {
		t.Errorf("env = %v", first.Env)
	}
}

func TestPlanSameModuleTwoContainers(t *testing.T) {
	c := catalogue(t, withLib(mod("nccl", "2", img)))
	claim, pod := claimAndPod(c,
		req{"a", resolve.ResourceName("nccl-2"), "nccl-2"},
		req{"b", resolve.ResourceName("nccl-2"), "nccl-2"})
	out, err := planner(c, nil).Plan(claim, pod)
	if err != nil {
		t.Fatal(err)
	}
	if out["container-0-request-0"] == nil || out["container-1-request-0"] == nil {
		t.Errorf("each container needs its own edits: %+v", out)
	}
}

func TestPlanFamilyDefaultMoved(t *testing.T) {
	old, cur := mod("nccl", "1", img), mod("nccl", "2", img)
	c := catalogue(t, old, cur)
	claim, pod := claimAndPod(c, req{"main", resolve.FamilyResourceName("nccl"), "nccl-2"})
	// Admission picked nccl-1; the family class then allocated nccl-2.
	pod.Annotations[resolve.ResolvedAnnotationKey("nccl")] = old.Status.Resolved.Annotations[resolve.ResolvedAnnotationKey("nccl")]
	_, err := planner(c, nil).Plan(claim, pod)
	if err == nil || err.Error() != "default for family nccl changed from nccl-1 to nccl-2 after admission; recreate the pod" {
		t.Errorf("err = %v", err)
	}
}

func TestPlanUnknownDevice(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	claim, pod := claimAndPod(c, req{"main", resolve.ResourceName("x-1"), "x-1"})
	delete(c, "x-1")
	_, err := planner(c, nil).Plan(claim, pod)
	if err == nil || !strings.Contains(err.Error(), "device "+resolve.DeviceName("x-1")+" is not a resolved Module") {
		t.Errorf("err = %v", err)
	}
}

func TestPlanHostNotReady(t *testing.T) {
	c := catalogue(t, mod("fab", "host", hst))
	claim, pod := claimAndPod(c, req{"main", resolve.ResourceName("fab-host"), "fab-host"})
	_, err := planner(c, fakeHosts{"fab-host": {Message: "root libfab.so.1 not in /etc/ld.so.cache"}}).Plan(claim, pod)
	if err == nil || !strings.Contains(err.Error(), "host module fab-host is not available on node n1: root libfab.so.1 not in /etc/ld.so.cache") {
		t.Errorf("err = %v", err)
	}
}

func TestPlanNeedsRequestMappings(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	claim, pod := claimAndPod(c, req{"main", resolve.ResourceName("x-1"), "x-1"})
	pod.Status.ExtendedResourceClaimStatus = nil
	if _, err := planner(c, nil).Plan(claim, pod); err == nil || !strings.Contains(err.Error(), "no extended resource claim status") {
		t.Errorf("err = %v", err)
	}
}

func TestPlanIgnoresOtherDrivers(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	claim, pod := claimAndPod(c, req{"main", resolve.ResourceName("x-1"), "x-1"})
	claim.Status.Allocation.Devices.Results = append(claim.Status.Allocation.Devices.Results,
		resourceapi.DeviceRequestAllocationResult{Request: "gpu", Driver: "gpu.example.com", Pool: "n1", Device: "gpu0"})
	out, err := planner(c, nil).Plan(claim, pod)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["gpu"]; ok {
		t.Error("other drivers' requests must not be planned")
	}
}
```

- [ ] **Step 2: Write the failing CDI tests**

<!-- file: internal/dra/cdi_test.go -->
```go
package dra

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWriteSpec(t *testing.T) {
	dir := t.TempDir()
	edits := map[string]*ContainerEdits{
		"container-0-request-0": {Env: []string{"BRICS_MODULE_X=x-1;rank=10;src=image;mount=/opt/modules/x/1"}},
		"container-0-request-1": nil,
	}
	if err := WriteSpec(dir, "cuid", edits); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(SpecPath(dir, "cuid"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["cdiVersion"] != "0.6.0" || got["kind"] != CDIKind {
		t.Errorf("header = %v", got)
	}
	devs := got["devices"].([]any)
	if len(devs) != 1 || devs[0].(map[string]any)["name"] != "cuid-container-0-request-0" {
		t.Errorf("devices = %v", devs)
	}
	if CDIDeviceID("cuid", "container-0-request-0") != "modules.isambard.ac.uk/module=cuid-container-0-request-0" {
		t.Error("CDIDeviceID")
	}
	// Only the spec itself is left behind: no temp files for the CDI cache to read.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != filepath.Base(SpecPath(dir, "cuid")) {
		t.Errorf("dir = %v", entries)
	}
}

func TestWriteSpecIdempotent(t *testing.T) {
	dir := t.TempDir()
	edits := map[string]*ContainerEdits{"r": {Env: []string{"A=1"}}}
	_ = WriteSpec(dir, "u", edits)
	a, _ := os.ReadFile(SpecPath(dir, "u"))
	_ = WriteSpec(dir, "u", edits)
	b, _ := os.ReadFile(SpecPath(dir, "u"))
	if !reflect.DeepEqual(a, b) {
		t.Error("rewriting must produce the same file")
	}
}

func TestRemoveSpec(t *testing.T) {
	dir := t.TempDir()
	_ = WriteSpec(dir, "u", map[string]*ContainerEdits{"r": {Env: []string{"A=1"}}})
	if err := RemoveSpec(dir, "u"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSpec(dir, "u"); err != nil {
		t.Errorf("second remove must be a no-op: %v", err)
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./internal/dra/ -count=1`
Expected: FAIL to compile, `undefined: Planner`.

- [ ] **Step 4: Implement `plan.go`**

<!-- file: internal/dra/plan.go -->
```go
package dra

import (
	"fmt"
	"path"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

// Mount, Hook and ContainerEdits are CDI containerEdits (cdiVersion 0.6.0).
type Mount struct {
	HostPath      string   `json:"hostPath"`
	ContainerPath string   `json:"containerPath"`
	Type          string   `json:"type,omitempty"`
	Options       []string `json:"options,omitempty"`
}

type Hook struct {
	HookName string   `json:"hookName"`
	Path     string   `json:"path"`
	Args     []string `json:"args,omitempty"`
}

type ContainerEdits struct {
	Env    []string `json:"env,omitempty"`
	Mounts []Mount  `json:"mounts,omitempty"`
	Hooks  []Hook   `json:"hooks,omitempty"`
}

var (
	bindOptions  = []string{"bind", "ro", "nosuid", "nodev", "rprivate"}
	rbindOptions = []string{"rbind", "ro", "nosuid", "nodev", "rprivate"}
)

// Planner turns the scheduler's extended-resource claim into CDI edits.
type Planner struct {
	Catalogue Catalogue
	Hosts     HostModules
	PVCs      PVCs
	NodeName  string
	HookPath  string
}

type member struct {
	value resolve.ModuleValue
	raw   string
}

// Plan maps each of the claim's requests for this driver to the CDI edits
// its container gets. A container's edits all go on its lowest-index
// request, and its other requests map to nil, so overlapping closures never
// bind a file twice.
func (p *Planner) Plan(claim *resourceapi.ResourceClaim, pod *corev1.Pod) (map[string]*ContainerEdits, error) {
	st := pod.Status.ExtendedResourceClaimStatus
	if st == nil || st.ResourceClaimName != claim.Name {
		return nil, fmt.Errorf("pod %s/%s has no extended resource claim status for claim %s yet", pod.Namespace, pod.Name, claim.Name)
	}
	if claim.Status.Allocation == nil {
		return nil, fmt.Errorf("claim %s/%s is not allocated", claim.Namespace, claim.Name)
	}
	type mapping struct{ container, resource string }
	byRequest := map[string]mapping{}
	for _, m := range st.RequestMappings {
		byRequest[m.RequestName] = mapping{m.ContainerName, m.ResourceName}
	}
	byDevice := map[string]*v1.Module{}
	for _, m := range p.Catalogue.List() {
		byDevice[resolve.DeviceName(m.Name)] = &m
	}

	requests := map[string][]string{}
	modules := map[string][]*v1.Module{}
	for _, r := range claim.Status.Allocation.Devices.Results {
		if r.Driver != v1.DriverName {
			continue
		}
		mp, ok := byRequest[r.Request]
		if !ok {
			return nil, fmt.Errorf("request %s is not mapped to a container", r.Request)
		}
		m, ok := byDevice[r.Device]
		if !ok || m.Status.Resolved == nil {
			return nil, fmt.Errorf("device %s is not a resolved Module (deleted or changed after scheduling); recreate the pod", r.Device)
		}
		if fam, ok := strings.CutPrefix(mp.resource, v1.FamilyResourcePrefix); ok {
			ann := pod.Annotations[resolve.ResolvedAnnotationKey(fam)]
			if !strings.HasPrefix(ann, m.Name+";") {
				admitted, _, _ := strings.Cut(ann, ";")
				return nil, fmt.Errorf("default for family %s changed from %s to %s after admission; recreate the pod", fam, admitted, m.Name)
			}
		}
		requests[mp.container] = append(requests[mp.container], r.Request)
		modules[mp.container] = append(modules[mp.container], m)
	}

	out := map[string]*ContainerEdits{}
	for c, reqs := range requests {
		// container-<i>-request-<j>: within one container only j varies, so
		// shorter sorts first gives numeric order.
		sort.Slice(reqs, func(i, j int) bool {
			if len(reqs[i]) != len(reqs[j]) {
				return len(reqs[i]) < len(reqs[j])
			}
			return reqs[i] < reqs[j]
		})
		e, err := p.edits(modules[c])
		if err != nil {
			return nil, fmt.Errorf("container %s: %w", c, err)
		}
		out[reqs[0]] = e
		for _, r := range reqs[1:] {
			out[r] = nil
		}
	}
	return out, nil
}

// edits builds one container's edits from the union of its modules'
// closures. A member in several closures keeps its highest rank.
func (p *Planner) edits(mods []*v1.Module) (*ContainerEdits, error) {
	members := map[string]member{}
	for _, m := range mods {
		for _, n := range m.Status.Resolved.Modules {
			dep, ok := p.Catalogue.Module(n)
			if !ok {
				return nil, fmt.Errorf("module %s (needed by %s) is not in the catalogue", n, m.Name)
			}
			raw := m.Status.Resolved.Annotations[resolve.ResolvedAnnotationKey(dep.Spec.Family)]
			mv, err := resolve.ParseValue(raw)
			if err != nil {
				return nil, fmt.Errorf("module %s: %w", m.Name, err)
			}
			if cur, ok := members[n]; !ok || mv.Rank > cur.value.Rank {
				members[n] = member{mv, raw}
			}
		}
	}
	ordered := make([]member, 0, len(members))
	for _, mb := range members {
		ordered = append(ordered, mb)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i].value, ordered[j].value
		if a.Rank != b.Rank {
			return a.Rank > b.Rank
		}
		return a.Name < b.Name
	})

	e := &ContainerEdits{}
	var hookArgs []string
	for _, mb := range ordered {
		m := mb.value
		e.Env = append(e.Env, resolve.MarkerKey(m.Name)+"="+mb.raw)
		switch {
		case m.Src == "host":
			r, ok := p.Hosts.HostModule(m.Name)
			if !ok || !r.Ready {
				why := "not discovered yet"
				if ok {
					why = r.Message
				}
				return nil, fmt.Errorf("host module %s is not available on node %s: %s", m.Name, p.NodeName, why)
			}
			for _, l := range r.Lib {
				e.Mounts = append(e.Mounts, Mount{HostPath: l.Path, ContainerPath: path.Join(m.Mount, "lib", l.Soname), Type: "bind", Options: bindOptions})
			}
			for _, l := range r.Deps {
				e.Mounts = append(e.Mounts, Mount{HostPath: l.Path, ContainerPath: path.Join(m.Mount, "deps", l.Soname), Type: "bind", Options: bindOptions})
			}
			if len(r.Deps) > 0 {
				hookArgs = append(hookArgs, "--deps", path.Join(m.Mount, "deps"))
			}
			if r.GlibcRequired != "" {
				hookArgs = append(hookArgs, "--glibc", m.Name+"="+r.GlibcRequired)
			}
		case strings.HasPrefix(m.Src, "pvc:"):
			claim, sub, _ := strings.Cut(strings.TrimPrefix(m.Src, "pvc:"), "/")
			src, err := p.PVCs.HostPath(claim, sub)
			if err != nil {
				return nil, err
			}
			e.Mounts = append(e.Mounts, Mount{HostPath: src, ContainerPath: m.Mount, Type: "bind", Options: rbindOptions})
		}
	}
	if len(hookArgs) > 0 {
		e.Hooks = []Hook{{HookName: "createContainer", Path: p.HookPath, Args: append([]string{"module-hook"}, hookArgs...)}}
	}
	return e, nil
}
```

- [ ] **Step 5: Implement `cdi.go`**

<!-- file: internal/dra/cdi.go -->
```go
package dra

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"k8s.io/apimachinery/pkg/types"
)

// CDIKind is the vendor/class of every CDI device this driver writes.
const CDIKind = "modules.isambard.ac.uk/module"

type cdiSpec struct {
	Version string      `json:"cdiVersion"`
	Kind    string      `json:"kind"`
	Devices []cdiDevice `json:"devices"`
}

type cdiDevice struct {
	Name           string          `json:"name"`
	ContainerEdits *ContainerEdits `json:"containerEdits"`
}

// CDIDeviceID names the CDI device for one request of a claim. Claim UIDs
// are never reused, so the CDI cache never serves a stale device.
func CDIDeviceID(claim types.UID, request string) string {
	return CDIKind + "=" + string(claim) + "-" + request
}

// SpecPath is the claim's CDI spec file.
func SpecPath(dir string, claim types.UID) string {
	return filepath.Join(dir, "modules.isambard.ac.uk-"+string(claim)+".json")
}

// WriteSpec atomically writes one CDI device per request with edits. The
// temp file's .tmp extension keeps the CDI cache from reading it.
func WriteSpec(dir string, claim types.UID, edits map[string]*ContainerEdits) error {
	s := cdiSpec{Version: "0.6.0", Kind: CDIKind}
	reqs := make([]string, 0, len(edits))
	for r := range edits {
		reqs = append(reqs, r)
	}
	sort.Strings(reqs)
	for _, r := range reqs {
		if edits[r] != nil {
			s.Devices = append(s.Devices, cdiDevice{Name: string(claim) + "-" + r, ContainerEdits: edits[r]})
		}
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".modules-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), SpecPath(dir, claim))
}

// RemoveSpec deletes the claim's CDI spec; a missing file is not an error.
func RemoveSpec(dir string, claim types.UID) error {
	if err := os.Remove(SpecPath(dir, claim)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/dra/ -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/dra
git commit -m "feat(dra): per-container CDI edits for module closures

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Driver and publisher

**Files:**
- Create: `internal/dra/driver.go`, `internal/dra/publisher.go`
- Test: `internal/dra/driver_test.go`, `internal/dra/publisher_test.go`

**Interfaces:**
- Consumes: `Planner`, `WriteSpec`, `RemoveSpec`, `CDIDeviceID` (Task 4); `Devices`, `Resources` (Task 3).
- Produces:
  - `dra.Pods interface { Pod(ctx context.Context, namespace, name string) (*corev1.Pod, error) }`;
  - `dra.ClientsetPods{Client kubernetes.Interface}`;
  - `dra.Driver{Planner *Planner; Pods Pods; CDIDir string; Log logr.Logger}`, which implements `kubeletplugin.DRAPlugin`;
  - `dra.Publisher{Pool string; Catalogue Catalogue; Hosts HostModules; PVCs PVCs; Publish func(context.Context, resourceslice.DriverResources) error; Debounce time.Duration; Log logr.Logger}` with `(*Publisher).Run(ctx context.Context, trigger <-chan struct{})`.

- [ ] **Step 1: Write the failing tests**

<!-- file: internal/dra/driver_test.go -->
```go
package dra

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"

	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

type fakePods map[string]*corev1.Pod

func (f fakePods) Pod(_ context.Context, ns, name string) (*corev1.Pod, error) {
	if p, ok := f[ns+"/"+name]; ok {
		return p, nil
	}
	return nil, errors.New("pods \"" + name + "\" not found")
}

func driverFor(t *testing.T, c fakeCatalogue, pod *corev1.Pod) *Driver {
	return &Driver{Planner: planner(c, nil), Pods: fakePods{pod.Namespace + "/" + pod.Name: pod}, CDIDir: t.TempDir(), Log: logr.Discard()}
}

func reserve(claim *resourceapi.ResourceClaim, pod *corev1.Pod) {
	pod.UID = "puid"
	claim.Status.ReservedFor = []resourceapi.ResourceClaimConsumerReference{{Resource: "pods", Name: pod.Name, UID: pod.UID}}
}

func TestPrepare(t *testing.T) {
	c := catalogue(t, withLib(mod("nccl", "2", img)))
	claim, pod := claimAndPod(c,
		req{"a", resolve.ResourceName("nccl-2"), "nccl-2"},
		req{"a", resolve.ResourceName("nccl-2"), "nccl-2"})
	reserve(claim, pod)
	d := driverFor(t, c, pod)
	res, err := d.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	if err != nil {
		t.Fatal(err)
	}
	r := res[claim.UID]
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	want := []kubeletplugin.Device{
		{Requests: []string{"container-0-request-0"}, PoolName: "n1", DeviceName: resolve.DeviceName("nccl-2"),
			CDIDeviceIDs: []string{CDIDeviceID(claim.UID, "container-0-request-0")}},
		{Requests: []string{"container-0-request-1"}, PoolName: "n1", DeviceName: resolve.DeviceName("nccl-2")},
	}
	if !reflect.DeepEqual(r.Devices, want) {
		t.Errorf("devices = %+v\nwant %+v", r.Devices, want)
	}
	if _, err := os.Stat(SpecPath(d.CDIDir, claim.UID)); err != nil {
		t.Error(err)
	}
}

func TestPrepareIdempotent(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	claim, pod := claimAndPod(c, req{"a", resolve.ResourceName("x-1"), "x-1"})
	reserve(claim, pod)
	d := driverFor(t, c, pod)
	first, _ := d.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	a, _ := os.ReadFile(SpecPath(d.CDIDir, claim.UID))
	again, _ := d.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	b, _ := os.ReadFile(SpecPath(d.CDIDir, claim.UID))
	if !reflect.DeepEqual(first, again) || string(a) != string(b) {
		t.Error("prepare must be idempotent")
	}
}

func TestPrepareReplacedPod(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	claim, pod := claimAndPod(c, req{"a", resolve.ResourceName("x-1"), "x-1"})
	reserve(claim, pod)
	claim.Status.ReservedFor[0].UID = types.UID("older")
	res, _ := driverFor(t, c, pod).PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	if err := res[claim.UID].Err; err == nil || !strings.Contains(err.Error(), "was replaced") {
		t.Errorf("err = %v", err)
	}
}

func TestUnprepare(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	claim, pod := claimAndPod(c, req{"a", resolve.ResourceName("x-1"), "x-1"})
	reserve(claim, pod)
	d := driverFor(t, c, pod)
	_, _ = d.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	res, err := d.UnprepareResourceClaims(context.Background(), []kubeletplugin.NamespacedObject{{UID: claim.UID}})
	if err != nil || res[claim.UID] != nil {
		t.Fatalf("unprepare: %v %v", res, err)
	}
	if _, err := os.Stat(SpecPath(d.CDIDir, claim.UID)); !os.IsNotExist(err) {
		t.Errorf("spec still present: %v", err)
	}
}
```

<!-- file: internal/dra/publisher_test.go -->
```go
package dra

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/dynamic-resource-allocation/resourceslice"
)

func TestPublisherRepublishesOnTrigger(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	var mu sync.Mutex
	var counts []int
	pub := &Publisher{Pool: "n1", Catalogue: c, Hosts: fakeHosts{}, PVCs: fakePVCs{}, Debounce: 10 * time.Millisecond, Log: logr.Discard(),
		Publish: func(_ context.Context, r resourceslice.DriverResources) error {
			mu.Lock()
			defer mu.Unlock()
			counts = append(counts, len(r.Pools["n1"].Slices[0].Devices))
			return nil
		}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	trigger := make(chan struct{}, 1)
	go pub.Run(ctx, trigger)

	waitCount := func(n int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			mu.Lock()
			got := len(counts)
			mu.Unlock()
			if got >= n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("published %d times, want %d", got, n)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitCount(1)
	y := mod("y", "1", img)
	for k, v := range catalogue(t, y) {
		c[k] = v
	}
	trigger <- struct{}{}
	waitCount(2)
	mu.Lock()
	defer mu.Unlock()
	if counts[0] != 1 || counts[1] != 2 {
		t.Errorf("device counts = %v", counts)
	}
}
```

The `catalogue(t, y)` in this test resolves `y` alone, and the map copy adds it to `c` without re-resolving `x`. That is enough, because the second publish must list both.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/dra/ -count=1`
Expected: FAIL to compile, `undefined: Driver`.

- [ ] **Step 3: Implement `driver.go`**

<!-- file: internal/dra/driver.go -->
```go
package dra

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
)

// Pods reads the pod a claim is reserved for.
type Pods interface {
	Pod(ctx context.Context, namespace, name string) (*corev1.Pod, error)
}

// ClientsetPods reads pods from the API server; prepare needs the pod's
// current status, which a cache might not have yet.
type ClientsetPods struct{ Client kubernetes.Interface }

func (c ClientsetPods) Pod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	return c.Client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
}

// Driver is the kubelet plugin: it prepares the scheduler's
// extended-resource claims by writing their CDI specs.
type Driver struct {
	Planner *Planner
	Pods    Pods
	CDIDir  string
	Log     logr.Logger
}

var _ kubeletplugin.DRAPlugin = (*Driver)(nil)

func (d *Driver) PrepareResourceClaims(ctx context.Context, claims []*resourceapi.ResourceClaim) (map[types.UID]kubeletplugin.PrepareResult, error) {
	out := map[types.UID]kubeletplugin.PrepareResult{}
	for _, c := range claims {
		out[c.UID] = d.prepare(ctx, c)
	}
	return out, nil
}

func (d *Driver) prepare(ctx context.Context, c *resourceapi.ResourceClaim) kubeletplugin.PrepareResult {
	id := c.Namespace + "/" + c.Name
	fail := func(err error) kubeletplugin.PrepareResult {
		d.Log.Info("refusing claim", "claim", id, "reason", err.Error())
		return kubeletplugin.PrepareResult{Err: fmt.Errorf("modules: %w", err)}
	}
	if len(c.Status.ReservedFor) != 1 || c.Status.ReservedFor[0].Resource != "pods" {
		return fail(fmt.Errorf("claim %s must be reserved for exactly one pod", id))
	}
	ref := c.Status.ReservedFor[0]
	pod, err := d.Pods.Pod(ctx, c.Namespace, ref.Name)
	if err != nil {
		return fail(err)
	}
	if pod.UID != ref.UID {
		return fail(fmt.Errorf("pod %s/%s was replaced after claim %s was reserved", c.Namespace, ref.Name, id))
	}
	edits, err := d.Planner.Plan(c, pod)
	if err != nil {
		return fail(err)
	}
	if err := WriteSpec(d.CDIDir, c.UID, edits); err != nil {
		return fail(err)
	}
	var res kubeletplugin.PrepareResult
	for _, r := range c.Status.Allocation.Devices.Results {
		if r.Driver != v1.DriverName {
			continue
		}
		dev := kubeletplugin.Device{Requests: []string{r.Request}, PoolName: r.Pool, DeviceName: r.Device, ShareID: r.ShareID}
		if edits[r.Request] != nil {
			dev.CDIDeviceIDs = []string{CDIDeviceID(c.UID, r.Request)}
		}
		res.Devices = append(res.Devices, dev)
	}
	d.Log.Info("prepared claim", "claim", id, "pod", pod.Name)
	return res
}

func (d *Driver) UnprepareResourceClaims(_ context.Context, claims []kubeletplugin.NamespacedObject) (map[types.UID]error, error) {
	out := map[types.UID]error{}
	for _, c := range claims {
		out[c.UID] = RemoveSpec(d.CDIDir, c.UID)
	}
	return out, nil
}

func (d *Driver) HandleError(_ context.Context, err error, msg string) {
	d.Log.Error(err, msg)
}
```

- [ ] **Step 4: Implement `publisher.go`**

<!-- file: internal/dra/publisher.go -->
```go
package dra

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/dynamic-resource-allocation/resourceslice"
)

// Publisher republishes this node's devices whenever trigger fires:
// Module changes and every discovery run.
type Publisher struct {
	Pool      string
	Catalogue Catalogue
	Hosts     HostModules
	PVCs      PVCs
	Publish   func(context.Context, resourceslice.DriverResources) error
	Debounce  time.Duration
	Log       logr.Logger
}

func (p *Publisher) Run(ctx context.Context, trigger <-chan struct{}) {
	for {
		devs := Devices(p.Catalogue, p.Hosts, p.PVCs)
		if err := p.Publish(ctx, Resources(p.Pool, devs)); err != nil {
			p.Log.Error(err, "publishing ResourceSlices")
		} else {
			p.Log.V(1).Info("published devices", "count", len(devs))
		}
		select {
		case <-ctx.Done():
			return
		case <-trigger:
		}
		// Coalesce bursts: a Module change also starts a discovery run.
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.Debounce):
		}
	drain:
		for {
			select {
			case <-trigger:
			default:
				break drain
			}
		}
	}
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/dra/ -count=1 -race`
Expected: PASS. `-race` needs cgo; if the linker fails on this Mac, run without `-race`.

- [ ] **Step 6: Commit**

```bash
git add internal/dra
git commit -m "feat(dra): kubelet plugin prepare/unprepare and slice publisher

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Wire the driver into `module-nri`

**Files:**
- Modify: `internal/nri/catalogue.go` (add `List`)
- Modify: `internal/nri/mountinfo.go` (add `Mounted`)
- Modify: `internal/nri/report.go` (add `Discoverer.OnUpdate`)
- Modify: `cmd/module-nri/main.go`
- Test: `internal/nri/report_test.go` (one new test)

**Interfaces:**
- Consumes: `dra.Planner`, `dra.Driver`, `dra.ClientsetPods`, `dra.Publisher` (Task 5); `nri.Plugin{Log}` (Task 2).
- Produces:
  - `nri.CacheCatalogue.List() []v1.Module`;
  - `(*nri.MountinfoPVCs).Mounted(claim string) bool`;
  - `nri.Discoverer.OnUpdate func()`;
  - a new module-nri flag, `--cdi-dir` (default `/var/run/cdi`).

- [ ] **Step 1: Write the failing `OnUpdate` test**

Read `internal/nri/report_test.go` for the existing `Discoverer` test setup: a fake reader and writer built with controller-runtime's `fake.NewClientBuilder`, and a temp host root. Add a test in the same style:

```go
func TestDiscovererCallsOnUpdate(t *testing.T) {
	d := newTestDiscoverer(t) // the helper the existing RunOnce tests use; if they build the Discoverer inline, copy that construction here
	calls := 0
	d.OnUpdate = func() { calls++ }
	if err := d.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("OnUpdate called %d times, want 1", calls)
	}
}
```

If `report_test.go` has no reusable helper, build the `Discoverer` exactly as its first `RunOnce` test does, then set `OnUpdate`.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/nri/ -run TestDiscovererCallsOnUpdate -count=1`
Expected: FAIL to compile, `d.OnUpdate undefined`.

- [ ] **Step 3: Implement the three small additions**

In `internal/nri/report.go`, add a field to `Discoverer`:

```go
	// OnUpdate, if set, runs after each discovery run updates State, so the
	// DRA driver can republish device readiness.
	OnUpdate func()
```

In `RunOnce`, right after `d.State.Set(results)`:

```go
	if d.OnUpdate != nil {
		d.OnUpdate()
	}
```

Append to `internal/nri/catalogue.go`:

```go
// List returns every Module in the cache; nil if the cache can't be read.
func (c CacheCatalogue) List() []v1.Module {
	var l v1.ModuleList
	if err := c.Reader.List(context.Background(), &l); err != nil {
		return nil
	}
	return l.Items
}
```

Append to `internal/nri/mountinfo.go`:

```go
// Mounted reports whether module-nri mounts claim (it is in nri.pvcs).
func (p *MountinfoPVCs) Mounted(claim string) bool { return p.Claims[claim] }
```

- [ ] **Step 4: Update `cmd/module-nri/main.go`**

Make these changes:
- Add a `cdiDir` string variable next to the others, and the flag `flag.StringVar(&cdiDir, "cdi-dir", "/var/run/cdi", "Directory for this driver's CDI specs.")`.
- Pass `cdiDir` through `run` (add it as the last parameter).
- Add these imports: `"k8s.io/client-go/kubernetes"`, `"k8s.io/dynamic-resource-allocation/kubeletplugin"`, `"github.com/isambard-sc/k8s-modules/internal/dra"`.

In `run`, replace the informer event-handler block with:

```go
	sliceTrigger := make(chan struct{}, 1)
	pokeSlices := func() {
		select {
		case sliceTrigger <- struct{}{}:
		default:
		}
	}
	// Discovery only cares about host Modules being added or removed; the
	// driver's devices change with any Module change (status, default label).
	if _, err := inf.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { poke(obj); pokeSlices() },
		UpdateFunc: func(_, _ any) { pokeSlices() },
		DeleteFunc: func(obj any) { poke(nil); pokeSlices() },
	}); err != nil {
		return err
	}
```

Set `OnUpdate: pokeSlices` in the `nri.Discoverer` literal.

Replace everything from `claims := map[string]bool{}` down to (but not including) `s, err := stub.New(` with:

```go
	claims := map[string]bool{}
	for _, c := range strings.Split(pvcs, ",") {
		if c = strings.TrimSpace(c); c != "" {
			claims[c] = true
		}
	}
	cat := nri.CacheCatalogue{Reader: modules}
	pvcPaths := &nri.MountinfoPVCs{Claims: claims, Dir: pvcDir,
		SelfMountinfo: "/proc/self/mountinfo", HostMountinfo: filepath.Join(hostRoot, "proc/1/mountinfo"), PodUID: podUID}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	driver := &dra.Driver{
		Planner: &dra.Planner{Catalogue: cat, Hosts: state, PVCs: pvcPaths, NodeName: nodeName, HookPath: hookPath},
		Pods:    dra.ClientsetPods{Client: cs},
		CDIDir:  cdiDir,
		Log:     log.WithName("dra"),
	}
	helper, err := kubeletplugin.Start(ctx, driver,
		kubeletplugin.DriverName(v1.DriverName),
		kubeletplugin.KubeClient(cs),
		kubeletplugin.NodeName(nodeName))
	if err != nil {
		return fmt.Errorf("starting DRA kubelet plugin: %w", err)
	}
	defer helper.Stop()
	pub := &dra.Publisher{Pool: nodeName, Catalogue: cat, Hosts: state, PVCs: pvcPaths,
		Publish: helper.PublishResources, Debounce: time.Second, Log: log.WithName("slices")}
	go pub.Run(ctx, sliceTrigger)

	p := &nri.Plugin{Log: log}
```

Update the package comment to: `// Command module-nri is the module system's node agent: a DRA kubelet plugin that publishes module devices and writes their CDI specs, and an NRI plugin that orders LD_LIBRARY_PATH and applies masks. It runs as a DaemonSet on every node.`

- [ ] **Step 5: Build and test**

Run: `go build ./... && go vet ./... && go test ./api/... ./internal/... ./cmd/... -count=1`
Expected: build OK and tests PASS. `cmd/module-nri/main_test.go` only covers `install-hook`, so it is unaffected.

- [ ] **Step 6: Commit**

```bash
git add internal/nri cmd/module-nri
git commit -m "feat(module-nri): run the DRA kubelet plugin next to the NRI plugin

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: DeviceClass reconciler

**Files:**
- Create: `internal/controller/deviceclass_controller.go`
- Test: `internal/controller/deviceclass_controller_test.go`, `test/envtest/deviceclass_test.go`
- Modify: `cmd/module-controller/main.go`, `test/envtest/suite_test.go`

**Interfaces:**
- Consumes: `resolve.DeviceClassName`, `resolve.FamilyClassName`, `resolve.ResourceName`, `resolve.FamilyResourceName`, `v1.LabelManaged`, `v1.DriverName` (Task 1).
- Produces: `controller.DesiredDeviceClasses(mods []v1.Module) []resourcev1.DeviceClass` and `controller.DeviceClassReconciler{client.Client}` with `SetupWithManager(mgr) error`.

- [ ] **Step 1: Write the failing unit test**

<!-- file: internal/controller/deviceclass_controller_test.go -->
```go
package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
)

func TestDesiredDeviceClasses(t *testing.T) {
	resolved := v1.Module{ObjectMeta: metav1.ObjectMeta{Name: "nccl-2.29.2"},
		Spec: v1.ModuleSpec{Family: "nccl", Version: "2.29.2"}, Status: v1.ModuleStatus{Resolved: &v1.Resolved{}}}
	pending := v1.Module{ObjectMeta: metav1.ObjectMeta{Name: "ofi-1"}, Spec: v1.ModuleSpec{Family: "ofi", Version: "1"}}
	got := DesiredDeviceClasses([]v1.Module{pending, resolved})

	want := map[string][2]string{
		"nccl-2.29.2.modules.isambard.ac.uk": {"modules.isambard.ac.uk/nccl-2.29.2",
			`device.driver == "modules.isambard.ac.uk" && device.attributes["modules.isambard.ac.uk"].module == "nccl-2.29.2"`},
		"nccl.family.modules.isambard.ac.uk": {"family.modules.isambard.ac.uk/nccl",
			`device.driver == "modules.isambard.ac.uk" && device.attributes["modules.isambard.ac.uk"].family == "nccl" && device.attributes["modules.isambard.ac.uk"].default == true`},
		"ofi.family.modules.isambard.ac.uk": {"family.modules.isambard.ac.uk/ofi",
			`device.driver == "modules.isambard.ac.uk" && device.attributes["modules.isambard.ac.uk"].family == "ofi" && device.attributes["modules.isambard.ac.uk"].default == true`},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d classes, want %d: %+v", len(got), len(want), got)
	}
	for i, dc := range got {
		w, ok := want[dc.Name]
		if !ok {
			t.Errorf("unexpected class %s", dc.Name)
			continue
		}
		if i > 0 && got[i-1].Name >= dc.Name {
			t.Error("classes must be sorted by name")
		}
		if dc.Labels[v1.LabelManaged] != "true" || dc.Spec.ExtendedResourceName == nil || *dc.Spec.ExtendedResourceName != w[0] ||
			len(dc.Spec.Selectors) != 1 || dc.Spec.Selectors[0].CEL == nil || dc.Spec.Selectors[0].CEL.Expression != w[1] {
			t.Errorf("class %s = %+v", dc.Name, dc)
		}
	}
}
```

The unresolved `ofi-1` gets no Module class, but its family still gets a family class. That family class selects nothing until a default is ready.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/controller/ -run TestDesiredDeviceClasses -count=1`
Expected: FAIL to compile, `undefined: DesiredDeviceClasses`.

- [ ] **Step 3: Implement the reconciler**

<!-- file: internal/controller/deviceclass_controller.go -->
```go
package controller

import (
	"context"
	"fmt"
	"sort"

	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

// DeviceClassReconciler keeps one DeviceClass per resolved Module and one
// per family, so containers can request modules as extended resources.
type DeviceClassReconciler struct {
	client.Client
}

// All classes are reconciled together under one key.
var allClasses = reconcile.Request{NamespacedName: types.NamespacedName{Name: "deviceclasses"}}

func (r *DeviceClassReconciler) SetupWithManager(mgr ctrl.Manager) error {
	all := handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []reconcile.Request {
		return []reconcile.Request{allClasses}
	})
	return ctrl.NewControllerManagedBy(mgr).
		Named("deviceclass").
		Watches(&v1.Module{}, all).
		Watches(&resourcev1.DeviceClass{}, all, builder.WithPredicates(predicate.NewPredicateFuncs(func(o client.Object) bool {
			return o.GetLabels()[v1.LabelManaged] == "true"
		}))).
		Complete(r)
}

func (r *DeviceClassReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	var mods v1.ModuleList
	if err := r.List(ctx, &mods); err != nil {
		return ctrl.Result{}, err
	}
	want := map[string]resourcev1.DeviceClass{}
	for _, dc := range DesiredDeviceClasses(mods.Items) {
		want[dc.Name] = dc
	}
	var have resourcev1.DeviceClassList
	if err := r.List(ctx, &have, client.MatchingLabels{v1.LabelManaged: "true"}); err != nil {
		return ctrl.Result{}, err
	}
	existing := map[string]*resourcev1.DeviceClass{}
	for i := range have.Items {
		dc := &have.Items[i]
		if _, ok := want[dc.Name]; !ok {
			if err := r.Delete(ctx, dc); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			continue
		}
		existing[dc.Name] = dc
	}
	for name, dc := range want {
		cur, ok := existing[name]
		if !ok {
			if err := r.Create(ctx, &dc); err != nil && !apierrors.IsAlreadyExists(err) {
				return ctrl.Result{}, err
			}
			continue
		}
		if !equality.Semantic.DeepEqual(cur.Spec.Selectors, dc.Spec.Selectors) ||
			!equality.Semantic.DeepEqual(cur.Spec.ExtendedResourceName, dc.Spec.ExtendedResourceName) {
			cur.Spec.Selectors, cur.Spec.ExtendedResourceName = dc.Spec.Selectors, dc.Spec.ExtendedResourceName
			if err := r.Update(ctx, cur); err != nil {
				return ctrl.Result{}, err
			}
		}
	}
	return ctrl.Result{}, nil
}

// DesiredDeviceClasses returns a class for each resolved Module and for each
// family with any Module, sorted by name. The family class selects the
// device whose default attribute is true, which the driver sets from the
// default label, so moving the default never changes a class.
func DesiredDeviceClasses(mods []v1.Module) []resourcev1.DeviceClass {
	attr := fmt.Sprintf("device.attributes[%q]", v1.DriverName)
	drv := fmt.Sprintf("device.driver == %q", v1.DriverName)
	var out []resourcev1.DeviceClass
	families := map[string]bool{}
	for _, m := range mods {
		families[m.Spec.Family] = true
		if m.Status.Resolved == nil {
			continue
		}
		out = append(out, deviceClass(resolve.DeviceClassName(m.Name), resolve.ResourceName(m.Name),
			fmt.Sprintf("%s && %s.%s == %q", drv, attr, v1.AttrModule, m.Name)))
	}
	for f := range families {
		out = append(out, deviceClass(resolve.FamilyClassName(f), resolve.FamilyResourceName(f),
			fmt.Sprintf("%s && %s.%s == %q && %s.%s == true", drv, attr, v1.AttrFamily, f, attr, v1.AttrDefault)))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func deviceClass(name, resource, expr string) resourcev1.DeviceClass {
	return resourcev1.DeviceClass{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{v1.LabelManaged: "true"}},
		Spec: resourcev1.DeviceClassSpec{
			Selectors:            []resourcev1.DeviceSelector{{CEL: &resourcev1.CELDeviceSelector{Expression: expr}}},
			ExtendedResourceName: ptr.To(resource),
		},
	}
}
```

- [ ] **Step 4: Run the unit test**

Run: `go test ./internal/controller/ -count=1`
Expected: PASS.

- [ ] **Step 5: Register it**

In `cmd/module-controller/main.go`, after the `NodeLabelReconciler` setup block, add:

```go
	if err := (&controller.DeviceClassReconciler{Client: mgr.GetClient()}).SetupWithManager(mgr); err != nil {
		log.Error(err, "setting up device class controller")
		os.Exit(1)
	}
```

In `test/envtest/suite_test.go`, add the same after the `NodeLabelReconciler` block, using the `fmt.Fprintln(os.Stderr, err); return 1` style.

- [ ] **Step 6: Write the envtest**

<!-- file: test/envtest/deviceclass_test.go -->
```go
package integration

import (
	"fmt"
	"testing"
	"time"

	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

func TestDeviceClasses(t *testing.T) {
	fam := newFamily("dc")
	m := imageModule(fam, "1", withDefault())
	create(t, m)
	waitResolved(t, m.Name)

	expect := map[string]string{
		resolve.DeviceClassName(m.Name): resolve.ResourceName(m.Name),
		resolve.FamilyClassName(fam):    resolve.FamilyResourceName(fam),
	}
	eventually(t, 15*time.Second, func() error {
		for name, res := range expect {
			dc := &resourcev1.DeviceClass{}
			if err := k8s.Get(ctx, client.ObjectKey{Name: name}, dc); err != nil {
				return err
			}
			if dc.Labels[v1.LabelManaged] != "true" || dc.Spec.ExtendedResourceName == nil || *dc.Spec.ExtendedResourceName != res {
				return fmt.Errorf("class %s = %+v", name, dc)
			}
		}
		return nil
	})

	if err := k8s.Delete(ctx, m); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, func() error {
		for name := range expect {
			err := k8s.Get(ctx, client.ObjectKey{Name: name}, &resourcev1.DeviceClass{})
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("class %s still present (err %v)", name, err)
			}
		}
		return nil
	})
}
```

- [ ] **Step 7: Run the envtest**

Run: `make test-envtest`
Expected: PASS, including `TestDeviceClasses`. Other admission tests still pass because admission is unchanged so far.

- [ ] **Step 8: Commit**

```bash
git add internal/controller cmd/module-controller test/envtest
git commit -m "feat(controller): a DeviceClass per Module and per family

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Per-container admission

**Files:**
- Modify (full rewrite): `deploy/helm/k8s-modules/templates/admission-mutating.yaml`, `admission-pods.yaml`, `admission-pod-consistency.yaml`
- Modify: `api/v1alpha1/module_types.go` (remove `AnnotationContainers`)
- Modify: `test/envtest/nri_admission_test.go` (`TestAdmissionNRIAnnotations`, `TestAdmissionNRIAnnotationsRequired`)
- Create: `test/envtest/dra_admission_test.go`

**Interfaces:**
- Consumes: `v1.AnnotationLoadContainers`, `resolve.ResourceName`, `resolve.FamilyResourceName` (Task 1).
- Produces: the admission behaviour described in the spec's "Mutating policy" and "Validating policies" sections, which Task 11's e2e tests rely on.

- [ ] **Step 1: Write the failing envtests**

<!-- file: test/envtest/dra_admission_test.go -->
```go
package integration

import (
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

func ctrWith(name string, limits ...string) corev1.Container {
	c := corev1.Container{Name: name, Image: "busybox"}
	if len(limits) > 0 {
		c.Resources.Limits = corev1.ResourceList{}
		for _, l := range limits {
			c.Resources.Limits[corev1.ResourceName(l)] = resource.MustParse("1")
		}
	}
	return c
}

func container(p *corev1.Pod, name string) corev1.Container {
	for _, c := range p.Spec.Containers {
		if c.Name == name {
			return c
		}
	}
	return corev1.Container{}
}

// mounted reports whether a container mounts the module's volume.
func mounted(c corev1.Container, module string) bool {
	for _, m := range c.VolumeMounts {
		if m.Name == resolve.VolumeName(module) {
			return true
		}
	}
	return false
}

func limitOne(c corev1.Container, key string) bool {
	l, r := c.Resources.Limits[corev1.ResourceName(key)], c.Resources.Requests[corev1.ResourceName(key)]
	return l.String() == "1" && r.String() == "1"
}

// onlyIn checks that exactly the named containers mount the module.
func onlyIn(module string, names ...string) func(*corev1.Pod) error {
	return func(p *corev1.Pod) error {
		want := map[string]bool{}
		for _, n := range names {
			want[n] = true
		}
		for _, c := range p.Spec.Containers {
			if mounted(c, module) != want[c.Name] {
				return fmt.Errorf("container %s mounts %s = %v, want %v (mounts %+v)", c.Name, module, mounted(c, module), want[c.Name], c.VolumeMounts)
			}
		}
		return nil
	}
}

func TestAdmissionExactLimit(t *testing.T) {
	fam := newFamily("exact")
	m := imageModule(fam, "1", withLib("lib"), withEnv("NCCL_DEBUG", "INFO"))
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	p := admit(t, func() *corev1.Pod {
		return newPod(ns, "", ctrWith("trainer", resolve.ResourceName(m.Name)), ctrWith("sidecar"))
	}, onlyIn(m.Name, "trainer"))
	if err := hasAnnotations(m.Status.Resolved.Annotations)(p); err != nil {
		t.Error(err)
	}
	if p.Annotations[v1.AnnotationRequiredPlugins] != v1.RequiredPluginsValue {
		t.Error("required-plugins annotation missing")
	}
	if _, ok := envValue(container(p, "trainer"), "NCCL_DEBUG"); !ok {
		t.Error("trainer lacks env.set")
	}
	if _, ok := envValue(container(p, "sidecar"), "NCCL_DEBUG"); ok {
		t.Error("sidecar must get nothing")
	}
}

func TestAdmissionFamilyLimit(t *testing.T) {
	fam := newFamily("famlim")
	m := imageModule(fam, "1", withDefault())
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	admit(t, func() *corev1.Pod {
		return newPod(ns, "", ctrWith("a", resolve.FamilyResourceName(fam)), ctrWith("b"))
	}, onlyIn(m.Name, "a"))
}

func TestAdmissionDependencyMountsOnRequester(t *testing.T) {
	base := imageModule(newFamily("dep"), "1")
	top := imageModule(newFamily("top"), "1", withDeps(base.Name))
	create(t, base)
	create(t, top)
	waitResolved(t, top.Name)
	ns := newNamespace(t, nil)
	admit(t, func() *corev1.Pod {
		return newPod(ns, "", ctrWith("a", resolve.ResourceName(top.Name)), ctrWith("b"))
	}, func(p *corev1.Pod) error {
		if err := onlyIn(top.Name, "a")(p); err != nil {
			return err
		}
		return onlyIn(base.Name, "a")(p)
	})
}

func TestAdmissionLoadAddsLimits(t *testing.T) {
	m := imageModule(newFamily("loadlim"), "1")
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	withCPU := ctrWith("a")
	withCPU.Resources.Limits = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")}
	admit(t, func() *corev1.Pod { return newPod(ns, m.Name, withCPU, ctrWith("b")) }, func(p *corev1.Pod) error {
		for _, c := range p.Spec.Containers {
			if !limitOne(c, resolve.ResourceName(m.Name)) {
				return fmt.Errorf("container %s resources = %+v", c.Name, c.Resources)
			}
		}
		if cpu := container(p, "a").Resources.Limits[corev1.ResourceCPU]; cpu.String() != "500m" {
			return fmt.Errorf("existing cpu limit lost: %+v", container(p, "a").Resources)
		}
		return onlyIn(m.Name, "a", "b")(p)
	})
}

func TestAdmissionLoadContainers(t *testing.T) {
	fam := newFamily("loadctr")
	m := imageModule(fam, "1", withDefault())
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	admit(t, func() *corev1.Pod {
		p := newPod(ns, fam, ctrWith("trainer"), ctrWith("sidecar"))
		p.Annotations[v1.AnnotationLoadContainers] = "trainer"
		return p
	}, func(p *corev1.Pod) error {
		if !limitOne(container(p, "trainer"), resolve.ResourceName(m.Name)) {
			return fmt.Errorf("trainer lacks the exact limit: %+v", container(p, "trainer").Resources)
		}
		if len(container(p, "sidecar").Resources.Limits) != 0 {
			return fmt.Errorf("sidecar got limits: %+v", container(p, "sidecar").Resources)
		}
		return onlyIn(m.Name, "trainer")(p)
	})
}

func TestAdmissionLoadContainersInvalid(t *testing.T) {
	m := imageModule(newFamily("loadbad"), "1")
	create(t, m)
	waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	for name, anns := range map[string]map[string]string{
		"unknown container": {v1.AnnotationLoad: m.Name, v1.AnnotationLoadContainers: "nope"},
		"empty":             {v1.AnnotationLoad: m.Name, v1.AnnotationLoadContainers: " , "},
		"without load":      {v1.AnnotationLoadContainers: "main"},
	} {
		t.Run(name, func(t *testing.T) {
			reject(t, func() *corev1.Pod {
				p := newPod(ns, "")
				for k, v := range anns {
					p.Annotations[k] = v
				}
				return p
			}, "modules.isambard.ac.uk/load-containers must")
		})
	}
}

func TestAdmissionUnknownLimit(t *testing.T) {
	fam := newFamily("unkfam")
	create(t, imageModule(fam, "1")) // no default
	waitResolved(t, fam+"-1")
	ns := newNamespace(t, nil)
	reject(t, func() *corev1.Pod { return newPod(ns, "", ctrWith("a", resolve.ResourceName("ghost-1"))) }, `module "ghost-1" was not loaded`)
	reject(t, func() *corev1.Pod { return newPod(ns, "", ctrWith("a", resolve.FamilyResourceName(fam))) }, `module "`+fam+`" was not loaded`)
	// An exact limit naming a family is not satisfied by that family's default.
	def := newFamily("exactfam")
	create(t, imageModule(def, "1", withDefault()))
	waitResolved(t, def+"-1")
	reject(t, func() *corev1.Pod {
		return newPod(ns, def, ctrWith("a", resolve.ResourceName(def)))
	}, `module "`+def+`" was not loaded`)
}

func TestAdmissionInitContainerLimit(t *testing.T) {
	m := imageModule(newFamily("init"), "1")
	create(t, m)
	waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	reject(t, func() *corev1.Pod {
		p := newPod(ns, "")
		p.Spec.InitContainers = []corev1.Container{ctrWith("setup", resolve.ResourceName(m.Name))}
		return p
	}, "init containers cannot load modules")
}

func TestAdmissionLimitQuantity(t *testing.T) {
	m := imageModule(newFamily("qty"), "1")
	create(t, m)
	waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	reject(t, func() *corev1.Pod {
		c := ctrWith("a")
		c.Resources.Limits = corev1.ResourceList{corev1.ResourceName(resolve.ResourceName(m.Name)): resource.MustParse("2")}
		return newPod(ns, "", c)
	}, "module resources must have quantity 1")
}

func TestAdmissionAccessViaLimit(t *testing.T) {
	m := imageModule(newFamily("acclim"), "1", withAccess(map[string]string{"team": "a"}))
	create(t, m)
	waitResolved(t, m.Name)
	ns := newNamespace(t, map[string]string{"team": "b"})
	reject(t, func() *corev1.Pod { return newPod(ns, "", ctrWith("a", resolve.ResourceName(m.Name))) },
		"not permitted in namespace", "was not loaded")
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `make test-envtest 2>&1 | grep -E '^(--- FAIL|ok|FAIL)'`
Expected: `TestAdmissionExactLimit`, `TestAdmissionFamilyLimit`, `TestAdmissionDependencyMountsOnRequester`, `TestAdmissionLoadAddsLimits`, `TestAdmissionLoadContainers`, `TestAdmissionLoadContainersInvalid`, `TestAdmissionUnknownLimit`, `TestAdmissionInitContainerLimit`, `TestAdmissionLimitQuantity` and `TestAdmissionAccessViaLimit` FAIL, mostly by timing out in `admit` or `reject`.

- [ ] **Step 3: Rewrite the mutating policy**

<!-- file: deploy/helm/k8s-modules/templates/admission-mutating.yaml -->
```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicy
metadata:
  name: modules.isambard.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  failurePolicy: Fail
  paramKind: {apiVersion: modules.isambard.ac.uk/v1alpha1, kind: Module}
  matchConstraints:
    {{- include "k8s-modules.podPolicyNamespaces" . | nindent 4 }}
    resourceRules:
    - {apiGroups: [""], apiVersions: [v1], operations: [CREATE], resources: [pods]}
  matchConditions:
  - name: requests-modules
    expression: >-
      (has(object.metadata.annotations) && 'modules.isambard.ac.uk/load' in object.metadata.annotations) ||
      object.spec.containers.exists(c, has(c.resources) && has(c.resources.limits) &&
        c.resources.limits.exists(k, k.startsWith('modules.isambard.ac.uk/') || k.startsWith('family.modules.isambard.ac.uk/')))
  variables:
  - name: anns
    expression: "has(object.metadata.annotations) ? object.metadata.annotations : {}"
  - name: requested
    expression: "'modules.isambard.ac.uk/load' in variables.anns ? variables.anns['modules.isambard.ac.uk/load'].split(',').map(s, s.trim()).filter(s, s != '') : []"
  - name: loadContainers
    expression: >-
      'modules.isambard.ac.uk/load-containers' in variables.anns ?
        variables.anns['modules.isambard.ac.uk/load-containers'].split(',').map(s, s.trim()).filter(s, s != '') :
        object.spec.containers.map(c, c.name)
  - name: isDefault
    expression: "has(params.metadata.labels) && 'modules.isambard.ac.uk/default' in params.metadata.labels && params.metadata.labels['modules.isambard.ac.uk/default'] == 'true'"
  - name: exactKey
    expression: "'modules.isambard.ac.uk/' + params.metadata.name"
  - name: familyKey
    expression: "'family.modules.isambard.ac.uk/' + params.spec.family"
  # Containers that request this Module through load (possibly narrowed by
  # load-containers) or through a container limit.
  - name: byLoad
    expression: >-
      params.metadata.name in variables.requested || (params.spec.family in variables.requested && variables.isDefault) ?
        object.spec.containers.map(c, c.name).filter(n, n in variables.loadContainers) : []
  - name: byLimit
    expression: >-
      object.spec.containers.filter(c, has(c.resources) && has(c.resources.limits) &&
        (variables.exactKey in c.resources.limits || (variables.isDefault && variables.familyKey in c.resources.limits))).map(c, c.name)
  - name: targets
    expression: "variables.byLoad + variables.byLimit"
  - name: nsAllowed
    expression: >-
      !has(params.spec.access) || !has(params.spec.access.matchLabels) ||
      params.spec.access.matchLabels.all(k, has(namespaceObject.metadata.labels) &&
        k in namespaceObject.metadata.labels && namespaceObject.metadata.labels[k] == params.spec.access.matchLabels[k])
  - name: applies
    expression: "has(params.status) && has(params.status.resolved) && variables.targets.size() > 0 && variables.nsAllowed"
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
            containers: object.spec.containers.filter(c, c.name in variables.targets).map(c, Object.spec.containers{
              name: c.name,
              volumeMounts: variables.mounts.map(m, Object.spec.containers.volumeMounts{name: m.name, mountPath: m.mountPath, readOnly: true}),
              env: variables.env.filter(e, !has(c.env) || !c.env.exists(ce, ce.name == e.name)).map(e, Object.spec.containers.env{name: e.name, value: e.value})
            })
          }
        }
  # load becomes an exact limit on each selected container, so the
  # scheduler allocates the Module's device to it. Quantities are plain
  # strings: quantity() is rejected by the apply.
  - patchType: ApplyConfiguration
    applyConfiguration:
      expression: >-
        !variables.applies || variables.byLoad.size() == 0 ? Object{} :
        Object{
          spec: Object.spec{
            containers: variables.byLoad.map(n, Object.spec.containers{
              name: n,
              resources: Object.spec.containers.resources{
                limits: {variables.exactKey: '1'},
                requests: {variables.exactKey: '1'}
              }
            })
          }
        }
  - patchType: ApplyConfiguration
    applyConfiguration:
      expression: >-
        !variables.applies ? Object{} :
        Object{
          metadata: Object.metadata{annotations: {
            'required-plugins.noderesource.dev/pod': '["modules"]'
          }}
        }
---
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicyBinding
metadata:
  name: modules.isambard.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  policyName: modules.isambard.ac.uk
  paramRef: {selector: {}, parameterNotFoundAction: Allow}
```

- [ ] **Step 4: Rewrite the pods policy**

<!-- file: deploy/helm/k8s-modules/templates/admission-pods.yaml -->
```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: pods.modules.isambard.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  failurePolicy: Fail
  matchConstraints:
    {{- include "k8s-modules.podPolicyNamespaces" . | nindent 4 }}
    resourceRules:
    - {apiGroups: [""], apiVersions: [v1], operations: [CREATE, UPDATE], resources: [pods, pods/status]}
  matchConditions:
  - name: has-module-fields
    expression: >-
      (has(object.metadata.annotations) && object.metadata.annotations.exists(k,
        k == 'modules.isambard.ac.uk/load' || k == 'modules.isambard.ac.uk/load-containers' || k.startsWith('resolved.modules.isambard.ac.uk/') || k.startsWith('env.modules.isambard.ac.uk/') || k.startsWith('required-plugins.noderesource.dev'))) ||
      (request.operation == 'UPDATE' && has(oldObject.metadata.annotations) && oldObject.metadata.annotations.exists(k,
        k == 'modules.isambard.ac.uk/load' || k == 'modules.isambard.ac.uk/load-containers' || k.startsWith('resolved.modules.isambard.ac.uk/') || k.startsWith('env.modules.isambard.ac.uk/') || k.startsWith('required-plugins.noderesource.dev'))) ||
      object.spec.containers.exists(c, has(c.resources) && has(c.resources.limits) &&
        c.resources.limits.exists(k, k.startsWith('modules.isambard.ac.uk/') || k.startsWith('family.modules.isambard.ac.uk/'))) ||
      (has(object.spec.initContainers) && object.spec.initContainers.exists(c, has(c.resources) && has(c.resources.limits) &&
        c.resources.limits.exists(k, k.startsWith('modules.isambard.ac.uk/') || k.startsWith('family.modules.isambard.ac.uk/'))))
  variables:
  - name: anns
    expression: "has(object.metadata.annotations) ? object.metadata.annotations : {}"
  - name: oldAnns
    expression: "request.operation == 'UPDATE' && has(oldObject.metadata.annotations) ? oldObject.metadata.annotations : {}"
  - name: loadNames
    expression: "'modules.isambard.ac.uk/load' in variables.anns ? variables.anns['modules.isambard.ac.uk/load'].split(',').map(s, s.trim()).filter(s, s != '') : []"
  # Every module limit key of every container, flattened by joining and
  # splitting (CEL here has no flatten).
  - name: limitKeys
    expression: >-
      object.spec.containers.map(c, has(c.resources) && has(c.resources.limits) ?
        c.resources.limits.filter(k, k.startsWith('modules.isambard.ac.uk/') || k.startsWith('family.modules.isambard.ac.uk/')).join(',') : '')
      .filter(s, s != '').join(',').split(',').filter(s, s != '')
  # 23 == len('modules.isambard.ac.uk/'), 30 == len('family.modules.isambard.ac.uk/').
  - name: exactNames
    expression: "variables.limitKeys.filter(k, k.startsWith('modules.isambard.ac.uk/')).map(k, k.substring(23))"
  - name: familyNames
    expression: "variables.limitKeys.filter(k, k.startsWith('family.modules.isambard.ac.uk/')).map(k, k.substring(30))"
  - name: requested
    expression: "variables.loadNames + variables.exactNames + variables.familyNames"
  - name: badNames
    expression: "variables.requested.filter(r, r.size() > 61 || !r.matches('^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$'))"
  - name: hasResolved
    expression: "variables.anns.exists(k, k.startsWith('resolved.modules.isambard.ac.uk/'))"
  - name: misfiled
    # 32 == len('resolved.modules.isambard.ac.uk/'); a value must name a module
    # of its key's family, or no Module would compare it with anything.
    expression: >-
      variables.anns.filter(k, k.startsWith('resolved.modules.isambard.ac.uk/') &&
        !variables.anns[k].startsWith(k.substring(32) + '-'))
  - name: isExactResolved
    expression: "variables.anns.filter(k, k.startsWith('resolved.modules.isambard.ac.uk/')).map(k, variables.anns[k].split(';')[0])"
  - name: unknown
    expression: >-
      variables.loadNames.filter(r, !(('resolved.modules.isambard.ac.uk/' + r) in variables.anns) && !(r in variables.isExactResolved)) +
      variables.exactNames.filter(r, !(r in variables.isExactResolved)) +
      variables.familyNames.filter(r, !(('resolved.modules.isambard.ac.uk/' + r) in variables.anns))
  - name: badQuantity
    expression: >-
      object.spec.containers.map(c, has(c.resources) && has(c.resources.limits) ?
        c.resources.limits.filter(k, (k.startsWith('modules.isambard.ac.uk/') || k.startsWith('family.modules.isambard.ac.uk/')) &&
          string(c.resources.limits[k]) != '1').join(',') : '').filter(s, s != '')
  - name: initModuleKeys
    expression: >-
      has(object.spec.initContainers) ? object.spec.initContainers.map(c, has(c.resources) && has(c.resources.limits) ?
        c.resources.limits.filter(k, k.startsWith('modules.isambard.ac.uk/') || k.startsWith('family.modules.isambard.ac.uk/')).join(',') : '')
        .filter(s, s != '') : []
  - name: loadContainers
    expression: "'modules.isambard.ac.uk/load-containers' in variables.anns ? variables.anns['modules.isambard.ac.uk/load-containers'].split(',').map(s, s.trim()).filter(s, s != '') : []"
  validations:
  - expression: "request.operation != 'CREATE' || variables.requested.size() <= 32"
    message: "a pod may request at most 32 modules (modules.isambard.ac.uk/load plus container limits)"
  # Before the unknown check: a bad load-containers makes load select no
  # container, which would otherwise surface as "was not loaded".
  - expression: >-
      request.operation != 'CREATE' || !('modules.isambard.ac.uk/load-containers' in variables.anns) ||
      ('modules.isambard.ac.uk/load' in variables.anns && variables.loadContainers.size() > 0 &&
       variables.loadContainers.all(n, object.spec.containers.exists(c, c.name == n)))
    message: "modules.isambard.ac.uk/load-containers must list existing containers of the pod and needs modules.isambard.ac.uk/load"
  - expression: "request.operation != 'CREATE' || variables.badNames.size() == 0"
    messageExpression: "'invalid module name \"' + variables.badNames[0] + '\" in modules.isambard.ac.uk/load or a container limit'"
  - expression: "request.operation != 'CREATE' || variables.badNames.size() > 0 || variables.unknown.size() == 0"
    messageExpression: >-
      'module "' + variables.unknown[0] + '" was not loaded: not found, not resolved, not permitted in namespace "' +
      request.namespace + '", or in conflict with another loaded module (kubectl get module ' + variables.unknown[0] + ')'
  - expression: "request.operation != 'CREATE' || variables.misfiled.size() == 0"
    messageExpression: >-
      'annotation ' + variables.misfiled[0] + ' must name a module of family "' + variables.misfiled[0].substring(32) + '"'
  - expression: "request.operation != 'CREATE' || variables.badQuantity.size() == 0"
    messageExpression: "'module resources must have quantity 1: ' + variables.badQuantity.join(',')"
  - expression: "request.operation != 'CREATE' || variables.initModuleKeys.size() == 0"
    messageExpression: "'init containers cannot load modules: ' + variables.initModuleKeys.join(',')"
  - expression: >-
      request.operation != 'UPDATE' || (
        variables.oldAnns.all(k,
          !(k == 'modules.isambard.ac.uk/load' || k == 'modules.isambard.ac.uk/load-containers' || k.startsWith('resolved.modules.isambard.ac.uk/') || k.startsWith('env.modules.isambard.ac.uk/') || k.startsWith('required-plugins.noderesource.dev')) ||
          (k in variables.anns && variables.anns[k] == variables.oldAnns[k])) &&
        variables.anns.all(k,
          !(k == 'modules.isambard.ac.uk/load' || k == 'modules.isambard.ac.uk/load-containers' || k.startsWith('resolved.modules.isambard.ac.uk/') || k.startsWith('env.modules.isambard.ac.uk/') || k.startsWith('required-plugins.noderesource.dev')) ||
          k in variables.oldAnns))
    message: "module annotations (modules.isambard.ac.uk/load, modules.isambard.ac.uk/load-containers, resolved.modules.isambard.ac.uk/*, env.modules.isambard.ac.uk/*, required-plugins.noderesource.dev*) cannot be changed after the pod is created"
  - expression: >-
      request.operation != 'CREATE' || !variables.hasResolved ||
      ('required-plugins.noderesource.dev/pod' in variables.anns &&
       variables.anns['required-plugins.noderesource.dev/pod'] == '["modules"]')
    message: "required-plugins.noderesource.dev/pod must be [\"modules\"] on pods that load modules"
  # NRI's validator prefers /container.<name> and the bare key over /pod,
  # so either would let a module pod start without the plugin.
  - expression: >-
      request.operation != 'CREATE' || !variables.hasResolved ||
      !variables.anns.exists(k, k.startsWith('required-plugins.noderesource.dev') && k != 'required-plugins.noderesource.dev/pod')
    message: "pods that load modules may not set other required-plugins.noderesource.dev annotations"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: pods.modules.isambard.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  policyName: pods.modules.isambard.ac.uk
  validationActions: [Deny]
```

- [ ] **Step 5: Update the pod-consistency policy**

In `deploy/helm/k8s-modules/templates/admission-pod-consistency.yaml`, replace the `matchConditions` entry and the `anns`, `requested` and `isRequested` variables with the following. Keep `isDefault`, `familyKey`, `references`, `isResolved`, `nsAllowed`, `mismatched` and all validations as they are.

```yaml
  matchConditions:
  - name: has-module-fields
    expression: >-
      (has(object.metadata.annotations) && object.metadata.annotations.exists(k,
        k == 'modules.isambard.ac.uk/load' || k.startsWith('resolved.modules.isambard.ac.uk/'))) ||
      object.spec.containers.exists(c, has(c.resources) && has(c.resources.limits) &&
        c.resources.limits.exists(k, k.startsWith('modules.isambard.ac.uk/') || k.startsWith('family.modules.isambard.ac.uk/')))
  variables:
  - name: anns
    expression: "has(object.metadata.annotations) ? object.metadata.annotations : {}"
  - name: requested
    expression: "'modules.isambard.ac.uk/load' in variables.anns ? variables.anns['modules.isambard.ac.uk/load'].split(',').map(s, s.trim()).filter(s, s != '') : []"
  - name: limitKeys
    expression: >-
      object.spec.containers.map(c, has(c.resources) && has(c.resources.limits) ?
        c.resources.limits.filter(k, k.startsWith('modules.isambard.ac.uk/') || k.startsWith('family.modules.isambard.ac.uk/')).join(',') : '')
      .filter(s, s != '').join(',').split(',').filter(s, s != '')
```

Put `isDefault` before `isRequested`, then:

```yaml
  - name: isRequested
    expression: >-
      params.metadata.name in variables.requested || (params.spec.family in variables.requested && variables.isDefault) ||
      ('modules.isambard.ac.uk/' + params.metadata.name) in variables.limitKeys ||
      (variables.isDefault && ('family.modules.isambard.ac.uk/' + params.spec.family) in variables.limitKeys)
```

- [ ] **Step 6: Remove `AnnotationContainers` and update the NRI envtests**

Delete the `AnnotationContainers = "modules.isambard.ac.uk/containers"` line from `api/v1alpha1/module_types.go`.

In `test/envtest/nri_admission_test.go`, replace `TestAdmissionNRIAnnotations` and `TestAdmissionNRIAnnotationsRequired` with:

```go
func TestAdmissionNRIAnnotations(t *testing.T) {
	m := imageModule(newFamily("nriann"), "1")
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	p := admit(t, func() *corev1.Pod {
		p := newPod(ns, m.Name, corev1.Container{Name: "a", Image: "busybox"}, corev1.Container{Name: "b", Image: "busybox"})
		p.Annotations[v1.AnnotationLoadContainers] = "a"
		return p
	}, hasAnnotations(map[string]string{v1.AnnotationRequiredPlugins: v1.RequiredPluginsValue}))

	for key, value := range map[string]string{v1.AnnotationLoadContainers: "a,b", v1.AnnotationRequiredPlugins: "[]"} {
		cur := &corev1.Pod{}
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(p), cur); err != nil {
			t.Fatal(err)
		}
		cur.Annotations[key] = value
		if err := k8s.Update(ctx, cur); err == nil || !strings.Contains(err.Error(), "cannot be changed after the pod is created") {
			t.Errorf("changing %s: err = %v", key, err)
		}
	}

	plain := newPod(ns, "")
	create(t, plain)
	if _, ok := plain.Annotations[v1.AnnotationRequiredPlugins]; ok {
		t.Error("a pod loading no modules must not get NRI annotations")
	}
}

func TestAdmissionNRIAnnotationsRequired(t *testing.T) {
	fam := newFamily("nrireq")
	m := imageModule(fam, "1")
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	key := v1.AnnotationResolvedPrefix + fam
	written := func(extra map[string]string) func() *corev1.Pod {
		return func() *corev1.Pod {
			p := newPod(ns, "")
			p.Annotations[key] = m.Status.Resolved.Annotations[key]
			for k, v := range extra {
				p.Annotations[k] = v
			}
			return p
		}
	}
	reject(t, written(nil), "required-plugins.noderesource.dev/pod must be")
	admit(t, written(map[string]string{v1.AnnotationRequiredPlugins: v1.RequiredPluginsValue}),
		hasAnnotations(m.Status.Resolved.Annotations))
}
```

Run `grep -rn AnnotationContainers --include=*.go .` and fix any remaining references (there should be none outside `spike/`, which is a separate module).

- [ ] **Step 7: Run all envtests**

Run: `make test-envtest`
Expected: every test PASSes, old and new. The old `load`-based tests such as `TestAdmissionSingleLoad` and `TestAdmissionDiamond` still pass, because `load` without `load-containers` targets every container.

If a CEL expression fails to compile, the policy creation error from `installPolicies` names the expression and column. Fix the CEL and ignore the "undefined field" *warnings*.

- [ ] **Step 8: Lint the chart and commit**

Run: `helm lint deploy/helm/k8s-modules`
Expected: `1 chart(s) linted, 0 chart(s) failed`.

```bash
git add deploy/helm/k8s-modules/templates api test/envtest
git commit -m "feat(admission)!: per-container modules from limits, load and load-containers

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: ResourceClaim guard

**Files:**
- Create: `deploy/helm/k8s-modules/templates/admission-resourceclaims.yaml`
- Modify: `test/envtest/policies_test.go` (probe in `waitForPolicies`)
- Test: `test/envtest/claims_test.go`

**Interfaces:**
- Consumes: the DeviceClass name forms from Task 1 (`*.modules.isambard.ac.uk`).
- Produces: user-made ResourceClaims and ResourceClaimTemplates for module classes are rejected with a message containing `module device classes`.

- [ ] **Step 1: Write the failing envtest**

<!-- file: test/envtest/claims_test.go -->
```go
package integration

import (
	"strings"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func claimFor(ns, class string) *resourcev1.ResourceClaim {
	return &resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "c-", Namespace: ns},
		Spec: resourcev1.ResourceClaimSpec{Devices: resourcev1.DeviceClaim{Requests: []resourcev1.DeviceRequest{{
			Name: "m", Exactly: &resourcev1.ExactDeviceRequest{DeviceClassName: class}}}}},
	}
}

func asUser(t *testing.T, user string) client.Client {
	cfg := rest.CopyConfig(restCfg)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: []string{"system:masters", "system:authenticated"}}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestResourceClaimGuard(t *testing.T) {
	ns := newNamespace(t, nil)
	for _, class := range []string{"nccl-2.29.2.modules.isambard.ac.uk", "nccl.family.modules.isambard.ac.uk"} {
		err := k8s.Create(ctx, claimFor(ns, class))
		if err == nil || !strings.Contains(err.Error(), "module device classes") {
			t.Errorf("claim for %s: err = %v", class, err)
		}
	}
	tmpl := &resourcev1.ResourceClaimTemplate{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "t-", Namespace: ns},
		Spec:       resourcev1.ResourceClaimTemplateSpec{Spec: claimFor(ns, "x-1.modules.isambard.ac.uk").Spec},
	}
	if err := k8s.Create(ctx, tmpl); err == nil || !strings.Contains(err.Error(), "module device classes") {
		t.Errorf("template: err = %v", err)
	}
	firstAvail := claimFor(ns, "")
	firstAvail.Spec.Devices.Requests[0].Exactly = nil
	firstAvail.Spec.Devices.Requests[0].FirstAvailable = []resourcev1.DeviceSubRequest{{Name: "a", DeviceClassName: "x-1.modules.isambard.ac.uk"}}
	if err := k8s.Create(ctx, firstAvail); err == nil || !strings.Contains(err.Error(), "module device classes") {
		t.Errorf("firstAvailable: err = %v", err)
	}

	// The scheduler creates the extended-resource claim itself.
	sched := claimFor(ns, "x-1.modules.isambard.ac.uk")
	if err := asUser(t, "system:kube-scheduler").Create(ctx, sched); err != nil {
		t.Errorf("scheduler's claim: %v", err)
	} else {
		_ = k8s.Delete(ctx, sched)
	}
	other := claimFor(ns, "gpu.example.com")
	create(t, other)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `make test-envtest 2>&1 | grep -A3 TestResourceClaimGuard`
Expected: FAIL, `claim for nccl-2.29.2.modules.isambard.ac.uk: err = <nil>`.

- [ ] **Step 3: Write the policy**

<!-- file: deploy/helm/k8s-modules/templates/admission-resourceclaims.yaml -->
```yaml
# Module devices are only requested through container limits, which the pod
# policies check for access and conflicts; the scheduler turns those limits
# into a claim of its own. A user's own claim for a module class would skip
# those checks, so it is denied.
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: resourceclaims.modules.isambard.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  failurePolicy: Fail
  matchConstraints:
    matchPolicy: Equivalent
    resourceRules:
    - {apiGroups: [resource.k8s.io], apiVersions: [v1], operations: [CREATE], resources: [resourceclaims, resourceclaimtemplates]}
  variables:
  - name: requests
    expression: >-
      request.resource.resource == 'resourceclaims' ?
        (has(object.spec.devices) && has(object.spec.devices.requests) ? object.spec.devices.requests : []) :
        (has(object.spec.spec.devices) && has(object.spec.spec.devices.requests) ? object.spec.spec.devices.requests : [])
  - name: usesModules
    expression: >-
      variables.requests.exists(r,
        (has(r.exactly) && r.exactly.deviceClassName.endsWith('.modules.isambard.ac.uk')) ||
        (has(r.firstAvailable) && r.firstAvailable.exists(s, s.deviceClassName.endsWith('.modules.isambard.ac.uk'))))
  validations:
  - expression: >-
      !variables.usesModules ||
      (request.resource.resource == 'resourceclaims' && request.userInfo.username == 'system:kube-scheduler')
    message: "module device classes (*.modules.isambard.ac.uk) can only be used through container resource limits (modules.isambard.ac.uk/<module> or family.modules.isambard.ac.uk/<family>), not in a ResourceClaim or ResourceClaimTemplate"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: resourceclaims.modules.isambard.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  policyName: resourceclaims.modules.isambard.ac.uk
  validationActions: [Deny]
```

- [ ] **Step 4: Add a probe to `waitForPolicies`**

In `test/envtest/policies_test.go`, add this to the `checks` slice:

```go
		{"resourceclaims policy", func() (bool, error) {
			return rejected(&resourcev1.ResourceClaim{
				ObjectMeta: metav1.ObjectMeta{GenerateName: "probe-", Namespace: probeNS},
				Spec: resourcev1.ResourceClaimSpec{Devices: resourcev1.DeviceClaim{Requests: []resourcev1.DeviceRequest{{
					Name: "m", Exactly: &resourcev1.ExactDeviceRequest{DeviceClassName: "probe-1.modules.isambard.ac.uk"}}}}},
			}), nil
		}},
```

Also add the import `resourcev1 "k8s.io/api/resource/v1"`.

- [ ] **Step 5: Run the envtests**

Run: `make test-envtest && helm lint deploy/helm/k8s-modules`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add deploy/helm/k8s-modules/templates/admission-resourceclaims.yaml test/envtest
git commit -m "feat(admission): deny user ResourceClaims for module device classes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Chart and kind plumbing

**Files:**
- Modify: `deploy/helm/k8s-modules/templates/nri-daemonset.yaml`
- Modify: `deploy/helm/k8s-modules/templates/nri-rbac.yaml`
- Modify: `deploy/helm/k8s-modules/templates/rbac.yaml`
- Modify: `test/images/host-libs/src/build.sh`
- Modify: `deploy/kind/setup.sh`. The working tree already has an uncommitted change here (applying CRDs server-side, and the `modules.modules.isambard.ac.uk` wait). Keep it; it is committed together with this task.

**Interfaces:**
- Consumes: the `--cdi-dir` flag default from Task 6; kubeletplugin's default paths (`/var/lib/kubelet/plugins/modules.isambard.ac.uk`, `/var/lib/kubelet/plugins_registry`).
- Produces:
  - a kind cluster where module-nri registers as a DRA driver;
  - `/usr/lib/brics-late/libfakelate.so.1` on every node, deliberately not in `ld.so.conf`;
  - discovery every 15 s.

- [ ] **Step 1: DaemonSet mounts**

In `nri-daemonset.yaml`, append to the `module-nri` container's `volumeMounts`:

```yaml
        - {name: kubelet-plugin, mountPath: /var/lib/kubelet/plugins/modules.isambard.ac.uk}
        - {name: kubelet-registry, mountPath: /var/lib/kubelet/plugins_registry}
        - {name: cdi, mountPath: /var/run/cdi}
```

Append to `volumes` (before the `range` over pvcs):

```yaml
      # The plugin's socket directory must exist before it starts.
      - {name: kubelet-plugin, hostPath: {path: /var/lib/kubelet/plugins/modules.isambard.ac.uk, type: DirectoryOrCreate}}
      - {name: kubelet-registry, hostPath: {path: /var/lib/kubelet/plugins_registry, type: Directory}}
      - {name: cdi, hostPath: {path: /var/run/cdi, type: DirectoryOrCreate}}
```

- [ ] **Step 2: RBAC**

Append to the rules in `nri-rbac.yaml`'s ClusterRole:

```yaml
- apiGroups: [resource.k8s.io]
  resources: [resourceslices]
  verbs: [get, list, watch, create, update, patch, delete]
- apiGroups: [resource.k8s.io]
  resources: [resourceclaims]
  verbs: [get]
- apiGroups: [""]
  resources: [pods]
  verbs: [get]
```

Append to the rules in `rbac.yaml`'s controller ClusterRole:

```yaml
- apiGroups: [resource.k8s.io]
  resources: [deviceclasses]
  verbs: [get, list, watch, create, update, patch, delete]
```

- [ ] **Step 3: The late library**

Append to `test/images/host-libs/src/build.sh`:

```sh
# libfakelate lives outside ld.so.conf: the readiness e2e adds its directory
# to prove pods wait for a host module to become ready.
L=/out/host/usr/lib/brics-late
mkdir -p "$L"
gcc -shared -fPIC -DFAKEDEP_VARIANT='"late"' -Wl,-soname,libfakelate.so.1 -o "$L/libfakelate.so.1.0.0" /src/fakedep.c
ln -s libfakelate.so.1.0.0 "$L/libfakelate.so.1"
```

- [ ] **Step 4: `setup.sh`**

In step `1b`, after `$CONTAINER_CLI cp "$CID:/usr/lib/brics-test" "$LIBS/"`, add:

```bash
$CONTAINER_CLI cp "$CID:/usr/lib/brics-late" "$LIBS/"
```

Inside the per-node loop, after the `brics-test` copy lines, add:

```bash
  $CONTAINER_CLI exec "$node" rm -rf /usr/lib/brics-late /etc/ld.so.conf.d/brics-late.conf
  $CONTAINER_CLI cp "$LIBS/brics-late" "$node:/usr/lib/brics-late"
```

These lines must come before the existing `ldconfig` so the cache never lists `brics-late`.

In the `helm upgrade --install` command, add:

```bash
  --set nri.discoveryInterval=15s \
```

- [ ] **Step 5: Rebuild the kind deployment and check the driver registers**

Run:
```bash
export KIND_EXPERIMENTAL_PROVIDER=podman CONTAINER_CLI=podman
make kind-up
kubectl get resourceslices -o wide
kubectl get deviceclasses
kubectl -n module-system logs ds/module-nri --tail=20
```

Expected:
- `make kind-up` succeeds;
- there is one ResourceSlice per node, driver `modules.isambard.ac.uk`, listing `nccl-2.29.2`'s device;
- the DeviceClasses `nccl-2.29.2.modules.isambard.ac.uk` and `nccl.family.modules.isambard.ac.uk` exist;
- the module-nri log has no errors.

If the slice is missing, the module-nri log shows the kubelet plugin error. RBAC `forbidden` means Step 2 is incomplete. A socket error means Step 1 is incomplete.

- [ ] **Step 6: Commit**

```bash
git add deploy/helm/k8s-modules/templates test/images/host-libs deploy/kind/setup.sh
git commit -m "feat(chart): run module-nri as a DRA kubelet plugin

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: e2e on kind

**Files:**
- Create: `test/e2e/dra_test.go`
- Modify: `test/e2e/nri_test.go` (replace `TestNRIFailClosed`)

**Interfaces:**
- Consumes:
  - from Task 10: the kind cluster, `nccl-2.29.2` (the default for family `nccl`), `libfakelate` in `/usr/lib/brics-late`, and the 15 s discovery interval;
  - existing e2e helpers: `newClient`, `namespace`, `module`, `createModule`, `hostFabric`, `runPodSpec`, `waitFor`, `ncclImage`, `clientImage`.
- Produces: e2e coverage for the spec's testing list.

- [ ] **Step 1: Write the new e2e tests**

<!-- file: test/e2e/dra_test.go -->
```go
//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
)

const busybox = "docker.io/library/busybox:1.37"

func limits(keys ...string) corev1.ResourceRequirements {
	l := corev1.ResourceList{}
	for _, k := range keys {
		l[corev1.ResourceName(k)] = resource.MustParse("1")
	}
	return corev1.ResourceRequirements{Limits: l}
}

func sh(name, image, script string, res corev1.ResourceRequirements) corev1.Container {
	return corev1.Container{Name: name, Image: image, Command: []string{"sh", "-ec", script}, Resources: res}
}

func multiPod(ns, name string, anns map[string]string, cs ...corev1.Container) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Annotations: anns},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: cs}}
}

const (
	hasNCCL = `test -d /opt/modules/nccl/2.29.2
case "$LD_LIBRARY_PATH" in /opt/modules/nccl/2.29.2/lib*) ;; *) echo "LD_LIBRARY_PATH=$LD_LIBRARY_PATH"; exit 1;; esac
test "$BRICS_MODULES" = nccl-2.29.2`
	noModules = `test ! -e /opt/modules/nccl
test -z "${BRICS_MODULES:-}"
! env | grep -q '^BRICS_MODULE_'`
)

func TestDRAPerContainer(t *testing.T) {
	c := newClient(t)
	ns := namespace(t, c, "e2e-dra-per-container")
	runPodSpec(t, c, multiPod(ns, "per-container", nil,
		sh("trainer", busybox, hasNCCL, limits("modules.isambard.ac.uk/nccl-2.29.2")),
		sh("sidecar", busybox, noModules, corev1.ResourceRequirements{})))
}

func TestDRALoadContainers(t *testing.T) {
	c := newClient(t)
	ns := namespace(t, c, "e2e-dra-load-containers")
	runPodSpec(t, c, multiPod(ns, "load-containers",
		map[string]string{v1.AnnotationLoad: "nccl", v1.AnnotationLoadContainers: "trainer"},
		sh("trainer", busybox, hasNCCL, corev1.ResourceRequirements{}),
		sh("sidecar", busybox, noModules, corev1.ResourceRequirements{})))
}

func TestDRAFamilyLimit(t *testing.T) {
	c := newClient(t)
	ns := namespace(t, c, "e2e-dra-family")
	runPodSpec(t, c, multiPod(ns, "family", nil,
		sh("trainer", busybox, hasNCCL, limits("family.modules.isambard.ac.uk/nccl"))))
}

// One container requests two modules and a dependency of each. Every file
// is bound once (a duplicate CDI mount would fail container creation) and
// each library dir appears once, in rank order.
func TestDRAOverlappingClosures(t *testing.T) {
	c := newClient(t)
	img := ncclImage(t, c)
	createModule(t, c, hostFabric("host"))
	module(t, c, "e2e-ofi", "1", img, "nccl-2.29.2")
	module(t, c, "e2e-fabuser", "1", img, "fakefabric-host")
	waitReason(t, c, "e2e-ofi-1", "Resolved")
	waitReason(t, c, "e2e-fabuser-1", "Resolved")
	ns := namespace(t, c, "e2e-dra-overlap")
	runPodSpec(t, c, multiPod(ns, "overlap", nil, sh("check", clientImage, `
test "$BRICS_MODULES" = "e2e-fabuser-1:e2e-ofi-1:fakefabric-host:nccl-2.29.2" || { echo "BRICS_MODULES=$BRICS_MODULES"; exit 1; }
if echo "$LD_LIBRARY_PATH" | tr : '\n' | sort | uniq -d | grep .; then echo "duplicate in LD_LIBRARY_PATH=$LD_LIBRARY_PATH"; exit 1; fi
out=$(fakefabric-check); echo "$out"; test "$out" = fakedep=image`,
		limits("modules.isambard.ac.uk/nccl-2.29.2", "modules.isambard.ac.uk/e2e-ofi-1",
			"modules.isambard.ac.uk/fakefabric-host", "modules.isambard.ac.uk/e2e-fabuser-1"))))
}

// nodeShell runs script as root in the node's own filesystem on node and
// waits for it to succeed.
func nodeShell(t *testing.T, c client.Client, ns, node, script string) {
	t.Helper()
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "node-shell-", Namespace: ns},
		Spec: corev1.PodSpec{
			NodeName: node, RestartPolicy: corev1.RestartPolicyNever,
			Tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
			Containers: []corev1.Container{{Name: "sh", Image: busybox,
				Command:         []string{"chroot", "/host", "sh", "-ec", script},
				SecurityContext: &corev1.SecurityContext{Privileged: ptr.To(true)},
				VolumeMounts:    []corev1.VolumeMount{{Name: "host", MountPath: "/host"}}}},
			Volumes: []corev1.Volume{{Name: "host", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/"}}}},
		},
	}
	if err := c.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	defer c.Delete(ctx, p)
	waitFor(t, 2*time.Minute, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			return err
		}
		switch p.Status.Phase {
		case corev1.PodSucceeded:
			return nil
		case corev1.PodFailed:
			t.Fatalf("node shell on %s failed: %s", node, podLogs(p))
		}
		return fmt.Errorf("node shell phase %s", p.Status.Phase)
	})
}

func unschedulable(c client.Client, p *corev1.Pod) error {
	if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
		return err
	}
	if p.Spec.NodeName != "" {
		return fmt.Errorf("pod was scheduled to %s", p.Spec.NodeName)
	}
	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == corev1.PodReasonUnschedulable {
			return nil
		}
	}
	return fmt.Errorf("pod not marked unschedulable yet: %+v", p.Status.Conditions)
}

// A pod needing a host module that no node has stays Pending, then runs
// once the library appears and the next discovery republishes the device.
func TestDRAHostReadiness(t *testing.T) {
	c := newClient(t)
	createModule(t, c, &v1.Module{ObjectMeta: metav1.ObjectMeta{Name: "fakelate-1"},
		Spec: v1.ModuleSpec{Family: "fakelate", Version: "1", Source: v1.ModuleSource{Host: &v1.HostSource{
			Discover: v1.HostDiscover{Roots: []string{"libfakelate.so.1"}}}}}})
	ns := namespace(t, c, "e2e-dra-ready")
	nodes := &corev1.NodeList{}
	if err := c.List(ctx, nodes); err != nil {
		t.Fatal(err)
	}
	setLate := func(on bool) {
		script := `rm -f /etc/ld.so.conf.d/brics-late.conf && ldconfig`
		if on {
			script = `echo /usr/lib/brics-late > /etc/ld.so.conf.d/brics-late.conf && ldconfig`
		}
		for _, n := range nodes.Items {
			nodeShell(t, c, ns, n.Name, script)
		}
	}
	setLate(false)
	t.Cleanup(func() { setLate(false) })

	p := multiPod(ns, "late", nil, sh("check", busybox, `test -f /opt/modules/fakelate/1/lib/libfakelate.so.1`,
		limits("modules.isambard.ac.uk/fakelate-1")))
	waitFor(t, 30*time.Second, func() error { return c.Create(ctx, p) })
	t.Cleanup(func() { _ = c.Delete(ctx, p) })
	waitFor(t, time.Minute, func() error { return unschedulable(c, p) })
	time.Sleep(30 * time.Second) // two discovery intervals
	if err := unschedulable(c, p); err != nil {
		t.Fatalf("pod scheduled before the library existed: %v", err)
	}

	setLate(true)
	waitFor(t, 3*time.Minute, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			return err
		}
		switch p.Status.Phase {
		case corev1.PodSucceeded:
			return nil
		case corev1.PodFailed:
			t.Fatalf("pod failed: %s", podLogs(p))
		}
		return fmt.Errorf("pod phase %s %+v", p.Status.Phase, p.Status.Conditions)
	})
}
```

- [ ] **Step 2: Replace `TestNRIFailClosed`**

In `test/e2e/nri_test.go`, rename `TestNRIFailClosed` to `TestDRAFailClosed` and replace the part after the "module-nri pods still present" `waitFor` with:

```go
	// With module-nri gone, the kubelet drops its ResourceSlices, so a pod
	// that needs a module is never scheduled.
	waitFor(t, 3*time.Minute, func() error {
		slices := &resourcev1.ResourceSliceList{}
		if err := c.List(ctx, slices); err != nil {
			return err
		}
		for _, s := range slices.Items {
			if s.Spec.Driver == v1.DriverName {
				return fmt.Errorf("slice %s still present", s.Name)
			}
		}
		return nil
	})

	ns := namespace(t, c, "e2e-dra-closed")
	p := shellPod(ns, "needs-driver", "docker.io/library/busybox:1.37", "nccl-2.29.2", "true")
	waitFor(t, 30*time.Second, func() error { return c.Create(ctx, p) })
	t.Cleanup(func() { _ = c.Delete(ctx, p) })
	waitFor(t, time.Minute, func() error { return unschedulable(c, p) })

	plain := shellPod(ns, "plain", "docker.io/library/busybox:1.37", "", "true")
	delete(plain.Annotations, v1.AnnotationLoad)
	runPodSpec(t, c, plain)
```

Add the import `resourcev1 "k8s.io/api/resource/v1"` to `nri_test.go`. The comment above the function should say: "Without module-nri there is no driver on the node, so module pods stay Pending; `required-plugins` still guards the case where the driver runs but NRI is disconnected."

- [ ] **Step 3: Run the whole e2e suite**

Run: `make test-e2e 2>&1 | tee /tmp/e2e.log | grep -E '^(=== RUN|--- (PASS|FAIL)|ok|FAIL)'`
Expected: every test PASSes. That covers the existing `TestSingleModule`, `TestFamilyDefault`, `TestDiamond`, `TestConflict`, `TestNRI*` and `TestDRAFailClosed`, and the new `TestDRA*`.

If `TestDRAFailClosed` times out waiting for slices to go, check `kubectl get resourceslices` after the DaemonSet is gone. If the kubelet keeps them, the driver must delete its slices on shutdown instead: call `helper.PublishResources(ctx, resourceslice.DriverResources{})` in a SIGTERM path before `helper.Stop()`, and give the DaemonSet `terminationGracePeriodSeconds: 30`. Report which case applied.

For any failure, collect the pod's events (`kubectl -n <ns> describe pod <name>`) and `kubectl -n module-system logs ds/module-nri`, and use superpowers:systematic-debugging before changing code.

- [ ] **Step 4: Commit**

```bash
git add test/e2e
git commit -m "test(e2e): per-container modules, family limits, overlap, readiness

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: User docs

**Files:**
- Modify: `docs/writing-modules.md`
- Modify: `deploy/helm/k8s-modules/README.md`

**Interfaces:**
- Consumes: the finished behaviour from Tasks 1–11.
- Produces: user- and admin-facing documentation.

- [ ] **Step 1: Find every place that describes loading**

Run: `grep -n "load\|containers\|nodeSelector\|host-" docs/writing-modules.md deploy/helm/k8s-modules/README.md`

- [ ] **Step 2: Update `docs/writing-modules.md`**

Rewrite the "how a user loads a module" part so it shows both forms:

````markdown
A container asks for a module in its resource limits. Only that container
gets the module (and everything it depends on):

```yaml
containers:
- name: trainer
  resources:
    limits:
      modules.isambard.ac.uk/aws-ofi-nccl-1.18.0: 1   # an exact module
      family.modules.isambard.ac.uk/nccl: 1           # a family's default
- name: sidecar                                        # gets no modules
```

The `load` annotation is a shortcut that adds those limits to every
container, or only to the containers `load-containers` lists:

```yaml
metadata:
  annotations:
    modules.isambard.ac.uk/load: aws-ofi-nccl-1.18.0,nccl
    modules.isambard.ac.uk/load-containers: trainer   # optional
```

The scheduler only places the pod on nodes where every requested module can
run, so host modules no longer need a `nodeSelector`. Init containers can't
load modules.
````

Update the "What happens next" explanation:
- the controller creates a DeviceClass per Module and per family;
- module-nri publishes a device per ready Module and, at container start, binds host and PVC files and sets `BRICS_MODULE_*` markers through CDI;
- NRI builds `LD_LIBRARY_PATH` and `BRICS_MODULES` from those markers.

Remove any mention of the `modules.isambard.ac.uk/containers` annotation. Say that the `modules.isambard.ac.uk/host-<family>` node labels still exist but aren't needed for scheduling.

- [ ] **Step 3: Update the chart README**

Add a "Requirements" note: Kubernetes 1.36+ with DRA Extended Resources (beta, on by default in 1.36) and containerd with CDI enabled (the default in containerd 2). Say that module-nri is now also a DRA kubelet plugin and needs the kubelet plugin directories, and note the new ResourceClaim policy.

- [ ] **Step 4: Commit**

```bash
git add docs/writing-modules.md deploy/helm/k8s-modules/README.md
git commit -m "docs: requesting modules per container with limits

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-review notes

- **Spec coverage:**

  | Spec section | Task |
  |---|---|
  | User API | 8, 12 |
  | Names | 1, 7 |
  | Mutating and validating policies | 8 |
  | Claim guard | 9 |
  | Driver slices | 3, 6 |
  | Driver prepare | 4, 5 |
  | NRI | 2 |
  | Controller | 7 |
  | Chart | 10 |
  | Testing | 2–11 |
  | Error-handling table rows | 4 (moved default, host not ready, unknown device), 8 (unknown, access, init), 9 (user claim), 11 (not ready anywhere, driver absent) |
- **Known deviation:** the spec's "a host module that becomes not ready after scheduling" row is covered by `TestPlanHostNotReady` at unit level. There is no e2e for it.
