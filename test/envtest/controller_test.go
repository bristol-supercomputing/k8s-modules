package integration

import (
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
)

func TestControllerResolvesChain(t *testing.T) {
	a, b, c := newFamily("chain-a"), newFamily("chain-b"), newFamily("chain-c")
	create(t, imageModule(c, "1"))
	create(t, imageModule(b, "1", withDeps(c+"-1")))
	create(t, imageModule(a, "1", withDeps(b+"-1")))
	m := waitResolved(t, a+"-1")
	if want := []string{c + "-1", b + "-1", a + "-1"}; !reflect.DeepEqual(m.Status.Resolved.Modules, want) {
		t.Errorf("modules = %v, want %v", m.Status.Resolved.Modules, want)
	}
	if v := m.Status.Resolved.Annotations[v1.AnnotationResolvedPrefix+a]; !strings.Contains(v, ";rank=30;") {
		t.Errorf("annotation %q, want rank=30", v)
	}
	waitCondition(t, a+"-1", v1.ConditionConflictFree, metav1.ConditionTrue, "NoConflicts")
}

func TestControllerLateDependency(t *testing.T) {
	a, dep := newFamily("late"), newFamily("late-dep")
	create(t, imageModule(a, "1", withDeps(dep+"-1")))
	m := waitCondition(t, a+"-1", v1.ConditionResolved, metav1.ConditionFalse, "DependencyNotFound")
	if m.Status.Resolved != nil {
		t.Error("unresolved module must have no status.resolved")
	}
	waitCondition(t, a+"-1", v1.ConditionConflictFree, metav1.ConditionUnknown, "NotEvaluated")
	create(t, imageModule(dep, "1"))
	waitResolved(t, a+"-1")
}

func TestControllerDeletedDependency(t *testing.T) {
	a, dep := newFamily("del"), newFamily("del-dep")
	d := imageModule(dep, "1")
	create(t, d)
	create(t, imageModule(a, "1", withDeps(dep+"-1")))
	waitResolved(t, a+"-1")
	if err := k8s.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}
	waitCondition(t, a+"-1", v1.ConditionResolved, metav1.ConditionFalse, "DependencyNotFound")
}

func TestControllerCycle(t *testing.T) {
	a, b := newFamily("cyc-a"), newFamily("cyc-b")
	create(t, imageModule(a, "1", withDeps(b+"-1")))
	create(t, imageModule(b, "1", withDeps(a+"-1")))
	m := waitCondition(t, a+"-1", v1.ConditionResolved, metav1.ConditionFalse, "DependencyCycle")
	c := findCond(m, v1.ConditionResolved)
	if !strings.Contains(c.Message, "dependency cycle") {
		t.Errorf("message %q", c.Message)
	}
}

func TestControllerConflicts(t *testing.T) {
	fam := newFamily("cf")
	create(t, imageModule(fam, "1"))
	create(t, imageModule(fam, "2"))
	both := newFamily("cf-both")
	create(t, imageModule(both, "1", withDeps(fam+"-1", fam+"-2")))
	waitCondition(t, both+"-1", v1.ConditionResolved, metav1.ConditionFalse, "Conflict")
	m := waitCondition(t, both+"-1", v1.ConditionConflictFree, metav1.ConditionFalse, "FamilyConflict")
	if msg := findCond(m, v1.ConditionConflictFree).Message; !strings.Contains(msg, fam) {
		t.Errorf("message %q does not name family %s", msg, fam)
	}

	p, q, top := newFamily("ec-p"), newFamily("ec-q"), newFamily("ec-top")
	create(t, imageModule(p, "1", withEnv("NCCL_ALGO", "ring")))
	create(t, imageModule(q, "1", withEnv("NCCL_ALGO", "tree")))
	create(t, imageModule(top, "1", withDeps(p+"-1", q+"-1")))
	m = waitCondition(t, top+"-1", v1.ConditionConflictFree, metav1.ConditionFalse, "EnvConflict")
	if msg := findCond(m, v1.ConditionConflictFree).Message; !strings.Contains(msg, "NCCL_ALGO") {
		t.Errorf("message %q does not name the variable", msg)
	}
}

func TestControllerPVC(t *testing.T) {
	missing, rwo, rox, dependent := newFamily("pvc-missing"), newFamily("pvc-rwo"), newFamily("pvc-rox"), newFamily("pvc-dep")
	pvcModule := func(fam, claim string) *v1.Module {
		m := imageModule(fam, "1")
		m.Spec.Source = v1.ModuleSource{PVC: &v1.PVCSource{ClaimName: claim, SubPath: "x"}}
		return m
	}
	pvc := func(name string, mode corev1.PersistentVolumeAccessMode) *corev1.PersistentVolumeClaim {
		return &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: modulesNamespace},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{mode},
				Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")}},
			},
		}
	}

	create(t, pvcModule(missing, missing))
	m := waitCondition(t, missing+"-1", v1.ConditionResolved, metav1.ConditionFalse, "PVCInvalid")
	if msg := findCond(m, v1.ConditionResolved).Message; !strings.Contains(msg, "does not exist") {
		t.Errorf("message %q", msg)
	}

	create(t, pvc(rwo, corev1.ReadWriteOnce))
	create(t, pvcModule(rwo, rwo))
	m = waitCondition(t, rwo+"-1", v1.ConditionResolved, metav1.ConditionFalse, "PVCInvalid")
	if msg := findCond(m, v1.ConditionResolved).Message; !strings.Contains(msg, "ReadOnlyMany") {
		t.Errorf("message %q", msg)
	}

	create(t, pvc(rox, corev1.ReadOnlyMany))
	create(t, pvcModule(rox, rox))
	waitResolved(t, rox+"-1")

	// A dependent of a pvc module is checked too, and recovers when the PVC appears.
	create(t, imageModule(dependent, "1", withDeps(missing+"-1")))
	waitCondition(t, dependent+"-1", v1.ConditionResolved, metav1.ConditionFalse, "PVCInvalid")
	create(t, pvc(missing, corev1.ReadOnlyMany))
	waitResolved(t, dependent+"-1")
	waitResolved(t, missing+"-1")
}

func findCond(m *v1.Module, typ string) *metav1.Condition {
	for i := range m.Status.Conditions {
		if m.Status.Conditions[i].Type == typ {
			return &m.Status.Conditions[i]
		}
	}
	return &metav1.Condition{}
}
