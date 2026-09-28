package nri

import (
	"reflect"
	"testing"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	"github.com/bristol-supercomputing/k8s-modules/internal/discovery"
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
