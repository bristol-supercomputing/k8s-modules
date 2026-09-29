package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
)

func TestAdmissionNRIAnnotations(t *testing.T) {
	m := imageModule(newFamily("nriann"), "1")
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	p := admit(t, func() *corev1.Pod {
		return newPod(ns, m.Name, corev1.Container{Name: "a", Image: "busybox"}, corev1.Container{Name: "b", Image: "busybox"})
	}, hasAnnotations(map[string]string{v1.AnnotationContainers: "a,b", v1.AnnotationRequiredPlugins: v1.RequiredPluginsValue}))

	for key, value := range map[string]string{v1.AnnotationContainers: "a", v1.AnnotationRequiredPlugins: "[]"} {
		cur := &corev1.Pod{}
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(p), cur); err != nil {
			t.Fatal(err)
		}
		cur.Annotations[key] = value
		if err := k8s.Update(ctx, cur); err == nil || !strings.Contains(err.Error(), "cannot be changed after the pod is created") {
			t.Errorf("changing %s: err = %v", key, err)
		}
	}

	plain := newPod(ns, "")
	create(t, plain)
	if _, ok := plain.Annotations[v1.AnnotationContainers]; ok {
		t.Error("a pod loading no modules must not get NRI annotations")
	}
}

func TestAdmissionNRIAnnotationsRequired(t *testing.T) {
	fam := newFamily("nrireq")
	m := imageModule(fam, "1")
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	key := v1.AnnotationResolvedPrefix + fam
	written := func(extra map[string]string) func() *corev1.Pod {
		return func() *corev1.Pod {
			p := newPod(ns, "")
			p.Annotations[key] = m.Status.Resolved.Annotations[key]
			for k, v := range extra {
				p.Annotations[k] = v
			}
			return p
		}
	}
	reject(t, written(nil), "modules.isambard.ac.uk/containers must list the pod's containers")
	reject(t, written(map[string]string{v1.AnnotationContainers: "main"}), "required-plugins.noderesource.dev/pod must be")
	admit(t, written(map[string]string{v1.AnnotationContainers: "main", v1.AnnotationRequiredPlugins: v1.RequiredPluginsValue}),
		hasAnnotations(m.Status.Resolved.Annotations))
}

func TestModuleReportNodeIdentity(t *testing.T) {
	as := func(node string) client.Client {
		cfg := rest.CopyConfig(restCfg)
		cfg.Impersonate = rest.ImpersonationConfig{
			UserName: "system:serviceaccount:module-system:module-nri",
			Groups:   []string{"system:masters", "system:authenticated"},
			Extra:    map[string][]string{"authentication.kubernetes.io/node-name": {node}},
		}
		c, err := client.New(cfg, client.Options{Scheme: scheme})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	n := familySeq.Add(1)
	a, b := fmt.Sprintf("ident-a-%d", n), fmt.Sprintf("ident-b-%d", n)
	eventually(t, 10*time.Second, func() error {
		other := &v1.ModuleNodeReport{ObjectMeta: metav1.ObjectMeta{Name: b}}
		err := as(a).Create(ctx, other)
		if err == nil {
			_ = k8s.Delete(ctx, other)
			return fmt.Errorf("node %s wrote %s's report", a, b)
		}
		if !strings.Contains(err.Error(), "may only write its own ModuleNodeReport") {
			return err
		}
		return nil
	})
	own := &v1.ModuleNodeReport{ObjectMeta: metav1.ObjectMeta{Name: a}}
	if err := as(a).Create(ctx, own); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = k8s.Delete(ctx, own) })
	own.Status.Modules = []v1.HostModuleReport{{Module: "x-host", Family: "x", Ready: true}}
	if err := as(a).Status().Update(ctx, own); err != nil {
		t.Errorf("own status update: %v", err)
	}
	// Callers without the node-name claim (admins, the controller) are not restricted.
	admin := &v1.ModuleNodeReport{ObjectMeta: metav1.ObjectMeta{Name: b}}
	create(t, admin)
}

// NRI's validator gives required-plugins.noderesource.dev/container.<name>
// precedence over /pod, so any other required-plugins key on a module pod
// would let it start without the plugin.
func TestAdmissionRejectsOtherRequiredPluginsKeys(t *testing.T) {
	m := imageModule(newFamily("nrikey"), "1")
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	for _, key := range []string{
		"required-plugins.noderesource.dev/container.main",
		"required-plugins.noderesource.dev",
	} {
		reject(t, func() *corev1.Pod {
			p := newPod(ns, m.Name)
			p.Annotations[key] = "[]"
			return p
		}, "may not set other required-plugins.noderesource.dev annotations")
	}
	p := admit(t, func() *corev1.Pod { return newPod(ns, m.Name) }, hasAnnotations(m.Status.Resolved.Annotations))
	cur := &corev1.Pod{}
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(p), cur); err != nil {
		t.Fatal(err)
	}
	cur.Annotations["required-plugins.noderesource.dev/container.main"] = "[]"
	if err := k8s.Update(ctx, cur); err == nil || !strings.Contains(err.Error(), "cannot be changed after the pod is created") {
		t.Errorf("adding a container-scoped key on UPDATE: err = %v", err)
	}
}
