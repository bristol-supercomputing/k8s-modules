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
