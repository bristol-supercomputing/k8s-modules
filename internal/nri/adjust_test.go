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
	p := podFor(c, "main", "x-1")
	x.Status.Resolved = nil // the Module became unresolved after admission
	if _, err := a.Adjust(p, ctr("main")); err == nil || !strings.Contains(err.Error(), "does not match the Module catalogue") {
		t.Errorf("unresolved module: err = %v", err)
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
