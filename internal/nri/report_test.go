package nri

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/discovery"
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

func TestDiscovererCallsOnUpdate(t *testing.T) {
	scheme := kruntime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}).
		WithStatusSubresource(&v1.ModuleNodeReport{}).Build()
	// An empty host root has no ld.so.cache; discovery then records that
	// per module and still updates State.
	d := &Discoverer{HostRoot: t.TempDir(), Arch: "arm64", NodeName: "n1",
		Reader: c, Writer: c, State: &HostState{}}
	calls := 0
	d.OnUpdate = func() { calls++ }
	if err := d.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("OnUpdate called %d times, want 1", calls)
	}
}
