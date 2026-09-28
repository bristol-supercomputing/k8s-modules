package controller

import (
	"reflect"
	"testing"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

func TestHostLabels(t *testing.T) {
	rep := &v1.ModuleNodeReport{Status: v1.ModuleNodeReportStatus{Modules: []v1.HostModuleReport{
		{Module: "fab-host", Family: "fab", Ready: true},
		{Module: "cxi-host", Family: "cxi", Ready: false},
	}}}
	cur := map[string]string{"keep": "me", v1.LabelHostPrefix + "stale": "true", v1.LabelHostPrefix + "cxi": "true"}
	got, changed := HostLabels(cur, rep)
	want := map[string]string{"keep": "me", v1.LabelHostPrefix + "fab": "true"}
	if !changed || !reflect.DeepEqual(got, want) {
		t.Errorf("got %v changed=%v, want %v", got, changed, want)
	}
	if _, changed := HostLabels(want, rep); changed {
		t.Error("already correct labels must report no change")
	}
	got, changed = HostLabels(want, nil)
	if !changed || !reflect.DeepEqual(got, map[string]string{"keep": "me"}) {
		t.Errorf("no report must remove every host label: %v", got)
	}
	if cur[v1.LabelHostPrefix+"stale"] != "true" {
		t.Error("HostLabels must not mutate its input")
	}
}
