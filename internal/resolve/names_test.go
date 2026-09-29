package resolve

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
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
		// Sanitising is lossy, so a changed name carries a hash of the original.
		{"nccl-2.29.2", "mod-nccl-2-29-2-a6b4217c"},
		{"libfabric-host", "mod-libfabric-host"},
		{"a-1.0_rc", "mod-a-1-0-rc-3c5ac3c1"},
	}
	for _, c := range cases {
		if got := VolumeName(c.in); got != c.want {
			t.Errorf("VolumeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Distinct Modules must never share a volume name: family nccl version
	// 2.29.2, family nccl-2 version 29.2, and family nccl-2-29 version 2.
	seen := map[string]string{}
	for _, n := range []string{"nccl-2.29.2", "nccl-2-29.2", "nccl-2-29-2"} {
		v := VolumeName(n)
		if prev, ok := seen[v]; ok {
			t.Errorf("VolumeName(%q) == VolumeName(%q) == %q", n, prev, v)
		}
		seen[v] = n
		if errs := validation.IsDNS1123Label(v); len(errs) > 0 {
			t.Errorf("%q is not a DNS label: %v", v, errs)
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
	if got := ResolvedAnnotationKey("nccl"); got != "resolved.modules.isambard.ac.uk/nccl" {
		t.Errorf("got %q", got)
	}
	if got := EnvAnnotationKey("NCCL_NET"); got != "env.modules.isambard.ac.uk/NCCL_NET" {
		t.Errorf("got %q", got)
	}
}
