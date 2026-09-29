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
