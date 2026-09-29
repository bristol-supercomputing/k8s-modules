package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
)

func TestDesiredDeviceClasses(t *testing.T) {
	resolved := v1.Module{ObjectMeta: metav1.ObjectMeta{Name: "nccl-2.29.2"},
		Spec: v1.ModuleSpec{Family: "nccl", Version: "2.29.2"}, Status: v1.ModuleStatus{Resolved: &v1.Resolved{}}}
	pending := v1.Module{ObjectMeta: metav1.ObjectMeta{Name: "ofi-1"}, Spec: v1.ModuleSpec{Family: "ofi", Version: "1"}}
	got := DesiredDeviceClasses([]v1.Module{pending, resolved})

	want := map[string][2]string{
		"nccl-2.29.2.modules.isambard.ac.uk": {"modules.isambard.ac.uk/nccl-2.29.2",
			`device.driver == "modules.isambard.ac.uk" && device.attributes["modules.isambard.ac.uk"].module == "nccl-2.29.2"`},
		"nccl.family.modules.isambard.ac.uk": {"family.modules.isambard.ac.uk/nccl",
			`device.driver == "modules.isambard.ac.uk" && device.attributes["modules.isambard.ac.uk"].family == "nccl" && device.attributes["modules.isambard.ac.uk"].default == true`},
		"ofi.family.modules.isambard.ac.uk": {"family.modules.isambard.ac.uk/ofi",
			`device.driver == "modules.isambard.ac.uk" && device.attributes["modules.isambard.ac.uk"].family == "ofi" && device.attributes["modules.isambard.ac.uk"].default == true`},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d classes, want %d: %+v", len(got), len(want), got)
	}
	for i, dc := range got {
		w, ok := want[dc.Name]
		if !ok {
			t.Errorf("unexpected class %s", dc.Name)
			continue
		}
		if i > 0 && got[i-1].Name >= dc.Name {
			t.Error("classes must be sorted by name")
		}
		if dc.Labels[v1.LabelManaged] != "true" || dc.Spec.ExtendedResourceName == nil || *dc.Spec.ExtendedResourceName != w[0] ||
			len(dc.Spec.Selectors) != 1 || dc.Spec.Selectors[0].CEL == nil || dc.Spec.Selectors[0].CEL.Expression != w[1] {
			t.Errorf("class %s = %+v", dc.Name, dc)
		}
	}
}
