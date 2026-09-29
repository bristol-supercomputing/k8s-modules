package nri

import (
	"reflect"
	"strings"
	"testing"

	api "github.com/containerd/nri/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/discovery"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

// mod, img and hst are fixtures shared with report_test.go.
func mod(family, version string, src v1.ModuleSource, deps ...string) *v1.Module {
	return &v1.Module{ObjectMeta: metav1.ObjectMeta{Name: family + "-" + version},
		Spec: v1.ModuleSpec{Family: family, Version: version, Source: src, DependsOn: deps}}
}

var (
	img = v1.ModuleSource{Image: &v1.ImageSource{Reference: "r/x@sha256:" + strings.Repeat("a", 64)}}
	hst = v1.ModuleSource{Host: &v1.HostSource{Discover: v1.HostDiscover{Roots: []string{"libfab.so.1"}}}}
)

func hostResult() discovery.Result {
	return discovery.Result{Ready: true, GlibcRequired: "2.36",
		Lib:  []discovery.Library{{Soname: "libfab.so.1", Path: "/usr/lib64/libfab.so.1.2"}},
		Deps: []discovery.Library{{Soname: "libdep.so.1", Path: "/usr/lib64/libdep.so.1.0"}}}
}

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
