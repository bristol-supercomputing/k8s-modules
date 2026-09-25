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
