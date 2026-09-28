package resolve

import (
	"errors"
	"reflect"
	"testing"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
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
		Volumes: []v1.ResolvedVolume{{Name: VolumeName("nccl-2.29.2"), Reference: m.Spec.Source.Image.Reference}},
		Mounts:  []v1.ResolvedMount{{Name: VolumeName("nccl-2.29.2"), MountPath: "/opt/modules/nccl/2.29.2"}},
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
