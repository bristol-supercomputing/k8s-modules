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

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
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
