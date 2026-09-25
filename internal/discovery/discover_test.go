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
