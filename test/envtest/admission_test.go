package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

var nsSeq int

func newNamespace(t *testing.T, labels map[string]string) string {
	t.Helper()
	nsSeq++
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("adm-%d", nsSeq), Labels: labels}}
	create(t, ns)
	return ns.Name
}

func newPod(ns, load string, containers ...corev1.Container) *corev1.Pod {
	if len(containers) == 0 {
		containers = []corev1.Container{{Name: "main", Image: "busybox"}}
	}
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "p-", Namespace: ns, Annotations: map[string]string{}},
		Spec:       corev1.PodSpec{Containers: containers},
	}
	if load != "" {
		p.Annotations[v1.AnnotationLoad] = load
	}
	return p
}

// admit creates the pod, retrying briefly while admission's param informer
// catches up with Modules the test just created, until ready(pod) holds.
func admit(t *testing.T, build func() *corev1.Pod, ready func(*corev1.Pod) error) *corev1.Pod {
	t.Helper()
	var p *corev1.Pod
	eventually(t, 10*time.Second, func() error {
		p = build()
		if err := k8s.Create(ctx, p); err != nil {
			return err
		}
		if err := ready(p); err != nil {
			_ = k8s.Delete(ctx, p)
			return err
		}
		return nil
	})
	t.Cleanup(func() { _ = k8s.Delete(ctx, p) })
	return p
}

// reject expects admission to refuse the pod with a message containing any of substrs.
func reject(t *testing.T, build func() *corev1.Pod, substrs ...string) {
	t.Helper()
	eventually(t, 10*time.Second, func() error {
		p := build()
		err := k8s.Create(ctx, p)
		if err == nil {
			_ = k8s.Delete(ctx, p)
			return fmt.Errorf("pod was admitted with annotations %v", p.Annotations)
		}
		for _, s := range substrs {
			if strings.Contains(err.Error(), s) {
				return nil
			}
		}
		return fmt.Errorf("error %q contains none of %q", err, substrs)
	})
}

func hasAnnotations(want map[string]string) func(*corev1.Pod) error {
	return func(p *corev1.Pod) error {
		for k, v := range want {
			if p.Annotations[k] != v {
				return fmt.Errorf("annotation %s = %q, want %q", k, p.Annotations[k], v)
			}
		}
		return nil
	}
}

func envValue(c corev1.Container, name string) (string, bool) {
	for _, e := range c.Env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

func TestAdmissionSingleLoad(t *testing.T) {
	fam := newFamily("single")
	m := imageModule(fam, "2.29.2", withLib("lib"), withEnv("NCCL_DEBUG", "INFO"))
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)

	p := admit(t, func() *corev1.Pod {
		return newPod(ns, m.Name, corev1.Container{Name: "a", Image: "busybox"}, corev1.Container{Name: "b", Image: "busybox"})
	}, hasAnnotations(m.Status.Resolved.Annotations))

	vn := resolve.VolumeName(m.Name)
	if len(p.Spec.Volumes) != 1 || p.Spec.Volumes[0].Name != vn || p.Spec.Volumes[0].Image == nil ||
		p.Spec.Volumes[0].Image.Reference != m.Spec.Source.Image.Reference ||
		p.Spec.Volumes[0].Image.PullPolicy != corev1.PullIfNotPresent {
		t.Errorf("volumes = %+v", p.Spec.Volumes)
	}
	for _, c := range p.Spec.Containers {
		if len(c.VolumeMounts) != 1 || c.VolumeMounts[0].Name != vn || !c.VolumeMounts[0].ReadOnly ||
			c.VolumeMounts[0].MountPath != "/opt/modules/"+fam+"/2.29.2" {
			t.Errorf("container %s mounts = %+v", c.Name, c.VolumeMounts)
		}
		if v, _ := envValue(c, "NCCL_DEBUG"); v != "INFO" {
			t.Errorf("container %s NCCL_DEBUG = %q", c.Name, v)
		}
	}
	want := resolve.AnnotationValue(m, 10)
	if got := p.Annotations[v1.AnnotationResolvedPrefix+fam]; got != want {
		t.Errorf("annotation %q, want %q", got, want)
	}
}

func TestAdmissionDiamond(t *testing.T) {
	a, b, c, d := newFamily("dia-a"), newFamily("dia-b"), newFamily("dia-c"), newFamily("dia-d")
	create(t, imageModule(d, "1", withEnv("NCCL_PROTO", "simple")))
	create(t, imageModule(b, "1", withDeps(d+"-1")))
	create(t, imageModule(c, "1", withDeps(d+"-1")))
	create(t, imageModule(a, "1", withDeps(b+"-1", c+"-1")))
	top := waitResolved(t, a+"-1")
	waitResolved(t, b+"-1")
	waitResolved(t, c+"-1")
	ns := newNamespace(t, nil)

	p := admit(t, func() *corev1.Pod { return newPod(ns, a+"-1") }, hasAnnotations(top.Status.Resolved.Annotations))
	if len(p.Spec.Volumes) != 4 {
		t.Errorf("want 4 volumes, got %+v", p.Spec.Volumes)
	}
	// Requesting both sides separately merges the shared dependency.
	p = admit(t, func() *corev1.Pod { return newPod(ns, b+"-1,"+c+"-1") }, func(p *corev1.Pod) error {
		if len(p.Spec.Volumes) != 3 {
			return fmt.Errorf("want 3 volumes, got %d", len(p.Spec.Volumes))
		}
		return nil
	})
	if n := len(p.Spec.Containers[0].Env); n != 1 {
		t.Errorf("shared env must appear once, got %+v", p.Spec.Containers[0].Env)
	}
}

func TestAdmissionUserEnvWins(t *testing.T) {
	fam := newFamily("userenv")
	m := imageModule(fam, "1", withEnv("NCCL_DEBUG", "INFO"))
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	p := admit(t, func() *corev1.Pod {
		return newPod(ns, m.Name, corev1.Container{Name: "a", Image: "busybox", Env: []corev1.EnvVar{{Name: "NCCL_DEBUG", Value: "TRACE"}}})
	}, hasAnnotations(m.Status.Resolved.Annotations))
	if v, _ := envValue(p.Spec.Containers[0], "NCCL_DEBUG"); v != "TRACE" || len(p.Spec.Containers[0].Env) != 1 {
		t.Errorf("env = %+v, want only the user's NCCL_DEBUG=TRACE", p.Spec.Containers[0].Env)
	}
}

func TestAdmissionFamilyConflictAcrossRequests(t *testing.T) {
	nccl, p, q := newFamily("fc-nccl"), newFamily("fc-p"), newFamily("fc-q")
	create(t, imageModule(nccl, "1"))
	create(t, imageModule(nccl, "2"))
	create(t, imageModule(p, "1", withDeps(nccl+"-1")))
	create(t, imageModule(q, "1", withDeps(nccl+"-2")))
	for _, n := range []string{p + "-1", q + "-1", nccl + "-1", nccl + "-2"} {
		waitResolved(t, n)
	}
	ns := newNamespace(t, nil)
	reject(t, func() *corev1.Pod { return newPod(ns, p+"-1,"+q+"-1") }, "requires resolved.modules.isambard.ac.uk/"+nccl)
	// The losing version is also "not loaded"; either policy may report first.
	reject(t, func() *corev1.Pod { return newPod(ns, nccl+"-1,"+nccl+"-2") },
		"requires resolved.modules.isambard.ac.uk/"+nccl, "or in conflict with another loaded module")
}

func TestAdmissionEnvConflictAcrossRequests(t *testing.T) {
	p, q := newFamily("envc-p"), newFamily("envc-q")
	create(t, imageModule(p, "1", withEnv("NCCL_ALGO", "ring")))
	create(t, imageModule(q, "1", withEnv("NCCL_ALGO", "tree")))
	waitResolved(t, p+"-1")
	waitResolved(t, q+"-1")
	ns := newNamespace(t, nil)
	reject(t, func() *corev1.Pod { return newPod(ns, p+"-1,"+q+"-1") }, "requires env.modules.isambard.ac.uk/NCCL_ALGO")
}

func TestAdmissionForgedAnnotation(t *testing.T) {
	fam := newFamily("forge")
	m := imageModule(fam, "1")
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	key := v1.AnnotationResolvedPrefix + fam
	forged := m.Name + ";rank=10;src=image;mount=/etc"

	reject(t, func() *corev1.Pod {
		p := newPod(ns, "")
		p.Annotations[key] = forged
		return p
	}, "requires "+key, "modules.isambard.ac.uk/containers must list the pod's containers")

	// Loading the module overwrites the forged value with the real one.
	admit(t, func() *corev1.Pod {
		p := newPod(ns, m.Name)
		p.Annotations[key] = forged
		return p
	}, hasAnnotations(m.Status.Resolved.Annotations))
}

func TestAdmissionAccess(t *testing.T) {
	fam := newFamily("acl")
	m := imageModule(fam, "1", withAccess(map[string]string{"team": "hpc"}))
	create(t, m)
	m = waitResolved(t, m.Name)
	denied := newNamespace(t, map[string]string{"team": "other"})
	allowed := newNamespace(t, map[string]string{"team": "hpc"})
	reject(t, func() *corev1.Pod { return newPod(denied, m.Name) }, "not permitted in namespace \""+denied+"\"")
	admit(t, func() *corev1.Pod { return newPod(allowed, m.Name) }, hasAnnotations(m.Status.Resolved.Annotations))

	// Access is enforced through dependencies too.
	top := newFamily("acl-top")
	create(t, imageModule(top, "1", withDeps(m.Name)))
	waitResolved(t, top+"-1")
	reject(t, func() *corev1.Pod { return newPod(denied, top+"-1") }, "not permitted in namespace \""+denied+"\"")
}

func TestAdmissionUnknownAndInvalid(t *testing.T) {
	ns := newNamespace(t, nil)
	reject(t, func() *corev1.Pod { return newPod(ns, "doesnotexist-1.0") }, `module "doesnotexist-1.0" was not loaded`)
	reject(t, func() *corev1.Pod { return newPod(ns, "Bad_Name") }, `invalid module name "Bad_Name"`)
	many := make([]string, 33)
	for i := range many {
		many[i] = fmt.Sprintf("m%d", i)
	}
	reject(t, func() *corev1.Pod { return newPod(ns, strings.Join(many, ",")) }, "more than 32 modules")
}

func TestAdmissionUnresolved(t *testing.T) {
	fam := newFamily("unres")
	create(t, imageModule(fam, "1", withDeps("missing-9.9")))
	waitCondition(t, fam+"-1", v1.ConditionResolved, metav1.ConditionFalse, "DependencyNotFound")
	ns := newNamespace(t, nil)
	// Either policy may report first (see plan deviation 2).
	reject(t, func() *corev1.Pod { return newPod(ns, fam+"-1") },
		`is not resolved: dependency "missing-9.9"`, `module "`+fam+`-1" was not loaded`)
}

func TestAdmissionFamilyDefault(t *testing.T) {
	fam := newFamily("deflt")
	def := imageModule(fam, "2", withDefault())
	create(t, def)
	create(t, imageModule(fam, "1"))
	def = waitResolved(t, def.Name)
	waitResolved(t, fam+"-1")
	ns := newNamespace(t, nil)
	admit(t, func() *corev1.Pod { return newPod(ns, fam) }, hasAnnotations(def.Status.Resolved.Annotations))
}

func TestAdmissionPodUpdate(t *testing.T) {
	fam := newFamily("upd")
	m := imageModule(fam, "1")
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	p := admit(t, func() *corev1.Pod { return newPod(ns, m.Name) }, hasAnnotations(m.Status.Resolved.Annotations))

	update := func(f func(*corev1.Pod)) error {
		cur := &corev1.Pod{}
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(p), cur); err != nil {
			return err
		}
		f(cur)
		return k8s.Update(ctx, cur)
	}
	if err := update(func(p *corev1.Pod) { p.Labels = map[string]string{"x": "y"}; p.Annotations["other"] = "ok" }); err != nil {
		t.Errorf("unrelated update rejected: %v", err)
	}
	for name, f := range map[string]func(*corev1.Pod){
		"change resolved": func(p *corev1.Pod) { p.Annotations[v1.AnnotationResolvedPrefix+fam] = "x;" },
		"remove resolved": func(p *corev1.Pod) { delete(p.Annotations, v1.AnnotationResolvedPrefix+fam) },
		"change load":     func(p *corev1.Pod) { p.Annotations[v1.AnnotationLoad] = "other-1" },
		"add env":         func(p *corev1.Pod) { p.Annotations[v1.AnnotationEnvPrefix+"NCCL_X"] = "1" },
	} {
		if err := update(f); err == nil || !strings.Contains(err.Error(), "cannot be changed after the pod is created") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestAdmissionPlainPodUntouched(t *testing.T) {
	ns := newNamespace(t, nil)
	p := newPod(ns, "")
	create(t, p)
	if len(p.Spec.Volumes) != 0 || len(p.Annotations) != 0 || len(p.Spec.Containers[0].Env) != 0 {
		t.Errorf("plain pod was mutated: %+v", p)
	}
}

func TestAdmissionLoadListWhitespaceAndDuplicates(t *testing.T) {
	fam := newFamily("ws")
	m := imageModule(fam, "1")
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	p := admit(t, func() *corev1.Pod { return newPod(ns, " "+m.Name+" , "+m.Name+" ,") }, hasAnnotations(m.Status.Resolved.Annotations))
	if len(p.Spec.Volumes) != 1 || len(p.Spec.Containers[0].VolumeMounts) != 1 {
		t.Errorf("duplicate load must inject once: %+v", p.Spec.Volumes)
	}
}

func TestAdmissionLongModuleName(t *testing.T) {
	// 36 + "-N" stays within the 40-char family limit; with a 20-char version
	// the name exceeds 59 chars, so the volume name takes the hash path.
	fam := newFamily(strings.Repeat("l", 36))
	m := imageModule(fam, strings.Repeat("9", 20))
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	p := admit(t, func() *corev1.Pod { return newPod(ns, m.Name) }, hasAnnotations(m.Status.Resolved.Annotations))
	if got := p.Spec.Volumes[0].Name; len(got) != 63 || got != resolve.VolumeName(m.Name) {
		t.Errorf("volume name %q", got)
	}
}

func TestModuleGuards(t *testing.T) {
	bad := imageModule(newFamily("guard"), "1")
	bad.Spec.Source.Image.Reference = "docker.io/evil/nccl" + digest()
	expectCreateError(t, bad, "is not in an allowed registry")

	expectCreateError(t, imageModule(newFamily("guard"), "1", withEnv("MY_VAR", "x")), "env.set name MY_VAR must start with one of")
}

func TestModuleOneDefaultPerFamily(t *testing.T) {
	fam := newFamily("onedef")
	create(t, imageModule(fam, "1", withDefault()))
	expectCreateError(t, imageModule(fam, "2", withDefault()), "is already the default for family")

	second := imageModule(fam, "3")
	create(t, second)
	eventually(t, 5*time.Second, func() error {
		cur := &v1.Module{}
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(second), cur); err != nil {
			return err
		}
		cur.Labels[v1.LabelDefault] = "true"
		err := k8s.Update(ctx, cur)
		if err == nil {
			return fmt.Errorf("label update to a second default was admitted")
		}
		if !strings.Contains(err.Error(), "is already the default for family") {
			return err
		}
		return nil
	})
}

func TestAdmissionForgedAnnotationUnderOtherKey(t *testing.T) {
	fam := newFamily("hidden")
	m := imageModule(fam, "1", withAccess(map[string]string{"tier": "gold"}))
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	// The restricted module's value filed under an unrelated family key would
	// otherwise reference no Module and escape the consistency check.
	other := v1.AnnotationResolvedPrefix + newFamily("other")
	reject(t, func() *corev1.Pod {
		p := newPod(ns, "")
		p.Annotations[other] = m.Status.Resolved.Annotations[v1.AnnotationResolvedPrefix+fam]
		return p
	}, "must name a module of family")
}

func TestAdmissionStatusSubresourceCannotChangeAnnotations(t *testing.T) {
	fam := newFamily("status")
	m := imageModule(fam, "1")
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	p := admit(t, func() *corev1.Pod { return newPod(ns, m.Name) }, hasAnnotations(m.Status.Resolved.Annotations))
	patch := []byte(`{"metadata":{"annotations":{"` + v1.AnnotationResolvedPrefix + fam + `":"forged;src=pvc:x/y"}}}`)
	err := k8s.Status().Patch(ctx, p, client.RawPatch(types.MergePatchType, patch))
	if err == nil || !strings.Contains(err.Error(), "cannot be changed after the pod is created") {
		t.Fatalf("status patch of module annotations: err = %v", err)
	}
}
