# Module System Phase 3 (NRI) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Every code block that creates a file is preceded by `<!-- file: <path> -->`; blocks without that marker are commands or excerpts.

**Goal:** Build the `module-nri` NRI plugin and the `module-hook` OCI hook.
- The plugin applies modules at container creation and checks each pod's annotations against the Module catalogue.
- It discovers host libraries and reports them per node.
- Pods fail closed when the plugin is absent.
- Everything is tested with stub libraries in kind.

**Architecture:**
- **Two new pure packages.** `internal/discovery` parses `ld.so.cache` and walks ELF dependencies. `internal/hook` edits the ld config inside an image rootfs and runs the glibc check. They sit alongside the small helpers `internal/fsutil` and `internal/glibc`.
- **`internal/nri`** turns pod annotations and discovery results into NRI container adjustments, and runs discovery and report writing.
- **Two binaries.** `cmd/module-nri` (DaemonSet) and `cmd/module-hook` (OCI `createContainer` hook) ship in one image.
- **Controller and chart.** The phase-2 controller gains a node-label reconciler. The chart gains the DaemonSet, RBAC and one ValidatingAdmissionPolicy, and the phase-2 mutating and pods policies change.

**Tech Stack:** Go 1.26, `github.com/containerd/nri` v0.12.0 (stub API), `debug/elf`, controller-runtime v0.24.1 cache/client, Helm, kind v1.36.4 with containerd 2.3.4 on podman.

**Spec:** `docs/superpowers/specs/2026-09-25-module-system-phase3-nri-design.md` (builds on `docs/superpowers/specs/2026-09-25-module-system-phase1-2-design.md`)

## Global Constraints

- **NRI registration:** plugin name `modules`, index `50`. Fail-closed annotation: `required-plugins.noderesource.dev/pod: '["modules"]'`. The spike confirmed this key; `nri.io` is wrong.
- **New annotation:** `modules.brics.ac.uk/containers`, a comma-joined list of `spec.containers` names.
- **Node label:** `modules.brics.ac.uk/host-<family>: "true"`.
- **Excluded glibc sonames:** `libc.so.6`, `libm.so.6`, `libpthread.so.0`, `libdl.so.2`, `librt.so.1`, `ld-linux*`.
- **Mount options:**
  - host file binds: `bind,ro,nosuid,nodev,rprivate`;
  - PVC binds: `rbind,ro,nosuid,nodev,rprivate`;
  - masks: tmpfs `ro,nosuid,nodev,noexec,size=0`.
- **Hook conf:** it writes `/etc/ld.so.conf.d/zz-brics-modules.conf`, listing the existing trusted dirs (`/lib64`, `/usr/lib64`, `/lib`, `/usr/lib`) first, then the `deps` dirs.
- **Namespace:** the DaemonSet runs in `.Values.modulesNamespace`.
- **Build flags:** `CGO_ENABLED=0` everywhere (Makefile exports it). Container images build for the node architecture; kind here is arm64.
- **Plugin timing:** containerd allows 2 s per plugin request, so CreateContainer does no I/O beyond reading two small mountinfo files for PVC modules.

## Spike results this plan relies on (kind v1.36.4, containerd 2.3.4)

- **Adjustments work.** `adj.AddEnv`, tmpfs `AddMount`, a single-file bind `AddMount`, and `AddHooks` with a `createContainer` hook all apply.
- **The hook's environment.** The hook runs as root with the bundle path from stdin JSON. `config.json` `root.path` is `"rootfs"` (relative to the bundle). A child process with `SysProcAttr.Chroot` running the image's `/sbin/ldconfig` succeeds, and `ldconfig -p` in the container then lists the bound library.
- **Fail-closed.** With `default_validator.enable = true`, a pod annotated `required-plugins.noderesource.dev/pod: '["modules"]'` gets `CreateContainerError: ... required plugin "modules" not present` while the plugin is down, and starts once the plugin connects.
- **ld cache order.** `ldconfig` scans trusted dirs last. Listing `/lib` and `/usr/lib` before the deps dir in the conf file makes the image's `/usr/lib` copy win.
- **Cache format.** The kind node's `/etc/ld.so.cache` is `glibc-ld.so.cache1.1`: a 48-byte header, then 24-byte entries (flags `0x0a03` for AArch64 libc6), with string offsets measured from the start of the file.

## Review Focus

1. **Container restart:** a container that restarts inside the same pod must get the same adjustments. `Adjust` is a pure function of pod, container and state; covered by `TestAdjustDeterministic` in Task 6.
2. **Existing `LD_LIBRARY_PATH`:** an image `LD_LIBRARY_PATH` containing one of the module dirs must not produce a duplicate entry. Covered by `TestPrependLibraryPath` in Task 6.
3. **Missing deps dir:** a host module with no `deps` must not give the hook a nonexistent deps dir. Covered by `TestAdjustHostModuleWithoutDeps` in Task 6.
4. **Several host modules in one pod:** one hook with all deps dirs and glibc arguments. Covered by `TestAdjustTwoHostModules` in Task 6.
5. **Plain pods on a node with the plugin:** they get no adjustment at all. Covered by `TestAdjustIgnoresPlainPods` in Task 6 and e2e test 7's plain pod.

## Ruling carried from the spec

- **e2e creates its own Modules:** e2e tests create `fakefabric-host`, `fakefabric-prefer` and the other test Modules themselves, instead of `setup.sh` creating them. This keeps each test self-contained, like the phase-2 e2e tests.

---

## File map

| Path | Responsibility |
|---|---|
| `api/v1alpha1/module_types.go` | `HostSource.PreferHost`, new constants |
| `api/v1alpha1/modulenodereport_types.go` | `ModuleNodeReport` kind |
| `internal/fsutil/resolve.go` | resolve a path inside a root, following symlinks, refusing `..` escapes |
| `internal/glibc/glibc.go` | glibc soname set, `GLIBC_x.y` parsing and comparison, multiarch names |
| `internal/discovery/ldcache.go` | `ld.so.cache` parser |
| `internal/discovery/discover.go` | closure walk, placement, hash |
| `internal/hook/hook.go`, `runner.go` | rootfs conf edit, ldconfig run, glibc check, OCI state parsing |
| `cmd/module-hook/main.go` | hook binary |
| `internal/nri/value.go` | resolved-annotation value parser |
| `internal/nri/ldpath.go` | `PrependLibraryPath` |
| `internal/nri/adjust.go` | `Adjuster.Adjust` |
| `internal/nri/mountinfo.go` | PVC host-path resolution |
| `internal/nri/state.go`, `catalogue.go`, `report.go`, `plugin.go` | runtime glue |
| `cmd/module-nri/main.go` | plugin binary and `install-hook` subcommand |
| `internal/controller/nodelabel_controller.go` | reports → node labels |
| `deploy/helm/k8s-modules/templates/*` | DaemonSet, RBAC, report policy, MAP/pods policy changes |
| `test/images/host-libs/**` | stub library sources, Dockerfile (host-libs, client, client-old targets) |
| `hack/gen-testdata.sh`, `hack/testdata.sh` | fixture generator |
| `test/e2e/nri_test.go` | kind scenarios |

---

### Task 1: API additions

**Files:**
- Modify: `api/v1alpha1/module_types.go`
- Create: `api/v1alpha1/modulenodereport_types.go`, `test/envtest/nri_crd_test.go`
- Generate: deepcopy and CRDs (`make generate manifests`)

**Interfaces:**
- Produces:
  - `v1.HostSource.PreferHost []string`;
  - `v1.ModuleNodeReport`, `ModuleNodeReportList`, `ModuleNodeReportStatus{DiscoveredAt *metav1.Time; Modules []HostModuleReport}`;
  - `HostModuleReport{Module, Family string; Ready bool; ClosureHash, GlibcRequired string; Lib, Deps []LibraryFile; Missing []string; Message string}`;
  - `LibraryFile{Soname, Path string}`;
  - constants `AnnotationContainers`, `AnnotationRequiredPlugins`, `RequiredPluginsValue`, `NRIPluginName`, `LabelHostPrefix`.

- [ ] **Step 1: Failing envtest**

<!-- file: test/envtest/nri_crd_test.go -->
```go
package integration

import (
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

func hostModule(family string, prefer ...string) *v1.Module {
	m := imageModule(family, "host")
	m.Spec.Source = v1.ModuleSource{Host: &v1.HostSource{
		Discover:   v1.HostDiscover{Roots: []string{"libx.so.1"}},
		PreferHost: prefer,
	}}
	return m
}

func TestCRDPreferHost(t *testing.T) {
	create(t, hostModule(newFamily("pref"), "libjson-c.so.5"))
	expectCreateError(t, hostModule(newFamily("pref"), "/usr/lib/libx.so"), "spec.source.host.preferHost")
}

func TestModuleNodeReportCRD(t *testing.T) {
	r := &v1.ModuleNodeReport{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("crd-node-%d", familySeq.Add(1))}}
	create(t, r)
	now := metav1.Now()
	r.Status = v1.ModuleNodeReportStatus{DiscoveredAt: &now, Modules: []v1.HostModuleReport{{
		Module: "x-host", Family: "x", Ready: true, ClosureHash: "sha256:00", GlibcRequired: "2.34",
		Lib: []v1.LibraryFile{{Soname: "libx.so.1", Path: "/usr/lib/libx.so.1"}},
	}}}
	if err := k8s.Status().Update(ctx, r); err != nil {
		t.Fatal(err)
	}
	got := &v1.ModuleNodeReport{}
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(r), got); err != nil {
		t.Fatal(err)
	}
	if len(got.Status.Modules) != 1 || got.Status.Modules[0].Lib[0].Soname != "libx.so.1" {
		t.Errorf("status not stored: %+v", got.Status)
	}
}
```

- [ ] **Step 2: Run** `make test-envtest`. Expected: build failure, `undefined: v1.ModuleNodeReport` and `unknown field PreferHost`.

- [ ] **Step 3: Types.** In `module_types.go`, add to the `const` block:

```go
	AnnotationContainers      = "modules.brics.ac.uk/containers"
	AnnotationRequiredPlugins = "required-plugins.noderesource.dev/pod"
	RequiredPluginsValue      = `["modules"]`
	NRIPluginName             = "modules"
	LabelHostPrefix           = "modules.brics.ac.uk/host-"
```

and replace `HostSource` with:

```go
type HostSource struct {
	Discover HostDiscover `json:"discover"`
	// PreferHost lists dependency sonames placed in lib/ (prepended to
	// LD_LIBRARY_PATH) instead of deps/, for when the image's copy is known
	// to be incompatible.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:items:Pattern=`^lib[A-Za-z0-9_.+-]+\.so(\.[0-9]+)*$`
	PreferHost []string `json:"preferHost,omitempty"`
}
```

<!-- file: api/v1alpha1/modulenodereport_types.go -->
```go
package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ModuleNodeReport records what the module NRI plugin discovered on one
// node. It is named after the node and owned by it.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=mnr
// +kubebuilder:printcolumn:name="Discovered",type=date,JSONPath=`.status.discoveredAt`
type ModuleNodeReport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Status ModuleNodeReportStatus `json:"status,omitempty"`
}

type ModuleNodeReportStatus struct {
	// +optional
	DiscoveredAt *metav1.Time `json:"discoveredAt,omitempty"`
	// +optional
	// +listType=map
	// +listMapKey=module
	Modules []HostModuleReport `json:"modules,omitempty"`
}

// HostModuleReport is the discovered closure of one host Module.
type HostModuleReport struct {
	Module string `json:"module"`
	Family string `json:"family"`
	Ready  bool   `json:"ready"`
	// +optional
	ClosureHash string `json:"closureHash,omitempty"`
	// +optional
	GlibcRequired string `json:"glibcRequired,omitempty"`
	// +optional
	Lib []LibraryFile `json:"lib,omitempty"`
	// +optional
	Deps []LibraryFile `json:"deps,omitempty"`
	// +optional
	Missing []string `json:"missing,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
}

type LibraryFile struct {
	Soname string `json:"soname"`
	Path   string `json:"path"`
}

// +kubebuilder:object:root=true
type ModuleNodeReportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModuleNodeReport `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ModuleNodeReport{}, &ModuleNodeReportList{})
}
```

- [ ] **Step 4: Run** `make generate manifests test-envtest`. Expected: `deploy/helm/k8s-modules/crds/modules.brics.ac.uk_modulenodereports.yaml` exists, and the whole envtest suite passes.

- [ ] **Step 5: Commit** — `git add api deploy/helm/k8s-modules/crds test/envtest/nri_crd_test.go && git commit -m "feat(api): ModuleNodeReport and host preferHost"`

---

### Task 2: Path resolution and glibc helpers

**Files:**
- Create: `internal/fsutil/resolve.go`, `internal/fsutil/resolve_test.go`, `internal/glibc/glibc.go`, `internal/glibc/glibc_test.go`

**Interfaces:**
- Produces:
  - `fsutil.ResolveIn(root, p string) (string, error)`: returns the real in-root absolute path. An escape wraps `fsutil.ErrEscape`.
  - `fsutil.Host(root, p string) string`
  - `glibc.Excluded(soname string) bool`
  - `glibc.Version(name string) (string, bool)`: turns `GLIBC_2.36` into `2.36`.
  - `glibc.Less(a, b string) bool`: `""` compares lowest.
  - `glibc.Multiarch(goarch string) string`

- [ ] **Step 1: Failing tests**

<!-- file: internal/fsutil/resolve_test.go -->
```go
package fsutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveIn(t *testing.T) {
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "usr/lib/x"), 0o755))
	must(os.WriteFile(filepath.Join(root, "usr/lib/x/libreal.so.1.2"), []byte("elf"), 0o644))
	must(os.Symlink("libreal.so.1.2", filepath.Join(root, "usr/lib/x/libreal.so.1")))
	must(os.Symlink("usr/lib", filepath.Join(root, "lib")))                               // relative dir link
	must(os.Symlink("/usr/lib/x/libreal.so.1", filepath.Join(root, "usr/lib/abs.so")))     // absolute: stays in root
	must(os.Symlink("../../../../etc/passwd", filepath.Join(root, "usr/lib/x/escape.so"))) // climbs above root
	must(os.Symlink("loop2", filepath.Join(root, "loop1")))
	must(os.Symlink("loop1", filepath.Join(root, "loop2")))

	cases := []struct{ in, want string }{
		{"/usr/lib/x/libreal.so.1", "/usr/lib/x/libreal.so.1.2"},
		{"/lib/x/libreal.so.1", "/usr/lib/x/libreal.so.1.2"},
		{"/usr/lib/abs.so", "/usr/lib/x/libreal.so.1.2"},
		{"/usr/lib/x/../x/libreal.so.1.2", "/usr/lib/x/libreal.so.1.2"},
	}
	for _, c := range cases {
		got, err := ResolveIn(root, c.in)
		if err != nil || got != c.want {
			t.Errorf("ResolveIn(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if _, err := ResolveIn(root, "/usr/lib/x/escape.so"); !errors.Is(err, ErrEscape) {
		t.Errorf("escape: err = %v, want ErrEscape", err)
	}
	if _, err := ResolveIn(root, "/missing/lib.so"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing: err = %v, want ErrNotExist", err)
	}
	if _, err := ResolveIn(root, "/loop1"); err == nil {
		t.Error("symlink loop must fail")
	}
	if Host(root, "/usr/lib") != filepath.Join(root, "usr/lib") {
		t.Error("Host join")
	}
}
```

<!-- file: internal/glibc/glibc_test.go -->
```go
package glibc

import "testing"

func TestVersion(t *testing.T) {
	for in, want := range map[string]string{"GLIBC_2.36": "2.36", "GLIBC_2.2.5": "2.2.5"} {
		if got, ok := Version(in); !ok || got != want {
			t.Errorf("Version(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"GLIBC_PRIVATE", "GLIBCXX_3.4", "GLIBC_", "GLIBC_2.x"} {
		if _, ok := Version(in); ok {
			t.Errorf("Version(%q) must be false", in)
		}
	}
}

func TestLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"", "2.17", true}, {"2.17", "", false}, {"2.9", "2.17", true}, {"2.17", "2.9", false},
		{"2.2.5", "2.3", true}, {"2.36", "2.36", false}, {"2.36", "2.36.1", true},
	}
	for _, c := range cases {
		if got := Less(c.a, c.b); got != c.want {
			t.Errorf("Less(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestExcluded(t *testing.T) {
	for _, s := range []string{"libc.so.6", "libm.so.6", "libpthread.so.0", "libdl.so.2", "librt.so.1", "ld-linux-aarch64.so.1", "ld-linux-x86-64.so.2"} {
		if !Excluded(s) {
			t.Errorf("%s must be excluded", s)
		}
	}
	if Excluded("libfabric.so.1") || Excluded("libcrypt.so.1") {
		t.Error("non-glibc libraries must not be excluded")
	}
}

func TestMultiarch(t *testing.T) {
	if Multiarch("arm64") != "aarch64-linux-gnu" || Multiarch("amd64") != "x86_64-linux-gnu" || Multiarch("riscv64") != "" {
		t.Error("multiarch names")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/fsutil/ ./internal/glibc/`. Expected: FAIL, undefined names.

- [ ] **Step 3: Implement**

<!-- file: internal/fsutil/resolve.go -->
```go
// Package fsutil resolves paths inside a directory tree that stands in for
// another filesystem's root, such as a host root or a container rootfs.
package fsutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ErrEscape reports a path whose ".." components climb above the root.
var ErrEscape = errors.New("path escapes root")

const maxSymlinks = 40

// ResolveIn resolves p, an absolute path as seen inside root, to its real
// path inside root. Symlinks are followed as the kernel would if root were
// "/": absolute targets restart at root. Unlike the kernel, a ".." above
// root is an error rather than being clamped, so a link can never name a
// file outside the tree.
func ResolveIn(root, p string) (string, error) {
	pending := strings.Split(p, "/")
	cur := "/"
	links := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			if cur == "/" {
				return "", fmt.Errorf("%s: %w", p, ErrEscape)
			}
			cur = path.Dir(cur)
			continue
		}
		next := path.Join(cur, part)
		fi, err := os.Lstat(Host(root, next))
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			cur = next
			continue
		}
		if links++; links > maxSymlinks {
			return "", fmt.Errorf("%s: too many levels of symbolic links", p)
		}
		target, err := os.Readlink(Host(root, next))
		if err != nil {
			return "", err
		}
		if path.IsAbs(target) {
			cur = "/"
		}
		pending = append(strings.Split(target, "/"), pending...)
	}
	return cur, nil
}

// Host returns the path under root for p, an absolute in-root path.
func Host(root, p string) string {
	return filepath.Join(root, filepath.FromSlash(p))
}
```

<!-- file: internal/glibc/glibc.go -->
```go
// Package glibc knows which libraries belong to glibc and how its symbol
// versions compare.
package glibc

import (
	"strconv"
	"strings"
)

var sonames = map[string]bool{
	"libc.so.6": true, "libm.so.6": true, "libpthread.so.0": true, "libdl.so.2": true, "librt.so.1": true,
}

// Excluded reports whether soname is part of glibc. Host glibc must never
// enter a container.
func Excluded(soname string) bool {
	return sonames[soname] || strings.HasPrefix(soname, "ld-linux")
}

// Version extracts "2.36" from the symbol version name "GLIBC_2.36".
// GLIBC_PRIVATE and other non-numeric names are not versions.
func Version(name string) (string, bool) {
	v, ok := strings.CutPrefix(name, "GLIBC_")
	if !ok || v == "" {
		return "", false
	}
	for _, p := range strings.Split(v, ".") {
		if _, err := strconv.Atoi(p); err != nil {
			return "", false
		}
	}
	return v, true
}

// Less compares dotted numeric versions. The empty string is lowest.
func Less(a, b string) bool {
	if a == "" || b == "" {
		return a == "" && b != ""
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(as), len(bs)); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// Multiarch is the Debian multiarch triplet for a Go architecture.
func Multiarch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64-linux-gnu"
	case "arm64":
		return "aarch64-linux-gnu"
	}
	return ""
}
```

- [ ] **Step 4: Run** `go test ./internal/fsutil/ ./internal/glibc/ -v`. Expected: PASS.

- [ ] **Step 5: Commit** — `git add internal/fsutil internal/glibc && git commit -m "feat: in-root path resolution and glibc version helpers"`

---

### Task 3: ELF fixtures and the `ld.so.cache` parser

**Files:**
- Create: `test/images/host-libs/src/{fakedep.c,fakefabric.c,originuser.c,check.c,fakelibc.c,fakelibc.map}`, `hack/testdata.sh`, `hack/gen-testdata.sh`
- Generate (committed): `internal/discovery/testdata/hostroot/**`, `internal/hook/testdata/libc231/libc.so.6`
- Create: `internal/discovery/ldcache.go`, `internal/discovery/ldcache_test.go`

**Interfaces:**
- Produces:
  - `discovery.Cache` (`map[string]string`, soname → in-root path);
  - `discovery.ParseCache(data []byte, goarch string) (Cache, error)`;
  - fixture tree `internal/discovery/testdata/hostroot` whose `ARCH` file holds the Go architecture it was built for.

- [ ] **Step 1: C sources**

<!-- file: test/images/host-libs/src/fakedep.c -->
```c
#ifndef FAKEDEP_VARIANT
#define FAKEDEP_VARIANT "host"
#endif
const char *fakedep_variant(void) { return FAKEDEP_VARIANT; }
```

<!-- file: test/images/host-libs/src/fakefabric.c -->
```c
#include <stdlib.h>

extern const char *fakedep_variant(void);

/* arc4random is new in glibc 2.36, so this library requires GLIBC_2.36. */
const char *fakefabric_variant(void) {
	(void)arc4random();
	return fakedep_variant();
}
```

<!-- file: test/images/host-libs/src/originuser.c -->
```c
extern const char *fakedep_variant(void);
const char *originuser_variant(void) { return fakedep_variant(); }
```

<!-- file: test/images/host-libs/src/check.c -->
```c
#include <stdio.h>

extern const char *fakefabric_variant(void);

int main(void) {
	printf("fakedep=%s\n", fakefabric_variant());
	return 0;
}
```

<!-- file: test/images/host-libs/src/fakelibc.c -->
```c
void fake_217(void) {}
void fake_228(void) {}
void fake_231(void) {}
```

<!-- file: test/images/host-libs/src/fakelibc.map -->
```
GLIBC_2.17 { global: fake_217; local: *; };
GLIBC_2.28 { global: fake_228; } GLIBC_2.17;
GLIBC_2.31 { global: fake_231; } GLIBC_2.28;
```

- [ ] **Step 2: Fixture generator**

<!-- file: hack/testdata.sh -->
```bash
#!/usr/bin/env bash
# Runs inside debian:bookworm (see gen-testdata.sh). Writes ELF and
# ld.so.cache fixtures under /out (= internal/ in the repo).
set -euo pipefail
apt-get update -qq >/dev/null && apt-get install -y -qq gcc libc6-dev >/dev/null
S=/src
H=/out/discovery/testdata/hostroot
L=$H/usr/lib/brics-test
rm -rf /out/discovery/testdata /out/hook/testdata
mkdir -p "$L/sub" "$L/rp" "$H/etc" /out/hook/testdata/libc231 /tmp/gone
so() { gcc -shared -fPIC -o "$1" -Wl,-soname,"$2" "${@:3}"; }

so "$L/libfakedep.so.1.0.0" libfakedep.so.1 "$S/fakedep.c"
ln -s libfakedep.so.1.0.0 "$L/libfakedep.so.1"
so "$L/libfakefabric.so.1.0.0" libfakefabric.so.1 "$S/fakefabric.c" -L"$L" -l:libfakedep.so.1
ln -s libfakefabric.so.1.0.0 "$L/libfakefabric.so.1"
# RUNPATH=$ORIGIN/sub; libsubdep is not in the cache, only in sub/.
so "$L/sub/libsubdep.so.1" libsubdep.so.1 "$S/fakedep.c"
so "$L/liborigin.so.1" liborigin.so.1 "$S/originuser.c" -Wl,--enable-new-dtags,-rpath,'$ORIGIN/sub' -L"$L/sub" -l:libsubdep.so.1
# RPATH (old dtags)=$ORIGIN/rp; librpdep is only in rp/.
so "$L/rp/librpdep.so.1" librpdep.so.1 "$S/fakedep.c"
so "$L/librpath.so.1" librpath.so.1 "$S/originuser.c" -Wl,--disable-new-dtags,-rpath,'$ORIGIN/rp' -L"$L/rp" -l:librpdep.so.1
# A library whose dependency is not installed.
so /tmp/gone/libgone.so.1 libgone.so.1 "$S/fakedep.c"
so "$L/libneedsgone.so.1" libneedsgone.so.1 "$S/originuser.c" -L/tmp/gone -l:libgone.so.1

echo /usr/lib/brics-test > "$H/etc/ld.so.conf"
ldconfig -X -r "$H" -f /etc/ld.so.conf -C /etc/ld.so.cache
dpkg --print-architecture > "$H/ARCH"

# Fake glibc 2.31 for the hook's version check.
gcc -shared -fPIC -nostdlib -o /out/hook/testdata/libc231/libc.so.6 -Wl,-soname,libc.so.6 \
	-Wl,--version-script,"$S/fakelibc.map" "$S/fakelibc.c"
chown -R "${HOST_UID:-0}:${HOST_GID:-0}" /out/discovery/testdata /out/hook/testdata
```

<!-- file: hack/gen-testdata.sh -->
```bash
#!/usr/bin/env bash
# Regenerates the committed ELF fixtures in internal/discovery/testdata and
# internal/hook/testdata. The fixtures are built for this machine's
# architecture, which is recorded in testdata/hostroot/ARCH.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
CLI=${CONTAINER_CLI:-$(command -v docker >/dev/null 2>&1 && echo docker || echo podman)}
"$CLI" run --rm -e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
	-v "$ROOT/test/images/host-libs/src:/src:ro" \
	-v "$ROOT/hack/testdata.sh:/testdata.sh:ro" \
	-v "$ROOT/internal:/out" \
	docker.io/library/debian:bookworm bash /testdata.sh
```

Run: `chmod +x hack/*.sh && hack/gen-testdata.sh && cat internal/discovery/testdata/hostroot/ARCH && ls -la internal/discovery/testdata/hostroot/usr/lib/brics-test`
Expected: `arm64` on this machine. The listing shows the `.so` files and symlinks, and `etc/ld.so.cache` exists.

If `ldconfig -r` writes the cache somewhere else, fix the flags so that `$H/etc/ld.so.cache` exists and stores paths starting `/usr/lib/brics-test/`. Record a `Ruling:`.

- [ ] **Step 3: Failing parser tests**

<!-- file: internal/discovery/ldcache_test.go -->
```go
package discovery

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var hostroot = filepath.Join("testdata", "hostroot")

func fixtureArch(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(hostroot, "ARCH"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func otherArch(a string) string {
	if a == "arm64" {
		return "amd64"
	}
	return "arm64"
}

func TestParseCacheFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(hostroot, "etc", "ld.so.cache"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseCache(data, fixtureArch(t))
	if err != nil {
		t.Fatal(err)
	}
	for so, want := range map[string]string{
		"libfakefabric.so.1": "/usr/lib/brics-test/libfakefabric.so.1",
		"libfakedep.so.1":    "/usr/lib/brics-test/libfakedep.so.1",
	} {
		if c[so] != want {
			t.Errorf("cache[%s] = %q, want %q", so, c[so], want)
		}
	}
	if _, ok := c["libsubdep.so.1"]; ok {
		t.Error("libsubdep lives only in sub/ and must not be cached")
	}
	other, err := ParseCache(data, otherArch(fixtureArch(t)))
	if err != nil || len(other) != 0 {
		t.Errorf("other arch must yield no entries: %v %v", other, err)
	}
}

// synthetic builds a new-format cache, optionally behind an old-format prefix.
func synthetic(withOld bool, entries ...[3]any) []byte {
	var strs bytes.Buffer
	type ent struct {
		flags      int32
		key, value uint32
	}
	headerLen := 48 + 24*len(entries)
	var es []ent
	for _, e := range entries {
		k := uint32(headerLen + strs.Len())
		strs.WriteString(e[0].(string) + "\x00")
		v := uint32(headerLen + strs.Len())
		strs.WriteString(e[1].(string) + "\x00")
		es = append(es, ent{e[2].(int32), k, v})
	}
	var b bytes.Buffer
	b.WriteString(newMagic)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(entries)))
	_ = binary.Write(&b, binary.LittleEndian, uint32(strs.Len()))
	b.Write(make([]byte, 24)) // flags, padding, extension offset, unused
	for _, e := range es {
		_ = binary.Write(&b, binary.LittleEndian, e.flags)
		_ = binary.Write(&b, binary.LittleEndian, e.key)
		_ = binary.Write(&b, binary.LittleEndian, e.value)
		b.Write(make([]byte, 12)) // osversion, hwcap
	}
	b.Write(strs.Bytes())
	if !withOld {
		return b.Bytes()
	}
	var o bytes.Buffer
	o.WriteString(oldMagic + "\x00")
	_ = binary.Write(&o, binary.LittleEndian, uint32(0))
	for o.Len()%8 != 0 {
		o.WriteByte(0)
	}
	return append(o.Bytes(), b.Bytes()...)
}

func TestParseCacheFirstEntryWinsAndOldPrefix(t *testing.T) {
	aarch := int32(flagELFLibc6 | flagAArch64Lib64)
	for _, withOld := range []bool{false, true} {
		data := synthetic(withOld,
			[3]any{"libx.so.1", "/first/libx.so.1", aarch},
			[3]any{"libx.so.1", "/second/libx.so.1", aarch},
			[3]any{"liby.so.1", "/x86/liby.so.1", int32(flagELFLibc6 | flagX8664Lib64)},
		)
		c, err := ParseCache(data, "arm64")
		if err != nil {
			t.Fatalf("withOld=%v: %v", withOld, err)
		}
		if c["libx.so.1"] != "/first/libx.so.1" || len(c) != 1 {
			t.Errorf("withOld=%v: cache = %v", withOld, c)
		}
	}
}

func TestParseCacheRejectsGarbage(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("not a cache"), []byte(newMagic)} {
		if _, err := ParseCache(data, "arm64"); err == nil {
			t.Errorf("ParseCache(%q) must fail", data)
		}
	}
	if _, err := ParseCache(synthetic(false), "riscv64"); err == nil {
		t.Error("unsupported arch must fail")
	}
}
```

Run `go test ./internal/discovery/`. Expected: FAIL, undefined `ParseCache`.

- [ ] **Step 4: Implement**

<!-- file: internal/discovery/ldcache.go -->
```go
// Package discovery finds a host module's library closure on a node.
package discovery

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Cache maps sonames to the paths ld.so would load them from, as recorded
// in /etc/ld.so.cache.
type Cache map[string]string

const (
	newMagic = "glibc-ld.so.cache1.1"
	oldMagic = "ld.so-1.7.0"

	headerLen = 48
	entryLen  = 24

	flagELFLibc6     = 0x0003
	flagX8664Lib64   = 0x0300
	flagAArch64Lib64 = 0x0a00
)

var errTruncated = errors.New("ld.so.cache is truncated")

func archFlags(goarch string) (int32, error) {
	switch goarch {
	case "amd64":
		return flagELFLibc6 | flagX8664Lib64, nil
	case "arm64":
		return flagELFLibc6 | flagAArch64Lib64, nil
	}
	return 0, fmt.Errorf("unsupported architecture %q", goarch)
}

// ParseCache reads glibc's "glibc-ld.so.cache1.1" format, alone or after
// the legacy "ld.so-1.7.0" prefix. It keeps entries for goarch without
// hwcap subdirectories. When a soname appears twice, the first entry wins,
// as it does for ld.so.
func ParseCache(data []byte, goarch string) (Cache, error) {
	want, err := archFlags(goarch)
	if err != nil {
		return nil, err
	}
	base := 0
	if bytes.HasPrefix(data, []byte(oldMagic)) {
		if len(data) < 16 {
			return nil, errTruncated
		}
		n := int(binary.LittleEndian.Uint32(data[12:]))
		base = (16 + n*12 + 7) &^ 7
	}
	if len(data) < base+headerLen || string(data[base:base+len(newMagic)]) != newMagic {
		return nil, errors.New("not a glibc-ld.so.cache1.1 file")
	}
	h := data[base:] // string offsets are relative to the new-format header
	n := int(binary.LittleEndian.Uint32(h[20:]))
	if headerLen+n*entryLen > len(h) {
		return nil, errTruncated
	}
	c := Cache{}
	for i := range n {
		e := h[headerLen+i*entryLen:]
		flags := int32(binary.LittleEndian.Uint32(e[0:]))
		if flags != want || binary.LittleEndian.Uint64(e[16:]) != 0 {
			continue
		}
		key, err := cstring(h, binary.LittleEndian.Uint32(e[4:]))
		if err != nil {
			return nil, err
		}
		value, err := cstring(h, binary.LittleEndian.Uint32(e[8:]))
		if err != nil {
			return nil, err
		}
		if _, seen := c[key]; !seen {
			c[key] = value
		}
	}
	return c, nil
}

func cstring(b []byte, off uint32) (string, error) {
	if int(off) >= len(b) {
		return "", errTruncated
	}
	end := bytes.IndexByte(b[off:], 0)
	if end < 0 {
		return "", errTruncated
	}
	return string(b[off : int(off)+end]), nil
}
```

- [ ] **Step 5: Run** `go test ./internal/discovery/ -v`. Expected: PASS.

- [ ] **Step 6: Commit** — `git add test/images/host-libs/src hack internal/discovery internal/hook/testdata && git commit -m "feat(discovery): ld.so.cache parser and ELF fixtures"`

---

### Task 4: Discovery walk

**Files:**
- Create: `internal/discovery/discover.go`, `internal/discovery/discover_test.go`

**Interfaces:**
- Consumes: `fsutil.ResolveIn`, `fsutil.Host`, `glibc.*`, `ParseCache`.
- Produces:
  - `type Library struct{ Soname, Path string; Size int64 }`
  - `type Result struct{ Ready bool; Lib, Deps []Library; Missing []string; GlibcRequired, Hash, Message string }`
  - `func NewHost(root, goarch string) (*Host, error)`
  - `func (h *Host) Discover(roots, preferHost []string) Result`

- [ ] **Step 1: Failing tests**

<!-- file: internal/discovery/discover_test.go -->
```go
package discovery

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func host(t *testing.T) *Host {
	t.Helper()
	h, err := NewHost(hostroot, fixtureArch(t))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func sonames(ls []Library) []string {
	var s []string
	for _, l := range ls {
		s = append(s, l.Soname)
	}
	return s
}

const L = "/usr/lib/brics-test/"

func TestDiscoverSplitsRootsAndDeps(t *testing.T) {
	r := host(t).Discover([]string{"libfakefabric.so.1"}, nil)
	if !r.Ready || r.Message != "" {
		t.Fatalf("not ready: %+v", r)
	}
	if !reflect.DeepEqual(sonames(r.Lib), []string{"libfakefabric.so.1"}) || r.Lib[0].Path != L+"libfakefabric.so.1.0.0" || r.Lib[0].Size == 0 {
		t.Errorf("lib = %+v", r.Lib)
	}
	if !reflect.DeepEqual(sonames(r.Deps), []string{"libfakedep.so.1"}) || r.Deps[0].Path != L+"libfakedep.so.1.0.0" {
		t.Errorf("deps = %+v (glibc must be excluded)", r.Deps)
	}
	if r.GlibcRequired != "2.36" {
		t.Errorf("glibcRequired = %q, want 2.36 (arc4random)", r.GlibcRequired)
	}
	if !strings.HasPrefix(r.Hash, "sha256:") || len(r.Hash) != 71 {
		t.Errorf("hash = %q", r.Hash)
	}
}

func TestDiscoverPreferHost(t *testing.T) {
	r := host(t).Discover([]string{"libfakefabric.so.1"}, []string{"libfakedep.so.1"})
	if !reflect.DeepEqual(sonames(r.Lib), []string{"libfakedep.so.1", "libfakefabric.so.1"}) || len(r.Deps) != 0 {
		t.Errorf("lib=%v deps=%v", sonames(r.Lib), sonames(r.Deps))
	}
}

func TestDiscoverRunpathAndRpath(t *testing.T) {
	r := host(t).Discover([]string{"liborigin.so.1", "librpath.so.1"}, nil)
	if !r.Ready {
		t.Fatalf("not ready: %s", r.Message)
	}
	want := map[string]string{"libsubdep.so.1": L + "sub/libsubdep.so.1", "librpdep.so.1": L + "rp/librpdep.so.1"}
	for _, d := range r.Deps {
		if want[d.Soname] != d.Path {
			t.Errorf("dep %s at %s, want %s", d.Soname, d.Path, want[d.Soname])
		}
		delete(want, d.Soname)
	}
	if len(want) != 0 {
		t.Errorf("missing deps %v", want)
	}
}

func TestLookupIgnoresRpathWhenRunpathSet(t *testing.T) {
	h := host(t)
	if _, ok := h.lookup("libsubdep.so.1", L, []string{L + "sub"}, []string{L + "rp"}); ok {
		t.Error("DT_RPATH must be ignored when DT_RUNPATH is present")
	}
	if p, ok := h.lookup("libsubdep.so.1", L, []string{"$ORIGIN/sub"}, nil); !ok || p != L+"sub/libsubdep.so.1" {
		t.Errorf("rpath with $ORIGIN: %q %v", p, ok)
	}
	if _, ok := h.lookup("libsubdep.so.1", L, nil, []string{"$LIB/sub", "relative/sub"}); ok {
		t.Error("$LIB and relative entries must be skipped")
	}
}

func TestDiscoverMissing(t *testing.T) {
	r := host(t).Discover([]string{"libnothere.so.1"}, nil)
	if r.Ready || !reflect.DeepEqual(r.Missing, []string{"libnothere.so.1"}) || r.Message != "root libnothere.so.1 not in /etc/ld.so.cache" {
		t.Errorf("missing root: %+v", r)
	}
	r = host(t).Discover([]string{"libneedsgone.so.1"}, nil)
	if r.Ready || !reflect.DeepEqual(r.Missing, []string{"libgone.so.1"}) || r.Message != "libneedsgone.so.1 needs libgone.so.1: not found" {
		t.Errorf("missing dep: %+v", r)
	}
}

func TestDiscoverSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("cp", "-a", hostroot+"/.", root).CombinedOutput(); err != nil {
		t.Fatalf("copy fixture: %v %s", err, out)
	}
	if err := os.Symlink("../../../../../../etc/passwd", filepath.Join(root, "usr/lib/brics-test/libesc.so.1")); err != nil {
		t.Fatal(err)
	}
	h, err := NewHost(root, fixtureArch(t))
	if err != nil {
		t.Fatal(err)
	}
	h.cache["libesc.so.1"] = L + "libesc.so.1"
	r := h.Discover([]string{"libesc.so.1"}, nil)
	if r.Ready || !strings.Contains(r.Message, "escapes root") {
		t.Errorf("escape: %+v", r)
	}
}

func TestDiscoverHashStable(t *testing.T) {
	a := host(t).Discover([]string{"libfakefabric.so.1"}, nil)
	b := host(t).Discover([]string{"libfakefabric.so.1"}, nil)
	c := host(t).Discover([]string{"libfakefabric.so.1"}, []string{"libfakedep.so.1"})
	if a.Hash != b.Hash || a.Hash == c.Hash {
		t.Errorf("hash stable=%v distinct=%v", a.Hash == b.Hash, a.Hash != c.Hash)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/discovery/`. Expected: FAIL, undefined `NewHost`.

- [ ] **Step 3: Implement**

<!-- file: internal/discovery/discover.go -->
```go
package discovery

import (
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bristol-supercomputing/k8s-modules/internal/fsutil"
	"github.com/bristol-supercomputing/k8s-modules/internal/glibc"
)

// Library is one file of a closure. Path is the real host path.
type Library struct {
	Soname string
	Path   string
	Size   int64
}

// Result is a host module's closure on one node.
type Result struct {
	Ready         bool
	Lib, Deps     []Library
	Missing       []string
	GlibcRequired string
	Hash          string
	Message       string
}

// Host is a node filesystem mounted at Root.
type Host struct {
	Root  string
	Arch  string
	cache Cache
}

// NewHost reads Root/etc/ld.so.cache.
func NewHost(root, goarch string) (*Host, error) {
	data, err := os.ReadFile(filepath.Join(root, "etc", "ld.so.cache"))
	if err != nil {
		return nil, err
	}
	c, err := ParseCache(data, goarch)
	if err != nil {
		return nil, fmt.Errorf("/etc/ld.so.cache: %w", err)
	}
	return &Host{Root: root, Arch: goarch, cache: c}, nil
}

// Discover resolves roots through the cache and walks DT_NEEDED. Roots and
// preferHost entries are placed in Lib, everything else in Deps. glibc is
// never included.
func (h *Host) Discover(roots, preferHost []string) Result {
	var res Result
	fail := func(format string, args ...any) {
		if res.Message == "" {
			res.Message = fmt.Sprintf(format, args...)
		}
	}
	isRoot, prefer := setOf(roots), setOf(preferHost)
	visited := map[string]bool{}
	type item struct{ soname, path string }
	var queue []item
	for _, r := range roots {
		if visited[r] {
			continue
		}
		visited[r] = true
		p, ok := h.cache[r]
		if !ok {
			res.Missing = append(res.Missing, r)
			fail("root %s not in /etc/ld.so.cache", r)
			continue
		}
		queue = append(queue, item{r, p})
	}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		real, err := fsutil.ResolveIn(h.Root, it.path)
		if err != nil {
			fail("%s: %v", it.soname, err)
			continue
		}
		full := fsutil.Host(h.Root, real)
		fi, err := os.Stat(full)
		if err != nil || !fi.Mode().IsRegular() {
			fail("%s: %s is not a regular file", it.soname, real)
			continue
		}
		f, err := elf.Open(full)
		if err != nil {
			fail("%s: %v", it.soname, err)
			continue
		}
		needed, _ := f.DynString(elf.DT_NEEDED)
		runpath, _ := f.DynString(elf.DT_RUNPATH)
		rpath, _ := f.DynString(elf.DT_RPATH)
		if v := glibcFloor(f); glibc.Less(res.GlibcRequired, v) {
			res.GlibcRequired = v
		}
		f.Close()

		lib := Library{Soname: it.soname, Path: real, Size: fi.Size()}
		if isRoot[it.soname] || prefer[it.soname] {
			res.Lib = append(res.Lib, lib)
		} else {
			res.Deps = append(res.Deps, lib)
		}
		// $ORIGIN is the directory the loader found the object in, which
		// is the unresolved lookup path.
		origin := path.Dir(it.path)
		for _, n := range needed {
			if glibc.Excluded(n) || visited[n] {
				continue
			}
			visited[n] = true
			p, ok := h.lookup(n, origin, rpath, runpath)
			if !ok {
				res.Missing = append(res.Missing, n)
				fail("%s needs %s: not found", it.soname, n)
				continue
			}
			queue = append(queue, item{n, p})
		}
	}
	bySoname := func(ls []Library) {
		sort.Slice(ls, func(i, j int) bool { return ls[i].Soname < ls[j].Soname })
	}
	bySoname(res.Lib)
	bySoname(res.Deps)
	sort.Strings(res.Missing)
	res.Ready = res.Message == ""
	res.Hash = closureHash(res)
	return res
}

// lookup follows ld.so's order: DT_RPATH (only without DT_RUNPATH),
// DT_RUNPATH, the cache, then the default directories.
func (h *Host) lookup(soname, origin string, rpath, runpath []string) (string, bool) {
	var dirs []string
	if len(runpath) == 0 {
		dirs = append(dirs, expand(rpath, origin)...)
	}
	dirs = append(dirs, expand(runpath, origin)...)
	for _, d := range dirs {
		if p := path.Join(d, soname); h.isFile(p) {
			return p, true
		}
	}
	if p, ok := h.cache[soname]; ok {
		return p, true
	}
	for _, d := range defaultDirs(h.Arch) {
		if p := path.Join(d, soname); h.isFile(p) {
			return p, true
		}
	}
	return "", false
}

func (h *Host) isFile(p string) bool {
	real, err := fsutil.ResolveIn(h.Root, p)
	if err != nil {
		return false
	}
	fi, err := os.Stat(fsutil.Host(h.Root, real))
	return err == nil && fi.Mode().IsRegular()
}

func expand(entries []string, origin string) []string {
	var out []string
	for _, e := range entries {
		for _, d := range strings.Split(e, ":") {
			if d == "" || strings.Contains(d, "$LIB") || strings.Contains(d, "${LIB}") ||
				strings.Contains(d, "$PLATFORM") || strings.Contains(d, "${PLATFORM}") {
				continue
			}
			d = strings.ReplaceAll(strings.ReplaceAll(d, "${ORIGIN}", origin), "$ORIGIN", origin)
			if path.IsAbs(d) {
				out = append(out, path.Clean(d))
			}
		}
	}
	return out
}

func defaultDirs(goarch string) []string {
	dirs := []string{"/lib64", "/usr/lib64", "/lib", "/usr/lib"}
	if ma := glibc.Multiarch(goarch); ma != "" {
		dirs = append(dirs, "/lib/"+ma, "/usr/lib/"+ma)
	}
	return dirs
}

func glibcFloor(f *elf.File) string {
	needs, err := f.DynamicVersionNeeds()
	if err != nil {
		return ""
	}
	floor := ""
	for _, n := range needs {
		if !glibc.Excluded(n.Name) {
			continue
		}
		for _, d := range n.Needs {
			if v, ok := glibc.Version(d.Dep); ok && glibc.Less(floor, v) {
				floor = v
			}
		}
	}
	return floor
}

func closureHash(r Result) string {
	var lines []string
	for _, l := range r.Lib {
		lines = append(lines, fmt.Sprintf("lib %s %s %d", l.Soname, l.Path, l.Size))
	}
	for _, l := range r.Deps {
		lines = append(lines, fmt.Sprintf("deps %s %s %d", l.Soname, l.Path, l.Size))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func setOf(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
```

- [ ] **Step 4: Run** `go test ./internal/discovery/ -v -count=1`. Expected: PASS.

- [ ] **Step 5: Commit** — `git add internal/discovery && git commit -m "feat(discovery): DT_NEEDED closure walk with split placement and glibc floor"`

---

### Task 5: OCI hook

**Files:**
- Create: `internal/hook/hook.go`, `internal/hook/runner.go`, `internal/hook/hook_test.go`, `cmd/module-hook/main.go`

**Interfaces:**
- Consumes: `fsutil`, `glibc`.
- Produces:
  - `hook.Options{Deps []string; Glibc map[string]string; Arch string}`
  - `hook.Runner` interface, with the method `Run(rootfs, ldconfig string) error`
  - `hook.ChrootRunner{}`
  - `hook.Apply(rootfs string, o Options, r Runner) error`
  - `hook.RootFromState(state []byte) (string, error)`
  - binary `module-hook --deps <dir> … --glibc <module>=<ver> …`

- [ ] **Step 1: Failing tests**

<!-- file: internal/hook/hook_test.go -->
```go
package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct{ rootfs, ldconfig string }

func (f *fakeRunner) Run(rootfs, ldconfig string) error {
	f.rootfs, f.ldconfig = rootfs, ldconfig
	return nil
}

// rootfs builds a minimal glibc-2.31 image tree.
func rootfs(t *testing.T) string {
	t.Helper()
	r := t.TempDir()
	for _, d := range []string{"etc/ld.so.conf.d", "usr/lib", "usr/sbin", "lib64"} {
		if err := os.MkdirAll(filepath.Join(r, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	libc, err := os.ReadFile(filepath.Join("testdata", "libc231", "libc.so.6"))
	if err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(r, "usr/lib/libc.so.6"), libc, 0o755))
	must(t, os.WriteFile(filepath.Join(r, "usr/sbin/ldconfig"), []byte("#!"), 0o755))
	must(t, os.Symlink("usr/sbin", filepath.Join(r, "sbin")))
	must(t, os.WriteFile(filepath.Join(r, "etc/ld.so.conf"), []byte("include /etc/ld.so.conf.d/*.conf\n"), 0o644))
	return r
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestApplyWritesConfAndRunsLdconfig(t *testing.T) {
	r := rootfs(t)
	run := &fakeRunner{}
	err := Apply(r, Options{Deps: []string{"/opt/modules/fab/host/deps"}, Glibc: map[string]string{"fab-host": "2.28"}, Arch: "arm64"}, run)
	if err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(filepath.Join(r, "etc/ld.so.conf.d/zz-brics-modules.conf"))
	if want := "/lib64\n/usr/lib\n/opt/modules/fab/host/deps\n"; string(conf) != want {
		t.Errorf("conf = %q, want %q (trusted dirs that exist, then deps)", conf, want)
	}
	if run.rootfs != r || run.ldconfig != "/usr/sbin/ldconfig" {
		t.Errorf("runner got %q %q", run.rootfs, run.ldconfig)
	}
	main, _ := os.ReadFile(filepath.Join(r, "etc/ld.so.conf"))
	if strings.Count(string(main), "include") != 1 {
		t.Errorf("existing include must not be duplicated: %q", main)
	}
}

func TestApplyAddsIncludeWhenMissing(t *testing.T) {
	r := rootfs(t)
	must(t, os.WriteFile(filepath.Join(r, "etc/ld.so.conf"), []byte("/usr/local/lib"), 0o644))
	must(t, Apply(r, Options{Deps: []string{"/d"}}, &fakeRunner{}))
	main, _ := os.ReadFile(filepath.Join(r, "etc/ld.so.conf"))
	if string(main) != "/usr/local/lib\ninclude /etc/ld.so.conf.d/*.conf\n" {
		t.Errorf("ld.so.conf = %q", main)
	}
}

func TestApplyGlibcTooOld(t *testing.T) {
	err := Apply(rootfs(t), Options{Deps: []string{"/d"}, Glibc: map[string]string{"fab-host": "2.36"}, Arch: "arm64"}, &fakeRunner{})
	want := "module fab-host needs GLIBC_2.36; the image's libc.so.6 provides up to GLIBC_2.31"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestApplyErrors(t *testing.T) {
	noLdconfig := rootfs(t)
	must(t, os.Remove(filepath.Join(noLdconfig, "usr/sbin/ldconfig")))
	noLibc := rootfs(t)
	must(t, os.Remove(filepath.Join(noLibc, "usr/lib/libc.so.6")))
	readOnly := rootfs(t)
	must(t, os.Chmod(filepath.Join(readOnly, "etc/ld.so.conf.d"), 0o555))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(readOnly, "etc/ld.so.conf.d"), 0o755) })

	cases := map[string]struct {
		root string
		want string
	}{
		"no ldconfig": {noLdconfig, "image has no ldconfig; host modules require a glibc-based image"},
		"no libc":     {noLibc, "image has no glibc libc.so.6; host modules require a glibc-based image"},
		"read-only":   {readOnly, "host modules need a writable /etc in the image (readOnlyRootFilesystem is not supported)"},
	}
	for name, c := range cases {
		err := Apply(c.root, Options{Deps: []string{"/d"}, Glibc: map[string]string{"m": "2.17"}, Arch: "arm64"}, &fakeRunner{})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestRootFromState(t *testing.T) {
	bundle := t.TempDir()
	must(t, os.WriteFile(filepath.Join(bundle, "config.json"), []byte(`{"root":{"path":"rootfs"}}`), 0o644))
	state, _ := json.Marshal(map[string]string{"bundle": bundle})
	got, err := RootFromState(state)
	if err != nil || got != filepath.Join(bundle, "rootfs") {
		t.Errorf("RootFromState = %q, %v", got, err)
	}
	must(t, os.WriteFile(filepath.Join(bundle, "config.json"), []byte(`{"root":{"path":"/abs/root"}}`), 0o644))
	if got, _ := RootFromState(state); got != "/abs/root" {
		t.Errorf("absolute root = %q", got)
	}
	if _, err := RootFromState([]byte(`{}`)); err == nil {
		t.Error("missing bundle must fail")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/hook/`. Expected: FAIL, undefined `Apply`.

- [ ] **Step 3: Implement**

<!-- file: internal/hook/hook.go -->
```go
// Package hook implements module-hook: it adds host-module dependency
// directories to an image's ld cache and checks the image's glibc. It runs
// as an OCI createContainer hook, so paths here are host paths into the
// container's rootfs, and every in-image path is resolved with
// fsutil.ResolveIn so image symlinks cannot reach outside the rootfs.
package hook

import (
	"bytes"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bristol-supercomputing/k8s-modules/internal/fsutil"
	"github.com/bristol-supercomputing/k8s-modules/internal/glibc"
)

// Options are module-hook's arguments.
type Options struct {
	Deps  []string          // container paths of deps directories
	Glibc map[string]string // module name -> required GLIBC version
	Arch  string            // Go architecture, for multiarch lookup
}

// Runner runs the image's ldconfig with rootfs as its root.
type Runner interface {
	Run(rootfs, ldconfig string) error
}

// ConfName is the ld.so.conf.d file module-hook owns.
const ConfName = "zz-brics-modules.conf"

// ldconfig scans these last, after every ld.so.conf entry. Listing the ones
// the image has before the deps directories keeps the image's own copies
// ahead of host copies in the cache.
var trustedDirs = []string{"/lib64", "/usr/lib64", "/lib", "/usr/lib"}

// Apply writes the ld config, runs ldconfig and checks glibc.
func Apply(rootfs string, o Options, r Runner) error {
	if err := writeConf(rootfs, o.Deps); err != nil {
		return err
	}
	ldconfig, err := findLdconfig(rootfs)
	if err != nil {
		return err
	}
	if err := r.Run(rootfs, ldconfig); err != nil {
		return fmt.Errorf("updating the image's ld cache: %w", err)
	}
	if len(o.Glibc) == 0 {
		return nil
	}
	return checkGlibc(rootfs, o)
}

func writeConf(rootfs string, deps []string) error {
	var b strings.Builder
	for _, d := range trustedDirs {
		if isDir(rootfs, d) {
			b.WriteString(d + "\n")
		}
	}
	for _, d := range deps {
		b.WriteString(d + "\n")
	}
	confd, err := resolveForWrite(rootfs, "/etc/ld.so.conf.d")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(fsutil.Host(rootfs, confd), 0o755); err != nil {
		return readOnly(err)
	}
	if err := os.WriteFile(fsutil.Host(rootfs, path.Join(confd, ConfName)), []byte(b.String()), 0o644); err != nil {
		return readOnly(err)
	}
	return ensureInclude(rootfs)
}

func ensureInclude(rootfs string) error {
	conf, err := resolveForWrite(rootfs, "/etc/ld.so.conf")
	if err != nil {
		return err
	}
	file := fsutil.Host(rootfs, conf)
	data, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "include" && (f[1] == "ld.so.conf.d/*.conf" || f[1] == "/etc/ld.so.conf.d/*.conf") {
			return nil
		}
	}
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	data = append(data, "include /etc/ld.so.conf.d/*.conf\n"...)
	if err := os.WriteFile(file, data, 0o644); err != nil {
		return readOnly(err)
	}
	return nil
}

// resolveForWrite resolves p inside rootfs; when p does not exist yet, its
// parent is resolved and p's base name joined to it.
func resolveForWrite(rootfs, p string) (string, error) {
	real, err := fsutil.ResolveIn(rootfs, p)
	if err == nil {
		return real, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	parent, err := fsutil.ResolveIn(rootfs, path.Dir(p))
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return "", err
	}
	return path.Join(parent, path.Base(p)), nil
}

func readOnly(err error) error {
	return fmt.Errorf("host modules need a writable /etc in the image (readOnlyRootFilesystem is not supported): %w", err)
}

func isDir(rootfs, p string) bool {
	real, err := fsutil.ResolveIn(rootfs, p)
	if err != nil {
		return false
	}
	fi, err := os.Stat(fsutil.Host(rootfs, real))
	return err == nil && fi.IsDir()
}

func findLdconfig(rootfs string) (string, error) {
	for _, p := range []string{"/sbin/ldconfig", "/usr/sbin/ldconfig"} {
		real, err := fsutil.ResolveIn(rootfs, p)
		if err != nil {
			continue
		}
		if fi, err := os.Stat(fsutil.Host(rootfs, real)); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return real, nil
		}
	}
	return "", errors.New("image has no ldconfig; host modules require a glibc-based image")
}

func checkGlibc(rootfs string, o Options) error {
	have, err := imageGlibc(rootfs, o.Arch)
	if err != nil {
		return err
	}
	mods := make([]string, 0, len(o.Glibc))
	for m := range o.Glibc {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	for _, m := range mods {
		if glibc.Less(have, o.Glibc[m]) {
			return fmt.Errorf("module %s needs GLIBC_%s; the image's libc.so.6 provides up to GLIBC_%s", m, o.Glibc[m], have)
		}
	}
	return nil
}

func imageGlibc(rootfs, goarch string) (string, error) {
	dirs := []string{"/lib64", "/usr/lib64"}
	if ma := glibc.Multiarch(goarch); ma != "" {
		dirs = append(dirs, "/lib/"+ma, "/usr/lib/"+ma)
	}
	dirs = append(dirs, "/lib", "/usr/lib")
	for _, d := range dirs {
		real, err := fsutil.ResolveIn(rootfs, path.Join(d, "libc.so.6"))
		if err != nil {
			continue
		}
		f, err := elf.Open(fsutil.Host(rootfs, real))
		if err != nil {
			continue
		}
		vers, err := f.DynamicVersions()
		f.Close()
		if err != nil {
			continue
		}
		have := ""
		for _, v := range vers {
			if g, ok := glibc.Version(v.Name); ok && glibc.Less(have, g) {
				have = g
			}
		}
		if have != "" {
			return have, nil
		}
	}
	return "", errors.New("image has no glibc libc.so.6; host modules require a glibc-based image")
}

// RootFromState reads the OCI state JSON a hook receives on stdin and
// returns the container's rootfs path from the bundle's config.json.
func RootFromState(state []byte) (string, error) {
	var st struct {
		Bundle string `json:"bundle"`
	}
	if err := json.Unmarshal(state, &st); err != nil || st.Bundle == "" {
		return "", fmt.Errorf("reading OCI state: no bundle (%v)", err)
	}
	cfg, err := os.ReadFile(filepath.Join(st.Bundle, "config.json"))
	if err != nil {
		return "", err
	}
	var spec struct {
		Root *struct {
			Path string `json:"path"`
		} `json:"root"`
	}
	if err := json.Unmarshal(cfg, &spec); err != nil || spec.Root == nil || spec.Root.Path == "" {
		return "", fmt.Errorf("bundle config.json has no root.path (%v)", err)
	}
	if filepath.IsAbs(spec.Root.Path) {
		return spec.Root.Path, nil
	}
	return filepath.Join(st.Bundle, spec.Root.Path), nil
}
```

<!-- file: internal/hook/runner.go -->
```go
package hook

import (
	"fmt"
	"os/exec"
	"syscall"
)

// ChrootRunner runs ldconfig in a child process chrooted into the rootfs,
// so the image's own ldconfig builds the image's own cache.
type ChrootRunner struct{}

func (ChrootRunner) Run(rootfs, ldconfig string) error {
	cmd := exec.Command(ldconfig)
	cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: rootfs}
	cmd.Dir = "/"
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %v: %s", ldconfig, err, out)
	}
	return nil
}
```

<!-- file: cmd/module-hook/main.go -->
```go
// Command module-hook is the module system's OCI createContainer hook.
// module-nri adds it to containers that load host modules.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/bristol-supercomputing/k8s-modules/internal/hook"
)

type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

func main() {
	var deps, glibcs repeated
	fs := flag.NewFlagSet("module-hook", flag.ContinueOnError)
	fs.Var(&deps, "deps", "container path of a host module deps directory (repeatable)")
	fs.Var(&glibcs, "glibc", "<module>=<version> the module needs (repeatable)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if err := run(os.Stdin, deps, glibcs); err != nil {
		fmt.Fprintln(os.Stderr, "module-hook:", err)
		os.Exit(1)
	}
}

func run(stdin io.Reader, deps, glibcs []string) error {
	state, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	rootfs, err := hook.RootFromState(state)
	if err != nil {
		return err
	}
	req := map[string]string{}
	for _, g := range glibcs {
		m, v, ok := strings.Cut(g, "=")
		if !ok || m == "" || v == "" {
			return fmt.Errorf("--glibc %q: want <module>=<version>", g)
		}
		req[m] = v
	}
	return hook.Apply(rootfs, hook.Options{Deps: deps, Glibc: req, Arch: runtime.GOARCH}, hook.ChrootRunner{})
}
```

- [ ] **Step 4: Run** `go test ./internal/hook/ -v -count=1 && GOOS=linux go build ./cmd/module-hook`. Expected: PASS, and the Linux build succeeds.

- [ ] **Step 5: Commit** — `git add internal/hook cmd/module-hook && git commit -m "feat(hook): module-hook ld cache update and glibc check"`

---

### Task 6: NRI adjustments (pure logic)

**Files:**
- Create: `internal/nri/value.go`, `internal/nri/ldpath.go`, `internal/nri/adjust.go`, `internal/nri/mountinfo.go`, and the tests `internal/nri/adjust_test.go` and `internal/nri/mountinfo_test.go`

**Interfaces:**
- Consumes:
  - `resolve.Resolve`, used in tests to build real annotation values;
  - `discovery.Result` and `discovery.Library`;
  - `v1alpha1` constants.
- Produces:
  - `nri.ModuleValue{Name string; Rank int; Src, Mount string; Lib, Mask []string}` and `nri.ParseValue(v string) (ModuleValue, error)`
  - interfaces `Catalogue{Module(name string) (*v1.Module, bool)}`, `HostModules{HostModule(name string) (discovery.Result, bool)}` and `PVCs{HostPath(claim string) (string, error)}`
  - `nri.Adjuster{Catalogue; Hosts; PVCs; NodeName, HookPath string}`, with the method `Adjust(pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, error)`
  - `nri.PrependLibraryPath(env []string, dirs []string) string`
  - `nri.MountInfo{MajMin, Root, MountPoint string}` and `nri.ParseMountInfo([]byte) ([]MountInfo, error)`
  - `nri.HostPathFor(self, host []MountInfo, mountPoint string) (string, error)`
  - `nri.MountinfoPVCs{Claims map[string]bool; Dir, SelfMountinfo, HostMountinfo string}`, which implements `PVCs`
  - error sentinel `nri.ErrClaimNotMounted`

- [ ] **Step 1: Add the NRI dependency** — `go get github.com/containerd/nri@v0.12.0`

- [ ] **Step 2: Failing tests**

<!-- file: internal/nri/adjust_test.go -->
```go
package nri

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	api "github.com/containerd/nri/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	"github.com/bristol-supercomputing/k8s-modules/internal/discovery"
	"github.com/bristol-supercomputing/k8s-modules/internal/resolve"
)

type fakeCatalogue map[string]*v1.Module

func (c fakeCatalogue) Module(n string) (*v1.Module, bool) { m, ok := c[n]; return m, ok }

type fakeHosts map[string]discovery.Result

func (h fakeHosts) HostModule(n string) (discovery.Result, bool) { r, ok := h[n]; return r, ok }

type fakePVCs map[string]string

func (p fakePVCs) HostPath(c string) (string, error) {
	if hp, ok := p[c]; ok {
		return hp, nil
	}
	return "", errors.New("pvc claim " + c + " is not mounted by module-nri; add it to nri.pvcs")
}

func mod(family, version string, src v1.ModuleSource, deps ...string) *v1.Module {
	return &v1.Module{ObjectMeta: metav1.ObjectMeta{Name: family + "-" + version},
		Spec: v1.ModuleSpec{Family: family, Version: version, Source: src, DependsOn: deps}}
}

var (
	img = v1.ModuleSource{Image: &v1.ImageSource{Reference: "r/x@sha256:" + strings.Repeat("a", 64)}}
	hst = v1.ModuleSource{Host: &v1.HostSource{Discover: v1.HostDiscover{Roots: []string{"libfab.so.1"}}}}
	pvc = v1.ModuleSource{PVC: &v1.PVCSource{ClaimName: "spack-apps", SubPath: "nccl/1"}}
)

// catalogue resolves every module so status annotations are the real format.
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

// podFor builds the pod annotations admission would write for loading names.
func podFor(c fakeCatalogue, containers string, names ...string) *api.PodSandbox {
	anns := map[string]string{v1.AnnotationContainers: containers, v1.AnnotationRequiredPlugins: v1.RequiredPluginsValue}
	for _, n := range names {
		for k, v := range c[n].Status.Resolved.Annotations {
			anns[k] = v
		}
	}
	return &api.PodSandbox{Name: "p", Namespace: "ns", Annotations: anns}
}

func ctr(name string, env ...string) *api.Container { return &api.Container{Name: name, Env: env} }

func envOf(adj *api.ContainerAdjustment) map[string]string {
	m := map[string]string{}
	for _, kv := range adj.GetEnv() {
		m[kv.Key] = kv.Value
	}
	return m
}

func mountsOf(adj *api.ContainerAdjustment) map[string]*api.Mount {
	m := map[string]*api.Mount{}
	for _, mt := range adj.GetMounts() {
		m[mt.Destination] = mt
	}
	return m
}

func hostResult() discovery.Result {
	return discovery.Result{Ready: true, GlibcRequired: "2.36",
		Lib:  []discovery.Library{{Soname: "libfab.so.1", Path: "/usr/lib64/libfab.so.1.2"}},
		Deps: []discovery.Library{{Soname: "libdep.so.1", Path: "/usr/lib64/libdep.so.1.0"}}}
}

func adjuster(c fakeCatalogue, h fakeHosts) *Adjuster {
	return &Adjuster{Catalogue: c, Hosts: h, PVCs: fakePVCs{"spack-apps": "/var/lib/pv"}, NodeName: "n1", HookPath: "/opt/brics-modules/bin/module-hook"}
}

func TestAdjustImageAndHostModules(t *testing.T) {
	nccl := mod("nccl", "2", img)
	nccl.Spec.Env = &v1.ModuleEnv{PrependPath: map[string]v1.PathList{v1.EnvLDLibraryPath: {"lib"}}}
	fab := mod("fab", "host", hst)
	top := mod("ofi", "1", img, "nccl-2", "fab-host")
	top.Spec.Env = &v1.ModuleEnv{PrependPath: map[string]v1.PathList{v1.EnvLDLibraryPath: {"lib"}}}
	top.Spec.MaskPaths = []string{"/opt/amazon"}
	c := catalogue(t, nccl, fab, top)

	adj, err := adjuster(c, fakeHosts{"fab-host": hostResult()}).Adjust(podFor(c, "main", "ofi-1"), ctr("main", "LD_LIBRARY_PATH=/usr/local/cuda/lib64:/opt/modules/nccl/2/lib"))
	if err != nil {
		t.Fatal(err)
	}
	env := envOf(adj)
	if want := "/opt/modules/ofi/1/lib:/opt/modules/fab/host/lib:/opt/modules/nccl/2/lib:/usr/local/cuda/lib64"; env["LD_LIBRARY_PATH"] != want {
		t.Errorf("LD_LIBRARY_PATH = %q\nwant %q", env["LD_LIBRARY_PATH"], want)
	}
	if env["BRICS_MODULES"] != "ofi-1:fab-host:nccl-2" {
		t.Errorf("BRICS_MODULES = %q", env["BRICS_MODULES"])
	}
	m := mountsOf(adj)
	if b := m["/opt/modules/fab/host/lib/libfab.so.1"]; b == nil || b.Source != "/usr/lib64/libfab.so.1.2" || !reflect.DeepEqual(b.Options, []string{"bind", "ro", "nosuid", "nodev", "rprivate"}) {
		t.Errorf("lib bind = %+v", b)
	}
	if b := m["/opt/modules/fab/host/deps/libdep.so.1"]; b == nil || b.Source != "/usr/lib64/libdep.so.1.0" {
		t.Errorf("deps bind = %+v", b)
	}
	if mk := m["/opt/amazon"]; mk == nil || mk.Type != "tmpfs" || !reflect.DeepEqual(mk.Options, []string{"ro", "nosuid", "nodev", "noexec", "size=0"}) {
		t.Errorf("mask = %+v", mk)
	}
	hooks := adj.GetHooks().GetCreateContainer()
	if len(hooks) != 1 || hooks[0].Path != "/opt/brics-modules/bin/module-hook" ||
		!reflect.DeepEqual(hooks[0].Args, []string{"module-hook", "--deps", "/opt/modules/fab/host/deps", "--glibc", "fab-host=2.36"}) {
		t.Errorf("hooks = %+v", hooks)
	}
}

func TestAdjustTwoHostModules(t *testing.T) {
	a, b := mod("fa", "host", hst), mod("fb", "host", hst)
	c := catalogue(t, a, b)
	rb := hostResult()
	rb.GlibcRequired = ""
	adj, err := adjuster(c, fakeHosts{"fa-host": hostResult(), "fb-host": rb}).Adjust(podFor(c, "main", "fa-host", "fb-host"), ctr("main"))
	if err != nil {
		t.Fatal(err)
	}
	hooks := adj.GetHooks().GetCreateContainer()
	want := []string{"module-hook", "--deps", "/opt/modules/fa/host/deps", "--glibc", "fa-host=2.36", "--deps", "/opt/modules/fb/host/deps"}
	if len(hooks) != 1 || !reflect.DeepEqual(hooks[0].Args, want) {
		t.Errorf("hooks = %+v", hooks)
	}
}

func TestAdjustHostModuleWithoutDeps(t *testing.T) {
	c := catalogue(t, mod("fab", "host", hst))
	r := hostResult()
	r.Deps, r.GlibcRequired = nil, ""
	adj, err := adjuster(c, fakeHosts{"fab-host": r}).Adjust(podFor(c, "main", "fab-host"), ctr("main"))
	if err != nil {
		t.Fatal(err)
	}
	if len(adj.GetHooks().GetCreateContainer()) != 0 {
		t.Error("no deps and no glibc requirement: no hook")
	}
}

func TestAdjustPVCModule(t *testing.T) {
	c := catalogue(t, mod("sp", "1", pvc))
	adj, err := adjuster(c, nil).Adjust(podFor(c, "main", "sp-1"), ctr("main"))
	if err != nil {
		t.Fatal(err)
	}
	b := mountsOf(adj)["/opt/modules/sp/1"]
	if b == nil || b.Source != "/var/lib/pv/nccl/1" || !reflect.DeepEqual(b.Options, []string{"rbind", "ro", "nosuid", "nodev", "rprivate"}) {
		t.Errorf("pvc bind = %+v", b)
	}
	if _, ok := envOf(adj)["LD_LIBRARY_PATH"]; ok {
		t.Error("no lib field: LD_LIBRARY_PATH must not be set")
	}
}

func TestAdjustIgnoresPlainPodsAndUnlistedContainers(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	a := adjuster(c, nil)
	if adj, err := a.Adjust(&api.PodSandbox{Annotations: map[string]string{"other": "1"}}, ctr("main")); adj != nil || err != nil {
		t.Errorf("plain pod: %v %v", adj, err)
	}
	if adj, err := a.Adjust(podFor(c, "main", "x-1"), ctr("init-setup")); adj != nil || err != nil {
		t.Errorf("unlisted container: %v %v", adj, err)
	}
}

func TestAdjustIgnoresPlainPods(t *testing.T) {
	if adj, err := adjuster(fakeCatalogue{}, nil).Adjust(&api.PodSandbox{}, ctr("main")); adj != nil || err != nil {
		t.Errorf("pod without annotations: %v %v", adj, err)
	}
}

func TestAdjustRejectsCatalogueMismatch(t *testing.T) {
	x := mod("x", "1", img)
	c := catalogue(t, x)
	key := v1.AnnotationResolvedPrefix + "x"
	a := adjuster(c, nil)
	cases := map[string]func(*api.PodSandbox){
		"forged field": func(p *api.PodSandbox) { p.Annotations[key] = "x-1;rank=10;src=host;mount=/opt/modules/x/1" },
		"unknown":      func(p *api.PodSandbox) { p.Annotations[key] = "ghost-1;rank=10;src=image;mount=/g" },
	}
	for name, mutate := range cases {
		p := podFor(c, "main", "x-1")
		mutate(p)
		_, err := a.Adjust(p, ctr("main"))
		if err == nil || !strings.Contains(err.Error(), "does not match the Module catalogue") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	x.Status.Resolved = nil // Module became unresolved after admission
	if _, err := a.Adjust(podFor(catalogue(t, mod("x", "1", img)), "main", "x-1"), ctr("main")); err == nil {
		t.Error("unresolved module must be refused")
	}
}

func TestAdjustHostModuleNotReady(t *testing.T) {
	c := catalogue(t, mod("fab", "host", hst))
	for name, hosts := range map[string]fakeHosts{
		"not discovered": {},
		"not ready":      {"fab-host": {Message: "root libfab.so.1 not in /etc/ld.so.cache"}},
	} {
		_, err := adjuster(c, hosts).Adjust(podFor(c, "main", "fab-host"), ctr("main"))
		if err == nil || !strings.HasPrefix(err.Error(), "host module fab-host is not available on node n1: ") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestAdjustPVCNotConfigured(t *testing.T) {
	m := mod("sp", "1", v1.ModuleSource{PVC: &v1.PVCSource{ClaimName: "other", SubPath: "x"}})
	c := catalogue(t, m)
	_, err := adjuster(c, nil).Adjust(podFor(c, "main", "sp-1"), ctr("main"))
	if err == nil || !strings.Contains(err.Error(), "pvc claim other is not mounted by module-nri; add it to nri.pvcs") {
		t.Errorf("err = %v", err)
	}
}

func TestAdjustDeterministic(t *testing.T) {
	c := catalogue(t, mod("a", "1", img), mod("b", "1", img), mod("fab", "host", hst))
	a := adjuster(c, fakeHosts{"fab-host": hostResult()})
	first, _ := a.Adjust(podFor(c, "main", "a-1", "b-1", "fab-host"), ctr("main"))
	for range 20 {
		again, _ := a.Adjust(podFor(c, "main", "a-1", "b-1", "fab-host"), ctr("main"))
		if !reflect.DeepEqual(first, again) {
			t.Fatal("Adjust is not deterministic")
		}
	}
}

func TestParseValue(t *testing.T) {
	v, err := ParseValue("aws-1.0;rank=20;src=pvc:spack/nccl/1;mount=/opt/modules/aws/1.0;lib=/a:/b;mask=/m")
	if err != nil {
		t.Fatal(err)
	}
	want := ModuleValue{Name: "aws-1.0", Rank: 20, Src: "pvc:spack/nccl/1", Mount: "/opt/modules/aws/1.0", Lib: []string{"/a", "/b"}, Mask: []string{"/m"}}
	if !reflect.DeepEqual(v, want) {
		t.Errorf("got %+v", v)
	}
	for _, bad := range []string{"", "x;rank=a;src=image;mount=/m", "x;src=image", "x;rank=10;junk;mount=/m"} {
		if _, err := ParseValue(bad); err == nil {
			t.Errorf("ParseValue(%q) must fail", bad)
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

<!-- file: internal/nri/mountinfo_test.go -->
```go
package nri

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const selfInfo = `22 1 0:21 / / rw - overlay overlay rw
40 22 254:1 /var/lib/brics-test/pvc /pvcs/spack-apps ro,relatime - ext4 /dev/vda1 rw
41 22 0:55 /vol/pv-nfs /pvcs/nfs\040claim ro - nfs4 srv:/export rw
`

const hostInfo = `1 0 254:1 / / rw - ext4 /dev/vda1 rw
30 1 254:1 /var/lib /var/lib rw - ext4 /dev/vda1 rw
50 1 0:55 / /var/lib/kubelet/pods/u/volumes/kubernetes.io~nfs/pv-nfs rw - nfs4 srv:/export rw
`

func TestHostPathFor(t *testing.T) {
	self, err := ParseMountInfo([]byte(selfInfo))
	if err != nil {
		t.Fatal(err)
	}
	host, err := ParseMountInfo([]byte(hostInfo))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"/pvcs/spack-apps": "/var/lib/brics-test/pvc", // longest matching root (/var/lib) wins
		"/pvcs/nfs claim":  "/var/lib/kubelet/pods/u/volumes/kubernetes.io~nfs/pv-nfs/vol/pv-nfs",
	}
	for mp, want := range cases {
		if got, err := HostPathFor(self, host, mp); err != nil || got != want {
			t.Errorf("HostPathFor(%q) = %q, %v; want %q", mp, got, err, want)
		}
	}
	if _, err := HostPathFor(self, host, "/pvcs/absent"); err == nil {
		t.Error("absent mount must fail")
	}
	if _, err := HostPathFor(self, nil, "/pvcs/spack-apps"); err == nil {
		t.Error("no host match must fail")
	}
}

func TestMountinfoPVCs(t *testing.T) {
	dir := t.TempDir()
	s, h := filepath.Join(dir, "self"), filepath.Join(dir, "host")
	_ = os.WriteFile(s, []byte(selfInfo), 0o644)
	_ = os.WriteFile(h, []byte(hostInfo), 0o644)
	p := &MountinfoPVCs{Claims: map[string]bool{"spack-apps": true}, Dir: "/pvcs", SelfMountinfo: s, HostMountinfo: h}
	if got, err := p.HostPath("spack-apps"); err != nil || got != "/var/lib/brics-test/pvc" {
		t.Errorf("HostPath = %q, %v", got, err)
	}
	if _, err := p.HostPath("other"); !errors.Is(err, ErrClaimNotMounted) {
		t.Errorf("unconfigured claim: %v", err)
	}
}
```

- [ ] **Step 3: Run** `go test ./internal/nri/`. Expected: FAIL, undefined names.

- [ ] **Step 4: Implement**

<!-- file: internal/nri/value.go -->
```go
// Package nri is the module system's NRI plugin: it applies the modules a
// pod's resolved.modules.brics.ac.uk annotations name when its containers
// are created.
package nri

import (
	"fmt"
	"strconv"
	"strings"
)

// ModuleValue is one parsed resolved.modules.brics.ac.uk/<family> value.
type ModuleValue struct {
	Name  string
	Rank  int
	Src   string
	Mount string
	Lib   []string
	Mask  []string
}

// ParseValue parses "name;rank=N;src=S;mount=M[;lib=a:b][;mask=a:b]".
func ParseValue(v string) (ModuleValue, error) {
	parts := strings.Split(v, ";")
	mv := ModuleValue{Name: parts[0], Rank: -1}
	for _, p := range parts[1:] {
		k, val, ok := strings.Cut(p, "=")
		if !ok {
			return mv, fmt.Errorf("malformed module annotation field %q", p)
		}
		switch k {
		case "rank":
			n, err := strconv.Atoi(val)
			if err != nil {
				return mv, fmt.Errorf("malformed rank %q", val)
			}
			mv.Rank = n
		case "src":
			mv.Src = val
		case "mount":
			mv.Mount = val
		case "lib":
			mv.Lib = strings.Split(val, ":")
		case "mask":
			mv.Mask = strings.Split(val, ":")
		}
	}
	if mv.Name == "" || mv.Rank < 0 || mv.Src == "" || mv.Mount == "" {
		return mv, fmt.Errorf("module annotation %q lacks name, rank, src or mount", v)
	}
	return mv, nil
}
```

<!-- file: internal/nri/ldpath.go -->
```go
package nri

import "strings"

// PrependLibraryPath returns dirs followed by the container's current
// LD_LIBRARY_PATH, without duplicates or empty entries. The first
// LD_LIBRARY_PATH in env wins, matching glibc getenv. Ported from the CXI
// DRA driver (internal/cdi/env.go).
func PrependLibraryPath(env []string, dirs []string) string {
	var current string
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "LD_LIBRARY_PATH="); ok {
			current = v
			break
		}
	}
	seen := map[string]bool{}
	var out []string
	add := func(d string) {
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	for _, d := range dirs {
		add(d)
	}
	for d := range strings.SplitSeq(current, ":") {
		add(d)
	}
	return strings.Join(out, ":")
}
```

<!-- file: internal/nri/adjust.go -->
```go
package nri

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	api "github.com/containerd/nri/pkg/api"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	"github.com/bristol-supercomputing/k8s-modules/internal/discovery"
)

// Catalogue looks up Modules, normally from an informer cache.
type Catalogue interface {
	Module(name string) (*v1.Module, bool)
}

// HostModules returns this node's latest discovery result for a module.
type HostModules interface {
	HostModule(name string) (discovery.Result, bool)
}

// PVCs locates the host path of a claim the DaemonSet mounts.
type PVCs interface {
	HostPath(claim string) (string, error)
}

// Adjuster turns a pod's module annotations into a container adjustment.
type Adjuster struct {
	Catalogue Catalogue
	Hosts     HostModules
	PVCs      PVCs
	NodeName  string
	HookPath  string
}

var (
	bindOptions  = []string{"bind", "ro", "nosuid", "nodev", "rprivate"}
	rbindOptions = []string{"rbind", "ro", "nosuid", "nodev", "rprivate"}
	maskOptions  = []string{"ro", "nosuid", "nodev", "noexec", "size=0"}
)

// Adjust returns nil for containers that load no modules. Every error
// fails container creation, so pods never start without their modules.
func (a *Adjuster) Adjust(pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, error) {
	anns := pod.GetAnnotations()
	var keys []string
	for k := range anns {
		if strings.HasPrefix(k, v1.AnnotationResolvedPrefix) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 || !slices.Contains(strings.Split(anns[v1.AnnotationContainers], ","), ctr.GetName()) {
		return nil, nil
	}
	sort.Strings(keys)

	mods := make([]ModuleValue, 0, len(keys))
	for _, k := range keys {
		v := anns[k]
		name, _, _ := strings.Cut(v, ";")
		m, ok := a.Catalogue.Module(name)
		if !ok || m.Status.Resolved == nil || m.Status.Resolved.Annotations[k] != v {
			return nil, fmt.Errorf("module annotation %s=%s does not match the Module catalogue (not found, not resolved, or changed); the pod must be recreated", k, v)
		}
		mv, err := ParseValue(v)
		if err != nil {
			return nil, err
		}
		mods = append(mods, mv)
	}
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].Rank != mods[j].Rank {
			return mods[i].Rank > mods[j].Rank
		}
		return mods[i].Name < mods[j].Name
	})

	adj := &api.ContainerAdjustment{}
	var libDirs, names, hookArgs []string
	for _, m := range mods {
		names = append(names, m.Name)
		libDirs = append(libDirs, m.Lib...)
		for _, p := range m.Mask {
			adj.AddMount(&api.Mount{Destination: p, Type: "tmpfs", Source: "tmpfs", Options: maskOptions})
		}
		switch {
		case m.Src == "host":
			r, ok := a.Hosts.HostModule(m.Name)
			if !ok || !r.Ready {
				why := "not discovered yet"
				if ok {
					why = r.Message
				}
				return nil, fmt.Errorf("host module %s is not available on node %s: %s", m.Name, a.NodeName, why)
			}
			for _, l := range r.Lib {
				adj.AddMount(&api.Mount{Destination: path.Join(m.Mount, "lib", l.Soname), Type: "bind", Source: l.Path, Options: bindOptions})
			}
			for _, l := range r.Deps {
				adj.AddMount(&api.Mount{Destination: path.Join(m.Mount, "deps", l.Soname), Type: "bind", Source: l.Path, Options: bindOptions})
			}
			if len(r.Deps) > 0 {
				hookArgs = append(hookArgs, "--deps", path.Join(m.Mount, "deps"))
			}
			if r.GlibcRequired != "" {
				hookArgs = append(hookArgs, "--glibc", m.Name+"="+r.GlibcRequired)
			}
		case strings.HasPrefix(m.Src, "pvc:"):
			claim, sub, _ := strings.Cut(strings.TrimPrefix(m.Src, "pvc:"), "/")
			hp, err := a.PVCs.HostPath(claim)
			if err != nil {
				return nil, err
			}
			adj.AddMount(&api.Mount{Destination: m.Mount, Type: "bind", Source: path.Join(hp, sub), Options: rbindOptions})
		}
	}
	if len(libDirs) > 0 {
		adj.AddEnv("LD_LIBRARY_PATH", PrependLibraryPath(ctr.GetEnv(), libDirs))
	}
	adj.AddEnv("BRICS_MODULES", strings.Join(names, ":"))
	if len(hookArgs) > 0 {
		adj.AddHooks(&api.Hooks{CreateContainer: []*api.Hook{{
			Path: a.HookPath,
			Args: append([]string{"module-hook"}, hookArgs...),
		}}})
	}
	return adj, nil
}
```

<!-- file: internal/nri/mountinfo.go -->
```go
package nri

import (
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
)

// ErrClaimNotMounted reports a pvc module whose claim the DaemonSet does
// not mount.
var ErrClaimNotMounted = errors.New("not mounted by module-nri")

// MountInfo is one /proc/<pid>/mountinfo line.
type MountInfo struct {
	MajMin     string
	Root       string
	MountPoint string
}

// ParseMountInfo parses mountinfo, unescaping octal sequences such as \040.
func ParseMountInfo(data []byte) ([]MountInfo, error) {
	var out []MountInfo
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 5 {
			return nil, fmt.Errorf("malformed mountinfo line %q", line)
		}
		out = append(out, MountInfo{MajMin: f[2], Root: unescape(f[3]), MountPoint: unescape(f[4])})
	}
	return out, nil
}

func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// HostPathFor maps our mount at mountPoint to a path in the host's mount
// namespace: the host mount of the same device whose root is the longest
// prefix of ours, plus the remainder of our root.
func HostPathFor(self, host []MountInfo, mountPoint string) (string, error) {
	var mine *MountInfo
	for i := range self {
		if self[i].MountPoint == mountPoint {
			mine = &self[i] // the last mount at a path is the visible one
		}
	}
	if mine == nil {
		return "", fmt.Errorf("%s is not a mount point", mountPoint)
	}
	var best *MountInfo
	for i := range host {
		h := &host[i]
		if h.MajMin != mine.MajMin || !within(mine.Root, h.Root) {
			continue
		}
		if best == nil || len(h.Root) > len(best.Root) {
			best = h
		}
	}
	if best == nil {
		return "", fmt.Errorf("no host mount of device %s contains %s", mine.MajMin, mine.Root)
	}
	rest := strings.TrimPrefix(mine.Root, best.Root)
	return path.Join(best.MountPoint, rest), nil
}

func within(p, root string) bool {
	return root == "/" || p == root || strings.HasPrefix(p, root+"/")
}

// MountinfoPVCs resolves claims mounted at Dir/<claim> via mountinfo.
type MountinfoPVCs struct {
	Claims        map[string]bool
	Dir           string
	SelfMountinfo string
	HostMountinfo string
}

func (p *MountinfoPVCs) HostPath(claim string) (string, error) {
	if !p.Claims[claim] {
		return "", fmt.Errorf("pvc claim %s is %w; add it to nri.pvcs", claim, ErrClaimNotMounted)
	}
	self, err := readMountInfo(p.SelfMountinfo)
	if err != nil {
		return "", err
	}
	host, err := readMountInfo(p.HostMountinfo)
	if err != nil {
		return "", err
	}
	hp, err := HostPathFor(self, host, path.Join(p.Dir, claim))
	if err != nil {
		return "", fmt.Errorf("cannot locate host path for pvc claim %s: %w", claim, err)
	}
	return hp, nil
}

func readMountInfo(file string) ([]MountInfo, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return ParseMountInfo(data)
}
```

- [ ] **Step 5: Run** `go test ./internal/nri/ -v -count=1`. Expected: PASS. (`TestAdjustPVCNotConfigured` uses the fake's message, which matches `MountinfoPVCs`'s wording.)

- [ ] **Step 6: Commit** — `git add go.mod go.sum internal/nri && git commit -m "feat(nri): container adjustments, catalogue check and PVC host paths"`

---

### Task 7: Plugin runtime, report writer and binary

**Files:**
- Create: `internal/nri/state.go`, `internal/nri/catalogue.go`, `internal/nri/report.go`, `internal/nri/plugin.go`, `internal/nri/report_test.go`, `cmd/module-nri/main.go`, `Dockerfile.nri`
- Modify: `Makefile` (`build` builds every command; add an `image-nri` target)

**Interfaces:**
- Consumes: Tasks 4 and 6.
- Produces:
  - `nri.HostState`, with the methods `Set(map[string]discovery.Result)` and `HostModule`
  - `nri.CacheCatalogue{Reader client.Reader}`
  - `nri.BuildReport(modules []v1.Module, results map[string]discovery.Result) []v1.HostModuleReport`
  - `nri.Discoverer{HostRoot, Arch, NodeName string; Reader client.Reader; Writer client.Client; State *HostState; Log logr.Logger}`, with the methods `RunOnce(ctx)` and `Run(ctx, interval, trigger)`
  - `nri.Plugin{Adjuster *Adjuster; Log logr.Logger}`, implementing the stub `CreateContainer`
  - image `module-nri`, holding `/module-nri` and `/module-hook`; `module-nri install-hook --to <dir>` copies the hook

- [ ] **Step 1: Failing test**

<!-- file: internal/nri/report_test.go -->
```go
package nri

import (
	"reflect"
	"testing"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	"github.com/bristol-supercomputing/k8s-modules/internal/discovery"
)

func TestBuildReport(t *testing.T) {
	ms := []v1.Module{*mod("zz", "host", hst), *mod("img", "1", img), *mod("aa", "host", hst)}
	results := map[string]discovery.Result{
		"zz-host": hostResult(),
		"aa-host": {Missing: []string{"libfab.so.1"}, Message: "root libfab.so.1 not in /etc/ld.so.cache"},
	}
	got := BuildReport(ms, results)
	if len(got) != 2 || got[0].Module != "aa-host" || got[1].Module != "zz-host" {
		t.Fatalf("report must list host modules sorted by name: %+v", got)
	}
	if got[0].Ready || got[0].Family != "aa" || got[0].Message == "" || !reflect.DeepEqual(got[0].Missing, []string{"libfab.so.1"}) {
		t.Errorf("not-ready entry: %+v", got[0])
	}
	z := got[1]
	if !z.Ready || z.GlibcRequired != "2.36" || z.Lib[0] != (v1.LibraryFile{Soname: "libfab.so.1", Path: "/usr/lib64/libfab.so.1.2"}) || len(z.Deps) != 1 {
		t.Errorf("ready entry: %+v", z)
	}
}

func TestHostState(t *testing.T) {
	var s HostState
	if _, ok := s.HostModule("x"); ok {
		t.Error("empty state")
	}
	s.Set(map[string]discovery.Result{"x": {Ready: true}})
	if r, ok := s.HostModule("x"); !ok || !r.Ready {
		t.Error("state lookup")
	}
}
```

Run `go test ./internal/nri/`. Expected: FAIL, undefined `BuildReport` and `HostState`.

- [ ] **Step 2: Implement**

<!-- file: internal/nri/state.go -->
```go
package nri

import (
	"sync/atomic"

	"github.com/bristol-supercomputing/k8s-modules/internal/discovery"
)

// HostState holds the latest discovery results. Discovery replaces the
// whole map at once, so CreateContainer never sees a half-updated state.
type HostState struct {
	results atomic.Pointer[map[string]discovery.Result]
}

func (s *HostState) Set(m map[string]discovery.Result) { s.results.Store(&m) }

func (s *HostState) HostModule(name string) (discovery.Result, bool) {
	m := s.results.Load()
	if m == nil {
		return discovery.Result{}, false
	}
	r, ok := (*m)[name]
	return r, ok
}
```

<!-- file: internal/nri/catalogue.go -->
```go
package nri

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

// CacheCatalogue reads Modules from an informer-backed reader, so lookups
// are in-memory and fit containerd's per-request timeout.
type CacheCatalogue struct{ Reader client.Reader }

func (c CacheCatalogue) Module(name string) (*v1.Module, bool) {
	m := &v1.Module{}
	if err := c.Reader.Get(context.Background(), client.ObjectKey{Name: name}, m); err != nil {
		return nil, false
	}
	return m, true
}
```

<!-- file: internal/nri/report.go -->
```go
package nri

import (
	"context"
	"sort"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	"github.com/bristol-supercomputing/k8s-modules/internal/discovery"
)

// BuildReport lists every host Module's result, sorted by module name.
func BuildReport(modules []v1.Module, results map[string]discovery.Result) []v1.HostModuleReport {
	var out []v1.HostModuleReport
	for _, m := range modules {
		if m.Spec.Source.Host == nil {
			continue
		}
		r := results[m.Name]
		out = append(out, v1.HostModuleReport{
			Module: m.Name, Family: m.Spec.Family, Ready: r.Ready,
			ClosureHash: r.Hash, GlibcRequired: r.GlibcRequired,
			Lib: files(r.Lib), Deps: files(r.Deps), Missing: r.Missing, Message: r.Message,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Module < out[j].Module })
	return out
}

func files(ls []discovery.Library) []v1.LibraryFile {
	var out []v1.LibraryFile
	for _, l := range ls {
		out = append(out, v1.LibraryFile{Soname: l.Soname, Path: l.Path})
	}
	return out
}

// Discoverer runs discovery for every host Module and publishes the result
// to the in-memory state and to the node's ModuleNodeReport.
type Discoverer struct {
	HostRoot, Arch, NodeName string
	Reader                   client.Reader // informer cache, for Modules
	Writer                   client.Client // direct client, for the report
	State                    *HostState
	Log                      logr.Logger
}

func (d *Discoverer) RunOnce(ctx context.Context) error {
	var list v1.ModuleList
	if err := d.Reader.List(ctx, &list); err != nil {
		return err
	}
	host, hostErr := discovery.NewHost(d.HostRoot, d.Arch)
	results := map[string]discovery.Result{}
	for _, m := range list.Items {
		h := m.Spec.Source.Host
		if h == nil {
			continue
		}
		if hostErr != nil {
			results[m.Name] = discovery.Result{Message: hostErr.Error()}
			continue
		}
		results[m.Name] = host.Discover(h.Discover.Roots, h.PreferHost)
	}
	d.State.Set(results)
	return d.writeReport(ctx, BuildReport(list.Items, results))
}

func (d *Discoverer) writeReport(ctx context.Context, modules []v1.HostModuleReport) error {
	rep := &v1.ModuleNodeReport{}
	err := d.Writer.Get(ctx, client.ObjectKey{Name: d.NodeName}, rep)
	if apierrors.IsNotFound(err) {
		node := &corev1.Node{}
		if err := d.Writer.Get(ctx, client.ObjectKey{Name: d.NodeName}, node); err != nil {
			return err
		}
		rep = &v1.ModuleNodeReport{ObjectMeta: metav1.ObjectMeta{
			Name:            d.NodeName,
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Node", Name: node.Name, UID: node.UID}},
		}}
		if err := d.Writer.Create(ctx, rep); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if rep.Status.DiscoveredAt != nil && equality.Semantic.DeepEqual(rep.Status.Modules, modules) {
		return nil
	}
	now := metav1.Now()
	rep.Status = v1.ModuleNodeReportStatus{DiscoveredAt: &now, Modules: modules}
	return d.Writer.Status().Update(ctx, rep)
}

// Run repeats discovery every interval and whenever trigger fires.
func (d *Discoverer) Run(ctx context.Context, interval time.Duration, trigger <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-trigger:
		}
		if err := d.RunOnce(ctx); err != nil {
			d.Log.Error(err, "discovery")
		}
	}
}
```

<!-- file: internal/nri/plugin.go -->
```go
package nri

import (
	"context"
	"fmt"

	api "github.com/containerd/nri/pkg/api"
	"github.com/go-logr/logr"
)

// Plugin is the NRI stub handler.
type Plugin struct {
	Adjuster *Adjuster
	Log      logr.Logger
}

func (p *Plugin) CreateContainer(_ context.Context, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	adj, err := p.Adjuster.Adjust(pod, ctr)
	id := pod.GetNamespace() + "/" + pod.GetName() + "/" + ctr.GetName()
	if err != nil {
		p.Log.Info("refusing container", "container", id, "reason", err.Error())
		return nil, nil, fmt.Errorf("modules: %w", err)
	}
	if adj != nil {
		p.Log.Info("applied modules", "container", id, "modules", envValue(adj, "BRICS_MODULES"))
	}
	return adj, nil, nil
}

func envValue(adj *api.ContainerAdjustment, key string) string {
	for _, kv := range adj.GetEnv() {
		if kv.Key == key {
			return kv.Value
		}
	}
	return ""
}
```

<!-- file: cmd/module-nri/main.go -->
```go
// Command module-nri is the module system's NRI plugin. It runs as a
// DaemonSet on every node.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/containerd/nri/pkg/stub"
	"github.com/go-logr/logr"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	"github.com/bristol-supercomputing/k8s-modules/internal/nri"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "install-hook" {
		if err := installHook(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "install-hook:", err)
			os.Exit(1)
		}
		return
	}
	var (
		nodeName, hostRoot, hookPath, pvcDir, pvcs, socket, idx string
		interval                                              time.Duration
	)
	flag.StringVar(&nodeName, "node-name", os.Getenv("NODE_NAME"), "This node's name.")
	flag.StringVar(&hostRoot, "host-root", "/host", "Where the host root filesystem is mounted.")
	flag.StringVar(&hookPath, "hook-path", "/opt/brics-modules/bin/module-hook", "Host path of module-hook.")
	flag.StringVar(&pvcDir, "pvc-dir", "/pvcs", "Directory holding one mount per configured claim.")
	flag.StringVar(&pvcs, "pvcs", "", "Comma-separated claims mounted under --pvc-dir.")
	flag.StringVar(&socket, "nri-socket", "/var/run/nri/nri.sock", "NRI socket.")
	flag.StringVar(&idx, "plugin-index", "50", "NRI plugin index.")
	flag.DurationVar(&interval, "discovery-interval", 10*time.Minute, "Host module rediscovery interval.")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("module-nri")
	if nodeName == "" {
		log.Error(nil, "--node-name or NODE_NAME is required")
		os.Exit(1)
	}
	if err := run(log, nodeName, hostRoot, hookPath, pvcDir, pvcs, socket, idx, interval); err != nil {
		log.Error(err, "exiting")
		os.Exit(1)
	}
}

func run(log logr.Logger, nodeName, hostRoot, hookPath, pvcDir, pvcs, socket, idx string, interval time.Duration) error {
	ctx := ctrl.SetupSignalHandler()
	cfg := ctrl.GetConfigOrDie()
	scheme := kruntime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return err
	}
	if err := v1.AddToScheme(scheme); err != nil {
		return err
	}
	modules, err := cache.New(cfg, cache.Options{Scheme: scheme})
	if err != nil {
		return err
	}
	trigger := make(chan struct{}, 1)
	poke := func(obj any) {
		if m, ok := obj.(*v1.Module); ok && m.Spec.Source.Host == nil {
			return
		}
		select {
		case trigger <- struct{}{}:
		default:
		}
	}
	inf, err := modules.GetInformer(ctx, &v1.Module{})
	if err != nil {
		return err
	}
	if _, err := inf.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    poke,
		DeleteFunc: func(obj any) { poke(nil) },
	}); err != nil {
		return err
	}
	go func() { _ = modules.Start(ctx) }()
	if !modules.WaitForCacheSync(ctx) {
		return fmt.Errorf("module cache did not sync")
	}
	writer, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	state := &nri.HostState{}
	d := &nri.Discoverer{HostRoot: hostRoot, Arch: runtime.GOARCH, NodeName: nodeName,
		Reader: modules, Writer: writer, State: state, Log: log}
	if err := d.RunOnce(ctx); err != nil {
		log.Error(err, "initial discovery")
	}
	go d.Run(ctx, interval, trigger)

	claims := map[string]bool{}
	for _, c := range strings.Split(pvcs, ",") {
		if c = strings.TrimSpace(c); c != "" {
			claims[c] = true
		}
	}
	p := &nri.Plugin{Log: log, Adjuster: &nri.Adjuster{
		Catalogue: nri.CacheCatalogue{Reader: modules},
		Hosts:     state,
		PVCs: &nri.MountinfoPVCs{Claims: claims, Dir: pvcDir,
			SelfMountinfo: "/proc/self/mountinfo", HostMountinfo: filepath.Join(hostRoot, "proc/1/mountinfo")},
		NodeName: nodeName,
		HookPath: hookPath,
	}}
	s, err := stub.New(p, stub.WithPluginName(v1.NRIPluginName), stub.WithPluginIdx(idx), stub.WithSocketPath(socket))
	if err != nil {
		return err
	}
	// Run returns when containerd drops the connection (for example on a
	// containerd restart); exiting lets kubelet restart us to reconnect.
	if err := s.Run(ctx); err != nil {
		return err
	}
	return fmt.Errorf("NRI connection closed")
}

func installHook(args []string) error {
	fs := flag.NewFlagSet("install-hook", flag.ContinueOnError)
	to := fs.String("to", "/opt/brics-modules/bin", "host directory to install module-hook into")
	from := fs.String("from", "/module-hook", "module-hook binary in this image")
	if err := fs.Parse(args); err != nil {
		return err
	}
	src, err := os.Open(*from)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.MkdirAll(*to, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(*to, ".module-hook-")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(*to, "module-hook"))
}
```

<!-- file: Dockerfile.nri -->
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
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/module-nri ./cmd/module-nri && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/module-hook ./cmd/module-hook

# Runs as root: the plugin reads host library files and host mount tables.
FROM gcr.io/distroless/static
COPY --from=build /out/module-nri /module-nri
COPY --from=build /out/module-hook /module-hook
ENTRYPOINT ["/module-nri"]
```

Makefile changes:
- In `build`, replace the go build line with `go build -o $(LOCALBIN)/ ./cmd/...`.
- Add `NRI_IMG ?= ghcr.io/bristol-supercomputing/module-nri:dev`.
- Add this target:

```make
image-nri:
	$(CONTAINER_CLI) build $(BUILD_ARGS) -f Dockerfile.nri -t $(NRI_IMG) .
```

- Add `image-nri` to `.PHONY`.

- [ ] **Step 3: Run** `go test ./internal/nri/ -count=1 && make build && GOOS=linux go build -o /dev/null ./cmd/module-nri`. Expected: PASS, and both builds succeed.

- [ ] **Step 4: Commit** — `git add internal/nri cmd/module-nri Dockerfile.nri Makefile go.mod go.sum && git commit -m "feat(nri): plugin runtime, discovery loop and node report"`

---

### Task 8: Node labels from reports

**Files:**
- Create: `internal/controller/nodelabel_controller.go`, `internal/controller/nodelabel_test.go`, `test/envtest/nodelabel_test.go`
- Modify: `cmd/module-controller/main.go` (register), `test/envtest/suite_test.go` (register; export `restCfg`)

**Interfaces:**
- Produces:
  - `controller.NodeLabelReconciler{client.Client}`, with the method `SetupWithManager`
  - `controller.HostLabels(current map[string]string, rep *v1.ModuleNodeReport) (map[string]string, bool)`
  - test global `restCfg *rest.Config`

- [ ] **Step 1: Failing tests**

<!-- file: internal/controller/nodelabel_test.go -->
```go
package controller

import (
	"reflect"
	"testing"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

func TestHostLabels(t *testing.T) {
	rep := &v1.ModuleNodeReport{Status: v1.ModuleNodeReportStatus{Modules: []v1.HostModuleReport{
		{Module: "fab-host", Family: "fab", Ready: true},
		{Module: "cxi-host", Family: "cxi", Ready: false},
	}}}
	cur := map[string]string{"keep": "me", v1.LabelHostPrefix + "stale": "true", v1.LabelHostPrefix + "cxi": "true"}
	got, changed := HostLabels(cur, rep)
	want := map[string]string{"keep": "me", v1.LabelHostPrefix + "fab": "true"}
	if !changed || !reflect.DeepEqual(got, want) {
		t.Errorf("got %v changed=%v, want %v", got, changed, want)
	}
	if _, changed := HostLabels(want, rep); changed {
		t.Error("already correct labels must report no change")
	}
	got, changed = HostLabels(want, nil)
	if !changed || !reflect.DeepEqual(got, map[string]string{"keep": "me"}) {
		t.Errorf("no report must remove every host label: %v", got)
	}
	if cur[v1.LabelHostPrefix+"stale"] != "true" {
		t.Error("HostLabels must not mutate its input")
	}
}
```

<!-- file: test/envtest/nodelabel_test.go -->
```go
package integration

import (
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

func TestNodeLabelsFromReport(t *testing.T) {
	name := fmt.Sprintf("label-node-%d", familySeq.Add(1))
	create(t, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name,
		Labels: map[string]string{"keep": "me", v1.LabelHostPrefix + "stale": "true"}}})
	rep := &v1.ModuleNodeReport{ObjectMeta: metav1.ObjectMeta{Name: name}}
	create(t, rep)

	setReady := func(ready bool) {
		t.Helper()
		eventually(t, 5*time.Second, func() error {
			if err := k8s.Get(ctx, client.ObjectKeyFromObject(rep), rep); err != nil {
				return err
			}
			rep.Status.Modules = []v1.HostModuleReport{{Module: "fab-host", Family: "fab", Ready: ready}}
			return k8s.Status().Update(ctx, rep)
		})
	}
	expect := func(want map[string]string) {
		t.Helper()
		eventually(t, 15*time.Second, func() error {
			n := &corev1.Node{}
			if err := k8s.Get(ctx, client.ObjectKey{Name: name}, n); err != nil {
				return err
			}
			for k, v := range want {
				got, ok := n.Labels[k]
				if v == "" && ok || v != "" && got != v {
					return fmt.Errorf("label %s = %q (present=%v), want %q", k, got, ok, v)
				}
			}
			return nil
		})
	}
	fab, stale := v1.LabelHostPrefix+"fab", v1.LabelHostPrefix+"stale"

	setReady(true)
	expect(map[string]string{fab: "true", stale: "", "keep": "me"})
	setReady(false)
	expect(map[string]string{fab: "", "keep": "me"})
	setReady(true)
	expect(map[string]string{fab: "true"})
	if err := k8s.Delete(ctx, rep); err != nil {
		t.Fatal(err)
	}
	expect(map[string]string{fab: "", "keep": "me"})
}
```

Run `go test ./internal/controller/`. Expected: FAIL, undefined `HostLabels`.

- [ ] **Step 2: Implement**

<!-- file: internal/controller/nodelabel_controller.go -->
```go
package controller

import (
	"context"
	"maps"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

// NodeLabelReconciler labels each node modules.brics.ac.uk/host-<family>
// for every host module its ModuleNodeReport shows ready.
type NodeLabelReconciler struct {
	client.Client
}

func (r *NodeLabelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("nodelabels").
		// Nodes change constantly (heartbeats); only label edits matter.
		For(&corev1.Node{}, builder.WithPredicates(predicate.LabelChangedPredicate{})).
		Watches(&v1.ModuleNodeReport{}, handler.EnqueueRequestsFromMapFunc(
			func(_ context.Context, o client.Object) []reconcile.Request {
				return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: o.GetName()}}}
			})).
		Complete(r)
}

func (r *NodeLabelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	node := &corev1.Node{}
	if err := r.Get(ctx, req.NamespacedName, node); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	rep := &v1.ModuleNodeReport{}
	if err := r.Get(ctx, req.NamespacedName, rep); apierrors.IsNotFound(err) {
		rep = nil
	} else if err != nil {
		return ctrl.Result{}, err
	}
	labels, changed := HostLabels(node.Labels, rep)
	if !changed {
		return ctrl.Result{}, nil
	}
	orig := node.DeepCopy()
	node.Labels = labels
	return ctrl.Result{}, r.Patch(ctx, node, client.MergeFrom(orig))
}

// HostLabels returns current with its host-* labels replaced by those the
// report implies. Other labels are untouched; current is not modified.
func HostLabels(current map[string]string, rep *v1.ModuleNodeReport) (map[string]string, bool) {
	want := map[string]bool{}
	if rep != nil {
		for _, m := range rep.Status.Modules {
			if m.Ready {
				want[v1.LabelHostPrefix+m.Family] = true
			}
		}
	}
	out := maps.Clone(current)
	if out == nil {
		out = map[string]string{}
	}
	changed := false
	for k := range out {
		if strings.HasPrefix(k, v1.LabelHostPrefix) && !want[k] {
			delete(out, k)
			changed = true
		}
	}
	for k := range want {
		if out[k] != "true" {
			out[k] = "true"
			changed = true
		}
	}
	return out, changed
}
```

In `cmd/module-controller/main.go`, after the ModuleReconciler setup, add:

```go
	if err := (&controller.NodeLabelReconciler{Client: mgr.GetClient()}).SetupWithManager(mgr); err != nil {
		log.Error(err, "setting up node label controller")
		os.Exit(1)
	}
```

In `test/envtest/suite_test.go`:
- add the package variable `restCfg *rest.Config` (import `k8s.io/client-go/rest`);
- set `restCfg = cfg` after `env.Start()`;
- register `(&controller.NodeLabelReconciler{Client: mgr.GetClient()}).SetupWithManager(mgr)` beside the ModuleReconciler.

- [ ] **Step 3: Run** `go test ./internal/controller/ -count=1 && make test-envtest`. Expected: PASS.

- [ ] **Step 4: Commit** — `git add internal/controller cmd/module-controller test/envtest && git commit -m "feat(controller): label nodes from module node reports"`

---

### Task 9: Chart — DaemonSet, RBAC, report policy, admission changes

**Files:**
- Modify: `deploy/helm/k8s-modules/values.yaml`, `templates/rbac.yaml`, `templates/admission-mutating.yaml`, `templates/admission-pods.yaml`, `test/envtest/admission_test.go` (`TestAdmissionForgedAnnotation` expectation)
- Create: `templates/nri-daemonset.yaml`, `templates/nri-rbac.yaml`, `templates/admission-module-reports.yaml`, `deploy/helm/k8s-modules/README.md`, `test/envtest/nri_admission_test.go`

**Interfaces:**
- Produces:
  - the policy `module-reports.modules.brics.ac.uk`;
  - the DaemonSet `module-nri` and the ServiceAccount `module-nri`, both in `modulesNamespace`;
  - the values `nri.image.{repository,tag,pullPolicy}`, `nri.hookDir`, `nri.pvcs`, `nri.discoveryInterval` and `nri.resources`.

- [ ] **Step 1: Failing envtest**

<!-- file: test/envtest/nri_admission_test.go -->
```go
package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

func TestAdmissionNRIAnnotations(t *testing.T) {
	m := imageModule(newFamily("nriann"), "1")
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	p := admit(t, func() *corev1.Pod {
		return newPod(ns, m.Name, corev1.Container{Name: "a", Image: "busybox"}, corev1.Container{Name: "b", Image: "busybox"})
	}, hasAnnotations(map[string]string{v1.AnnotationContainers: "a,b", v1.AnnotationRequiredPlugins: v1.RequiredPluginsValue}))

	for key, value := range map[string]string{v1.AnnotationContainers: "a", v1.AnnotationRequiredPlugins: "[]"} {
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
	if _, ok := plain.Annotations[v1.AnnotationContainers]; ok {
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
	reject(t, written(nil), "modules.brics.ac.uk/containers must list the pod's containers")
	reject(t, written(map[string]string{v1.AnnotationContainers: "main"}), "required-plugins.noderesource.dev/pod must be")
	admit(t, written(map[string]string{v1.AnnotationContainers: "main", v1.AnnotationRequiredPlugins: v1.RequiredPluginsValue}),
		hasAnnotations(m.Status.Resolved.Annotations))
}

func TestModuleReportNodeIdentity(t *testing.T) {
	as := func(node string) client.Client {
		cfg := rest.CopyConfig(restCfg)
		cfg.Impersonate = rest.ImpersonationConfig{
			UserName: "system:serviceaccount:module-system:module-nri",
			Groups:   []string{"system:masters", "system:authenticated"},
			Extra:    map[string][]string{"authentication.kubernetes.io/node-name": {node}},
		}
		c, err := client.New(cfg, client.Options{Scheme: scheme})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	n := familySeq.Add(1)
	a, b := fmt.Sprintf("ident-a-%d", n), fmt.Sprintf("ident-b-%d", n)
	eventually(t, 10*time.Second, func() error {
		other := &v1.ModuleNodeReport{ObjectMeta: metav1.ObjectMeta{Name: b}}
		err := as(a).Create(ctx, other)
		if err == nil {
			_ = k8s.Delete(ctx, other)
			return fmt.Errorf("node %s wrote %s's report", a, b)
		}
		if !strings.Contains(err.Error(), "may only write its own ModuleNodeReport") {
			return err
		}
		return nil
	})
	own := &v1.ModuleNodeReport{ObjectMeta: metav1.ObjectMeta{Name: a}}
	if err := as(a).Create(ctx, own); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = k8s.Delete(ctx, own) })
	own.Status.Modules = []v1.HostModuleReport{{Module: "x-host", Family: "x", Ready: true}}
	if err := as(a).Status().Update(ctx, own); err != nil {
		t.Errorf("own status update: %v", err)
	}
	// Callers without the node-name claim (admins, the controller) are not restricted.
	admin := &v1.ModuleNodeReport{ObjectMeta: metav1.ObjectMeta{Name: b}}
	create(t, admin)
}
```

Also change the expectation in `TestAdmissionForgedAnnotation` (in `admission_test.go`). Its forged pod now also lacks the NRI annotations, and either policy may deny first:

```go
	}, "requires "+key, "modules.brics.ac.uk/containers must list the pod's containers")
```

Run `make test-envtest`. Expected: FAIL. The NRI annotations are missing and no report policy exists.

- [ ] **Step 2: values.yaml** — append:

```yaml
nri:
  image:
    repository: ghcr.io/bristol-supercomputing/module-nri
    tag: ""
    pullPolicy: IfNotPresent
  hookDir: /opt/brics-modules/bin
  pvcs: []
  discoveryInterval: 10m
  resources: {}
```

- [ ] **Step 3: admission-mutating.yaml** — under `mutations:`, after the existing entry, add a second one:

```yaml
  - patchType: ApplyConfiguration
    applyConfiguration:
      expression: >-
        !variables.applies ? Object{} :
        Object{
          metadata: Object.metadata{annotations: {
            'modules.brics.ac.uk/containers': object.spec.containers.map(c, c.name).join(','),
            'required-plugins.noderesource.dev/pod': '["modules"]'
          }}
        }
```

- [ ] **Step 4: admission-pods.yaml.** In every expression that tests whether a key is a module annotation (the two in `matchConditions` and the two in the UPDATE validation), extend the predicate. Change this:

```
k == 'modules.brics.ac.uk/load' || k.startsWith('resolved.modules.brics.ac.uk/') || k.startsWith('env.modules.brics.ac.uk/')
```

to this:

```
k == 'modules.brics.ac.uk/load' || k.startsWith('resolved.modules.brics.ac.uk/') || k.startsWith('env.modules.brics.ac.uk/') || k == 'modules.brics.ac.uk/containers' || k == 'required-plugins.noderesource.dev/pod'
```

Update the UPDATE message to list the two new keys. Add this variable:

```yaml
  - name: hasResolved
    expression: "variables.anns.exists(k, k.startsWith('resolved.modules.brics.ac.uk/'))"
```

Then append these validations after the existing ones:

```yaml
  - expression: >-
      request.operation != 'CREATE' || !variables.hasResolved ||
      ('modules.brics.ac.uk/containers' in variables.anns &&
       variables.anns['modules.brics.ac.uk/containers'] == object.spec.containers.map(c, c.name).join(','))
    message: "modules.brics.ac.uk/containers must list the pod's containers (the module admission policy sets it)"
  - expression: >-
      request.operation != 'CREATE' || !variables.hasResolved ||
      ('required-plugins.noderesource.dev/pod' in variables.anns &&
       variables.anns['required-plugins.noderesource.dev/pod'] == '["modules"]')
    message: "required-plugins.noderesource.dev/pod must be [\"modules\"] on pods that load modules"
```

- [ ] **Step 5: New templates**

<!-- file: deploy/helm/k8s-modules/templates/admission-module-reports.yaml -->
```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: module-reports.modules.brics.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
    - apiGroups: [modules.brics.ac.uk]
      apiVersions: [v1alpha1]
      operations: [CREATE, UPDATE]
      resources: [modulenodereports, modulenodereports/status]
  matchConditions:
  - name: node-bound-caller
    expression: "'authentication.kubernetes.io/node-name' in request.userInfo.extra"
  validations:
  - expression: "object.metadata.name == request.userInfo.extra['authentication.kubernetes.io/node-name'][0]"
    messageExpression: >-
      'a node may only write its own ModuleNodeReport (caller is on node ' +
      request.userInfo.extra['authentication.kubernetes.io/node-name'][0] + ')'
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: module-reports.modules.brics.ac.uk
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  policyName: module-reports.modules.brics.ac.uk
  validationActions: [Deny]
```

<!-- file: deploy/helm/k8s-modules/templates/nri-rbac.yaml -->
```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: module-nri
  namespace: {{ .Values.modulesNamespace }}
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: {{ .Release.Name }}-module-nri
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
rules:
- apiGroups: [modules.brics.ac.uk]
  resources: [modules]
  verbs: [get, list, watch]
- apiGroups: [modules.brics.ac.uk]
  resources: [modulenodereports, modulenodereports/status]
  verbs: [get, create, update, patch]
- apiGroups: [""]
  resources: [nodes]
  verbs: [get]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: {{ .Release.Name }}-module-nri
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: {{ .Release.Name }}-module-nri}
subjects:
- {kind: ServiceAccount, name: module-nri, namespace: {{ .Values.modulesNamespace }}}
```

<!-- file: deploy/helm/k8s-modules/templates/nri-daemonset.yaml -->
```yaml
{{- $image := printf "%s:%s" .Values.nri.image.repository (.Values.nri.image.tag | default .Chart.AppVersion) }}
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: module-nri
  namespace: {{ .Values.modulesNamespace }}
  labels: {{- include "k8s-modules.labels" . | nindent 4 }}
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: module-nri
      app.kubernetes.io/instance: {{ .Release.Name }}
  template:
    metadata:
      labels:
        app.kubernetes.io/name: module-nri
        app.kubernetes.io/instance: {{ .Release.Name }}
    spec:
      serviceAccountName: module-nri
      priorityClassName: system-node-critical
      tolerations:
      - operator: Exists
      initContainers:
      - name: install-hook
        image: {{ $image }}
        imagePullPolicy: {{ .Values.nri.image.pullPolicy }}
        args: [install-hook, --to={{ .Values.nri.hookDir }}]
        volumeMounts:
        - {name: hook-dir, mountPath: {{ .Values.nri.hookDir }}}
      containers:
      - name: module-nri
        image: {{ $image }}
        imagePullPolicy: {{ .Values.nri.image.pullPolicy }}
        args:
        - --node-name=$(NODE_NAME)
        - --host-root=/host
        - --hook-path={{ .Values.nri.hookDir }}/module-hook
        - --discovery-interval={{ .Values.nri.discoveryInterval }}
        {{- with .Values.nri.pvcs }}
        - --pvcs={{ join "," . }}
        {{- end }}
        env:
        - name: NODE_NAME
          valueFrom: {fieldRef: {fieldPath: spec.nodeName}}
        securityContext:
          privileged: true
          readOnlyRootFilesystem: true
        resources: {{- toYaml .Values.nri.resources | nindent 10 }}
        volumeMounts:
        - {name: nri-socket, mountPath: /var/run/nri}
        - {name: host-root, mountPath: /host, readOnly: true, mountPropagation: HostToContainer}
        {{- range $i, $claim := .Values.nri.pvcs }}
        - {name: pvc-{{ $i }}, mountPath: /pvcs/{{ $claim }}, readOnly: true}
        {{- end }}
      volumes:
      - {name: nri-socket, hostPath: {path: /var/run/nri, type: Directory}}
      - {name: host-root, hostPath: {path: /, type: Directory}}
      - {name: hook-dir, hostPath: {path: {{ .Values.nri.hookDir }}, type: DirectoryOrCreate}}
      {{- range $i, $claim := .Values.nri.pvcs }}
      - {name: pvc-{{ $i }}, persistentVolumeClaim: {claimName: {{ $claim }}, readOnly: true}}
      {{- end }}
```

In `templates/rbac.yaml`, append these to the controller's ClusterRole rules:

```yaml
- apiGroups: [modules.brics.ac.uk]
  resources: [modulenodereports]
  verbs: [get, list, watch]
- apiGroups: [""]
  resources: [nodes]
  verbs: [get, list, watch, patch]
```

<!-- file: deploy/helm/k8s-modules/README.md -->
```markdown
# k8s-modules

Installs the `Module` CRD, the module controller, the admission policies
that inject modules into pods, and the `module-nri` DaemonSet.

## Node requirements

- Kubernetes 1.36+ (MutatingAdmissionPolicy v1, image volumes).
- containerd 2.x with NRI enabled and its default validator switched on,
  so pods that load modules fail closed when `module-nri` is not running:

  ```toml
  [plugins."io.containerd.nri.v1.nri".default_validator]
    enable = true
  ```

  Without the validator, a module pod on a node without the plugin starts
  without its modules.

## Limits

- Pods loading `host` modules need a glibc-based image with `ldconfig` and
  a writable `/etc` (`readOnlyRootFilesystem: true` is not supported).
- Every claim used by a `pvc` module must be listed in `nri.pvcs` and exist
  in `modulesNamespace`.
- Pods that load modules carry `required-plugins.noderesource.dev/pod:
  '["modules"]'`, which cannot be combined with other required plugins.
```

- [ ] **Step 6: Run** `helm lint deploy/helm/k8s-modules && helm template t deploy/helm/k8s-modules -n module-system --set 'nri.pvcs={spack-apps}' | grep -A3 'claimName' && make test-envtest`. Expected: lint passes, the PVC volume renders, and envtest passes. Run the suite twice to catch flaky deny order.

- [ ] **Step 7: Commit** — `git add deploy/helm test/envtest && git commit -m "feat(chart): module-nri DaemonSet, report policy and NRI admission annotations"`

---

### Task 10: kind environment for NRI

**Files:**
- Create: `test/images/host-libs/Dockerfile`, `test/images/host-libs/src/build.sh`, `deploy/kind/pv-spack-apps.yaml`
- Modify: `deploy/kind/kind-config.yaml`, `deploy/kind/setup.sh`

**Interfaces:**
- Produces:
  - host libraries at `/usr/lib/brics-test` on every node, in the node's ld cache;
  - images `kind-registry:5000/module-nri:dev`, `kind-registry:5000/fakefabric-client:dev` and `kind-registry:5000/fakefabric-client-old:dev`;
  - the PVC `spack-apps` in `module-system`, which contains `lib/libpvc.so`;
  - the chart installed with `nri.pvcs={spack-apps}`.

- [ ] **Step 1: Images**

<!-- file: test/images/host-libs/src/build.sh -->
```sh
#!/bin/sh
# Builds the stub host libraries and the fakefabric-check client.
set -eu
H=/out/host/usr/lib/brics-test
C=/out/client
mkdir -p "$H" "$C/usr/local/bin" "$C/usr/lib" "$C/opt/masked"
gcc -shared -fPIC -DFAKEDEP_VARIANT='"host"' -Wl,-soname,libfakedep.so.1 -o "$H/libfakedep.so.1.0.0" /src/fakedep.c
ln -s libfakedep.so.1.0.0 "$H/libfakedep.so.1"
gcc -shared -fPIC -Wl,-soname,libfakefabric.so.1 -o "$H/libfakefabric.so.1.0.0" /src/fakefabric.c -L"$H" -l:libfakedep.so.1
ln -s libfakefabric.so.1.0.0 "$H/libfakefabric.so.1"
# The image's own libfakedep lives only in /usr/lib, a trusted directory not
# listed in ld.so.conf: the case module-hook's conf ordering must handle.
gcc -shared -fPIC -DFAKEDEP_VARIANT='"image"' -Wl,-soname,libfakedep.so.1 -o "$C/usr/lib/libfakedep.so.1" /src/fakedep.c
gcc -o "$C/usr/local/bin/fakefabric-check" /src/check.c -L"$H" -l:libfakefabric.so.1 -Wl,-rpath-link,"$H"
echo secret > "$C/opt/masked/secret"
```

<!-- file: test/images/host-libs/Dockerfile -->
```dockerfile
FROM docker.io/library/debian:bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev && rm -rf /var/lib/apt/lists/*
COPY src/ /src/
RUN sh /src/build.sh

FROM scratch AS host-libs
COPY --from=build /out/host/ /

FROM docker.io/library/debian:bookworm-slim AS client
COPY --from=build /out/client/ /
RUN ldconfig

FROM docker.io/library/debian:buster-slim AS client-old
COPY --from=build /out/client/ /
RUN ldconfig
```

<!-- file: deploy/kind/pv-spack-apps.yaml -->
```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: brics-test-spack-apps
spec:
  capacity: {storage: 1Gi}
  accessModes: [ReadOnlyMany]
  persistentVolumeReclaimPolicy: Retain
  storageClassName: ""
  hostPath: {path: /var/lib/brics-test/pvc, type: Directory}
  claimRef: {namespace: module-system, name: spack-apps}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: spack-apps
  namespace: module-system
spec:
  accessModes: [ReadOnlyMany]
  storageClassName: ""
  volumeName: brics-test-spack-apps
  resources: {requests: {storage: 1Gi}}
```

- [ ] **Step 2: kind-config.yaml** — add:

```yaml
containerdConfigPatches:
- |-
  [plugins."io.containerd.nri.v1.nri".default_validator]
    enable = true
```

- [ ] **Step 3: setup.sh changes**

**After the containerd `hosts.toml` loop in step 1**, add:

```bash
step "1b. stub host libraries on every node"
# shellcheck disable=SC2086
$CONTAINER_CLI build $BUILD_ARGS --target host-libs -t localhost/brics-host-libs:dev "$ROOT/test/images/host-libs"
LIBS=$(mktemp -d)
CID=$($CONTAINER_CLI create localhost/brics-host-libs:dev /none)
$CONTAINER_CLI cp "$CID:/usr/lib/brics-test" "$LIBS/"
$CONTAINER_CLI rm "$CID" >/dev/null
for node in $(kind get nodes --name "$CLUSTER"); do
  $CONTAINER_CLI exec "$node" rm -rf /usr/lib/brics-test
  $CONTAINER_CLI cp "$LIBS/brics-test" "$node:/usr/lib/brics-test"
  echo /usr/lib/brics-test | $CONTAINER_CLI exec -i "$node" tee /etc/ld.so.conf.d/brics-test.conf >/dev/null
  $CONTAINER_CLI exec "$node" ldconfig
  $CONTAINER_CLI exec "$node" sh -c 'mkdir -p /var/lib/brics-test/pvc/lib && echo stub > /var/lib/brics-test/pvc/lib/libpvc.so'
done
rm -rf "$LIBS"
```

**In step 2**, after the existing pushes, add:

```bash
# shellcheck disable=SC2086
$CONTAINER_CLI build $BUILD_ARGS -f "$ROOT/Dockerfile.nri" -t "localhost:${REG_PORT}/module-nri:dev" "$ROOT"
$CONTAINER_CLI push "${PUSH_FLAGS[@]}" "localhost:${REG_PORT}/module-nri:dev"
for target in client client-old; do
  # shellcheck disable=SC2086
  $CONTAINER_CLI build $BUILD_ARGS --target "$target" -t "localhost:${REG_PORT}/fakefabric-$target:dev" "$ROOT/test/images/host-libs"
  $CONTAINER_CLI push "${PUSH_FLAGS[@]}" "localhost:${REG_PORT}/fakefabric-$target:dev"
done
```

**Replace step 3's `helm upgrade` block** with:

```bash
kubectl create namespace module-system --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f "$ROOT/deploy/kind/pv-spack-apps.yaml"
helm upgrade --install k8s-modules "$ROOT/deploy/helm/k8s-modules" \
  --namespace module-system \
  --set image.repository="${REG_NAME}:5000/module-controller" \
  --set image.tag=dev --set image.pullPolicy=Always \
  --set nri.image.repository="${REG_NAME}:5000/module-nri" \
  --set nri.image.tag=dev --set nri.image.pullPolicy=Always \
  --set "nri.pvcs={spack-apps}" \
  --set "registryAllowlist={${REG_NAME}:5000/modules/}" \
  --wait --timeout 3m
kubectl -n module-system rollout restart deployment/module-controller daemonset/module-nri
kubectl -n module-system rollout status deployment/module-controller --timeout=2m
kubectl -n module-system rollout status daemonset/module-nri --timeout=3m
```

(The `fakefabric-$target` naming gives `fakefabric-client` and `fakefabric-client-old`.)

- [ ] **Step 4: Recreate the cluster and verify**

```bash
make kind-down && make kind-up
kubectl get modulenodereports
kubectl -n module-system logs ds/module-nri | head
podman exec k8s-modules-worker sh -c 'containerd config dump | grep -A1 "default_validator\]"'
```

Expected:
- `ready:` is printed;
- one report per node, with `DISCOVERED` set;
- the plugin log shows no errors;
- `enable = true`.

- [ ] **Step 5: Commit** — `git add deploy/kind test/images/host-libs && git commit -m "feat(dev): kind NRI environment with stub host libraries and a ROX PV"`

---

### Task 11: kind e2e for NRI

**Files:**
- Create: `test/e2e/nri_test.go`
- Modify: `test/e2e/e2e_test.go`. Extract a generic `runPodSpec` and add a log dump on failure.

**Interfaces:**
- Consumes: the Task 10 cluster and the phase-2 e2e helpers `newClient`, `namespace`, `waitFor`, `waitReason` and `expectRejected`.

- [ ] **Step 1: Refactor `runPod`** in `e2e_test.go` into:

```go
// runPodSpec creates p and waits for it to succeed, printing its logs on failure.
func runPodSpec(t *testing.T, c client.Client, p *corev1.Pod) {
	t.Helper()
	waitFor(t, 30*time.Second, func() error { return c.Create(ctx, p) })
	t.Cleanup(func() { _ = c.Delete(ctx, p) })
	waitFor(t, 3*time.Minute, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			return err
		}
		switch p.Status.Phase {
		case corev1.PodSucceeded:
			return nil
		case corev1.PodFailed:
			t.Fatalf("pod %s failed: %s", p.Name, podLogs(t, p))
		}
		return fmt.Errorf("pod %s phase %s %+v", p.Name, p.Status.Phase, p.Status.ContainerStatuses)
	})
}

func podLogs(t *testing.T, p *corev1.Pod) string {
	cs, err := kubernetes.NewForConfig(ctrl.GetConfigOrDie())
	if err != nil {
		return err.Error()
	}
	b, err := cs.CoreV1().Pods(p.Namespace).GetLogs(p.Name, &corev1.PodLogOptions{}).DoRaw(ctx)
	if err != nil {
		return err.Error()
	}
	return string(b)
}

func shellPod(ns, name, image, load, script string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Annotations: map[string]string{v1.AnnotationLoad: load}},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{
			Name: "check", Image: image, Command: []string{"sh", "-ec", script},
		}}},
	}
}
```

Make `runPod(t, c, ns, load, script)` call `runPodSpec(t, c, shellPod(ns, "check", "docker.io/library/busybox:1.37", load, script))`. Import `k8s.io/client-go/kubernetes`.

- [ ] **Step 2: nri_test.go**

<!-- file: test/e2e/nri_test.go -->
```go
//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

const (
	clientImage    = "kind-registry:5000/fakefabric-client:dev"
	clientOldImage = "kind-registry:5000/fakefabric-client-old:dev"
)

func createModule(t *testing.T, c client.Client, m *v1.Module) {
	t.Helper()
	_ = c.Delete(ctx, m.DeepCopy())
	waitFor(t, 30*time.Second, func() error { return c.Create(ctx, m.DeepCopy()) })
	t.Cleanup(func() { _ = c.Delete(ctx, m) })
	waitReason(t, c, m.Name, "Resolved")
}

func hostFabric(version string, prefer ...string) *v1.Module {
	return &v1.Module{ObjectMeta: metav1.ObjectMeta{Name: "fakefabric-" + version},
		Spec: v1.ModuleSpec{Family: "fakefabric", Version: version, Source: v1.ModuleSource{Host: &v1.HostSource{
			Discover: v1.HostDiscover{Roots: []string{"libfakefabric.so.1"}}, PreferHost: prefer}}}}
}

// waitContainerError waits until the pod's container fails to start with a
// message containing substr.
func waitContainerError(t *testing.T, c client.Client, p *corev1.Pod, substr string) {
	t.Helper()
	waitFor(t, 3*time.Minute, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			return err
		}
		for _, s := range p.Status.ContainerStatuses {
			for _, msg := range []string{waitingMsg(s.State), terminatedMsg(s.State), terminatedMsg(s.LastTerminationState)} {
				if strings.Contains(msg, substr) {
					return nil
				}
			}
		}
		return fmt.Errorf("pod %s: no container error containing %q yet: %+v", p.Name, substr, p.Status.ContainerStatuses)
	})
}

func waitingMsg(s corev1.ContainerState) string {
	if s.Waiting != nil {
		return s.Waiting.Message
	}
	return ""
}

func terminatedMsg(s corev1.ContainerState) string {
	if s.Terminated != nil {
		return s.Terminated.Message
	}
	return ""
}

func TestNRIImageModuleEnv(t *testing.T) {
	c := newClient(t)
	ns := namespace(t, c, "e2e-nri-env")
	runPodSpec(t, c, shellPod(ns, "env", "docker.io/library/busybox:1.37", "nccl-2.29.2", `
case "$LD_LIBRARY_PATH" in /opt/modules/nccl/2.29.2/lib*) ;; *) echo "LD_LIBRARY_PATH=$LD_LIBRARY_PATH"; exit 1;; esac
test "$BRICS_MODULES" = nccl-2.29.2`))
}

func TestNRIHostModule(t *testing.T) {
	c := newClient(t)
	createModule(t, c, hostFabric("host"))
	ns := namespace(t, c, "e2e-nri-host")
	runPodSpec(t, c, shellPod(ns, "host", clientImage, "fakefabric-host", `
test -f /opt/modules/fakefabric/host/lib/libfakefabric.so.1
test -f /opt/modules/fakefabric/host/deps/libfakedep.so.1
ldconfig -p | grep -q /opt/modules/fakefabric/host/deps/libfakedep.so.1
out=$(fakefabric-check); echo "$out"; test "$out" = fakedep=image`))
}

func TestNRIPreferHost(t *testing.T) {
	c := newClient(t)
	createModule(t, c, hostFabric("prefer", "libfakedep.so.1"))
	ns := namespace(t, c, "e2e-nri-prefer")
	runPodSpec(t, c, shellPod(ns, "prefer", clientImage, "fakefabric-prefer", `
out=$(fakefabric-check); echo "$out"; test "$out" = fakedep=host`))
}

func TestNRIGlibcTooOld(t *testing.T) {
	c := newClient(t)
	createModule(t, c, hostFabric("host"))
	ns := namespace(t, c, "e2e-nri-glibc")
	p := shellPod(ns, "old", clientOldImage, "fakefabric-host", "fakefabric-check")
	waitFor(t, 30*time.Second, func() error { return c.Create(ctx, p) })
	t.Cleanup(func() { _ = c.Delete(ctx, p) })
	waitContainerError(t, c, p, "module fakefabric-host needs GLIBC_2.36")
}

func TestNRIMaskPaths(t *testing.T) {
	c := newClient(t)
	createModule(t, c, &v1.Module{ObjectMeta: metav1.ObjectMeta{Name: "e2e-mask-1"},
		Spec: v1.ModuleSpec{Family: "e2e-mask", Version: "1", MaskPaths: []string{"/opt/masked"},
			Source: v1.ModuleSource{Image: &v1.ImageSource{Reference: ncclImage(t, c)}}}})
	ns := namespace(t, c, "e2e-nri-mask")
	runPodSpec(t, c, shellPod(ns, "mask", clientImage, "e2e-mask-1", `test ! -e /opt/masked/secret`))
}

func TestNRIPVCModule(t *testing.T) {
	c := newClient(t)
	createModule(t, c, &v1.Module{ObjectMeta: metav1.ObjectMeta{Name: "e2e-pvc-1"},
		Spec: v1.ModuleSpec{Family: "e2e-pvc", Version: "1",
			Source: v1.ModuleSource{PVC: &v1.PVCSource{ClaimName: "spack-apps", SubPath: "."}}}})
	ns := namespace(t, c, "e2e-nri-pvc")
	runPodSpec(t, c, shellPod(ns, "pvc", "docker.io/library/busybox:1.37", "e2e-pvc-1", `
test -f /opt/modules/e2e-pvc/1/lib/libpvc.so
! touch /opt/modules/e2e-pvc/1/x 2>/dev/null`))
}

func TestNRIFailClosed(t *testing.T) {
	c := newClient(t)
	ds := types.NamespacedName{Namespace: "module-system", Name: "module-nri"}
	setSelector := func(sel map[string]string) {
		patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"nodeSelector": sel}}}})
		if err := c.Patch(ctx, &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Namespace: ds.Namespace, Name: ds.Name}}, client.RawPatch(types.StrategicMergePatchType, patch)); err != nil {
			t.Fatal(err)
		}
	}
	dsReady := func(want int32) func() error {
		return func() error {
			d := &appsv1.DaemonSet{}
			if err := c.Get(ctx, ds, d); err != nil {
				return err
			}
			if d.Status.NumberReady != want || d.Status.DesiredNumberScheduled != want || d.Status.UpdatedNumberScheduled != want {
				return fmt.Errorf("daemonset ready=%d desired=%d, want %d", d.Status.NumberReady, d.Status.DesiredNumberScheduled, want)
			}
			return nil
		}
	}
	nodes := &corev1.NodeList{}
	if err := c.List(ctx, nodes); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		setSelector(nil)
		waitFor(t, 3*time.Minute, dsReady(int32(len(nodes.Items))))
	})
	setSelector(map[string]string{"brics-test/nowhere": "true"})
	waitFor(t, 2*time.Minute, func() error {
		pods := &corev1.PodList{}
		if err := c.List(ctx, pods, client.InNamespace(ds.Namespace), client.MatchingLabels{"app.kubernetes.io/name": "module-nri"}); err != nil {
			return err
		}
		if len(pods.Items) != 0 {
			return fmt.Errorf("%d module-nri pods still present", len(pods.Items))
		}
		return nil
	})

	ns := namespace(t, c, "e2e-nri-closed")
	p := shellPod(ns, "needs-plugin", "docker.io/library/busybox:1.37", "nccl-2.29.2", "true")
	waitFor(t, 30*time.Second, func() error { return c.Create(ctx, p) })
	t.Cleanup(func() { _ = c.Delete(ctx, p) })
	waitContainerError(t, c, p, `required plugin "modules" not present`)

	plain := shellPod(ns, "plain", "docker.io/library/busybox:1.37", "", "true")
	delete(plain.Annotations, v1.AnnotationLoad)
	runPodSpec(t, c, plain)
}

func TestNRINodeReportAndLabels(t *testing.T) {
	c := newClient(t)
	createModule(t, c, hostFabric("host"))
	nodes := &corev1.NodeList{}
	if err := c.List(ctx, nodes); err != nil {
		t.Fatal(err)
	}
	var hash string
	for _, n := range nodes.Items {
		waitFor(t, 2*time.Minute, func() error {
			r := &v1.ModuleNodeReport{}
			if err := c.Get(ctx, client.ObjectKey{Name: n.Name}, r); err != nil {
				return err
			}
			for _, m := range r.Status.Modules {
				if m.Module != "fakefabric-host" {
					continue
				}
				if !m.Ready {
					return fmt.Errorf("node %s: not ready: %s", n.Name, m.Message)
				}
				if hash == "" {
					hash = m.ClosureHash
				}
				if m.ClosureHash != hash {
					return fmt.Errorf("node %s hash %s differs from %s", n.Name, m.ClosureHash, hash)
				}
				return nil
			}
			return fmt.Errorf("node %s: fakefabric-host not reported", n.Name)
		})
		waitFor(t, time.Minute, func() error {
			node := &corev1.Node{}
			if err := c.Get(ctx, client.ObjectKey{Name: n.Name}, node); err != nil {
				return err
			}
			if node.Labels[v1.LabelHostPrefix+"fakefabric"] != "true" {
				return fmt.Errorf("node %s lacks %sfakefabric", n.Name, v1.LabelHostPrefix)
			}
			return nil
		})
	}
}
```

- [ ] **Step 3: Run** `make test-e2e`. Expected: every phase-2 and NRI test passes.

When a test fails, read the container error first, then the plugin logs: `kubectl -n module-system logs ds/module-nri`. Fix the cause (superpowers:systematic-debugging) rather than the test.

- [ ] **Step 4: Full verification** — `make build test test-envtest helm-lint test-e2e`. Expected: all green.

- [ ] **Step 5: Commit** — `git add test/e2e && git commit -m "test(e2e): NRI env, host module, preferHost, glibc, mask, pvc, fail-closed and report scenarios"`
