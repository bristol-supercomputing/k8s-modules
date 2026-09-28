package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

func TestSetStatus(t *testing.T) {
	cases := []struct {
		name                           string
		err                            error
		resolved, conflict             metav1.ConditionStatus
		resolvedReason, conflictReason string
	}{
		{"ok", nil, metav1.ConditionTrue, metav1.ConditionTrue, "Resolved", "NoConflicts"},
		{"missing", resolve.ErrDependencyNotFound{Module: "a", Missing: "b"}, metav1.ConditionFalse, metav1.ConditionUnknown, "DependencyNotFound", "NotEvaluated"},
		{"cycle", resolve.ErrDependencyCycle{Path: []string{"a", "a"}}, metav1.ConditionFalse, metav1.ConditionUnknown, "DependencyCycle", "NotEvaluated"},
		{"family", resolve.ErrFamilyConflict{Family: "f", Modules: []string{"f-1", "f-2"}}, metav1.ConditionFalse, metav1.ConditionFalse, "Conflict", "FamilyConflict"},
		{"env", resolve.ErrEnvConflict{Name: "N", Modules: []string{"a", "b"}, Values: []string{"1", "2"}}, metav1.ConditionFalse, metav1.ConditionFalse, "Conflict", "EnvConflict"},
		{"pvc", ErrPVCInvalid{Module: "a", Namespace: "ns", Claim: "c", Problem: "does not exist"}, metav1.ConditionFalse, metav1.ConditionUnknown, "PVCInvalid", "NotEvaluated"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := v1.ModuleStatus{Resolved: &v1.Resolved{Modules: []string{"stale"}}}
			var res *v1.Resolved
			if c.err == nil {
				res = &v1.Resolved{Modules: []string{"a"}}
			}
			setStatus(&st, 7, res, c.err)
			r := meta.FindStatusCondition(st.Conditions, v1.ConditionResolved)
			cf := meta.FindStatusCondition(st.Conditions, v1.ConditionConflictFree)
			if r.Status != c.resolved || r.Reason != c.resolvedReason || cf.Status != c.conflict || cf.Reason != c.conflictReason {
				t.Errorf("got Resolved=%s/%s ConflictFree=%s/%s", r.Status, r.Reason, cf.Status, cf.Reason)
			}
			if r.ObservedGeneration != 7 || cf.ObservedGeneration != 7 {
				t.Error("observedGeneration not set")
			}
			if c.err != nil && (st.Resolved != nil || r.Message != c.err.Error()) {
				t.Errorf("failure must clear resolved and carry the error message; got %+v %q", st.Resolved, r.Message)
			}
			if c.err == nil && st.Resolved != res {
				t.Error("success must set resolved")
			}
		})
	}
}
