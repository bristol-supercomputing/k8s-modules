//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
)

var ctx = context.Background()

func newClient(t *testing.T) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = v1.AddToScheme(s)
	c, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: s})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// ncclImage returns the digest-pinned nccl-fake reference setup.sh installed.
func ncclImage(t *testing.T, c client.Client) string {
	m := &v1.Module{}
	if err := c.Get(ctx, client.ObjectKey{Name: "nccl-2.29.2"}, m); err != nil {
		t.Fatalf("nccl-2.29.2 missing; run make kind-up: %v", err)
	}
	return m.Spec.Source.Image.Reference
}

func namespace(t *testing.T, c client.Client, name string) string {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	_ = c.Delete(ctx, ns)
	waitFor(t, 2*time.Minute, func() error {
		return c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})
	})
	t.Cleanup(func() { _ = c.Delete(ctx, ns) })
	return name
}

func module(t *testing.T, c client.Client, family, version, image string, deps ...string) *v1.Module {
	m := &v1.Module{
		ObjectMeta: metav1.ObjectMeta{Name: family + "-" + version},
		Spec: v1.ModuleSpec{Family: family, Version: version, DependsOn: deps,
			Source: v1.ModuleSource{Image: &v1.ImageSource{Reference: image}},
			Env:    &v1.ModuleEnv{PrependPath: map[string]v1.PathList{v1.EnvLDLibraryPath: {"lib"}}}},
	}
	_ = c.Delete(ctx, m)
	waitFor(t, 30*time.Second, func() error { return c.Create(ctx, m.DeepCopy()) })
	t.Cleanup(func() { _ = c.Delete(ctx, m) })
	return m
}

func waitFor(t *testing.T, d time.Duration, f func() error) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		err := f()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %v", err)
		}
		time.Sleep(time.Second)
	}
}

func waitReason(t *testing.T, c client.Client, name, reason string) {
	t.Helper()
	waitFor(t, time.Minute, func() error {
		m := &v1.Module{}
		if err := c.Get(ctx, client.ObjectKey{Name: name}, m); err != nil {
			return err
		}
		if cond := meta.FindStatusCondition(m.Status.Conditions, v1.ConditionResolved); cond == nil || cond.Reason != reason {
			return fmt.Errorf("%s Resolved condition = %+v, want reason %s", name, cond, reason)
		}
		return nil
	})
}

// runPod runs a shell script in busybox with the given modules loaded and
// waits for it to succeed.
func runPod(t *testing.T, c client.Client, ns, load, script string) {
	t.Helper()
	runPodSpec(t, c, shellPod(ns, "check", "docker.io/library/busybox:1.37", load, script))
}

// runPodSpec creates p and waits for it to succeed, printing its logs on failure.
func runPodSpec(t *testing.T, c client.Client, p *corev1.Pod) {
	t.Helper()
	waitFor(t, 30*time.Second, func() error { return c.Create(ctx, p) })
	t.Cleanup(func() { _ = c.Delete(ctx, p) })
	waitFor(t, 3*time.Minute, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			return err
		}
		switch p.Status.Phase {
		case corev1.PodSucceeded:
			return nil
		case corev1.PodFailed:
			t.Fatalf("pod %s failed: %s", p.Name, podLogs(p))
		}
		return fmt.Errorf("pod %s phase %s %+v", p.Name, p.Status.Phase, p.Status.ContainerStatuses)
	})
}

func podLogs(p *corev1.Pod) string {
	cs, err := kubernetes.NewForConfig(ctrl.GetConfigOrDie())
	if err != nil {
		return err.Error()
	}
	b, err := cs.CoreV1().Pods(p.Namespace).GetLogs(p.Name, &corev1.PodLogOptions{}).DoRaw(ctx)
	if err != nil {
		return err.Error()
	}
	return string(b)
}

func shellPod(ns, name, image, load, script string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Annotations: map[string]string{v1.AnnotationLoad: load}},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{
			Name: "check", Image: image, Command: []string{"sh", "-ec", script},
		}}},
	}
}

func expectRejected(t *testing.T, c client.Client, ns, load string, substrs ...string) {
	t.Helper()
	waitFor(t, 30*time.Second, func() error {
		p := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "reject-", Namespace: ns, Annotations: map[string]string{v1.AnnotationLoad: load}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "docker.io/library/busybox:1.37"}}},
		}
		err := c.Create(ctx, p)
		if err == nil {
			_ = c.Delete(ctx, p)
			return fmt.Errorf("pod loading %q was admitted", load)
		}
		for _, s := range substrs {
			if strings.Contains(err.Error(), s) {
				return nil
			}
		}
		return fmt.Errorf("error %q contains none of %q", err, substrs)
	})
}

func TestSingleModule(t *testing.T) {
	c := newClient(t)
	ns := namespace(t, c, "e2e-single")
	runPod(t, c, ns, "nccl-2.29.2", `
test -f /opt/modules/nccl/2.29.2/lib/libnccl.so.2
test "$(cat /opt/modules/nccl/2.29.2/VERSION)" = 2.29.2
test "$NCCL_DEBUG" = WARN
! touch /opt/modules/nccl/2.29.2/x 2>/dev/null`)
}

func TestFamilyDefault(t *testing.T) {
	c := newClient(t)
	ns := namespace(t, c, "e2e-default")
	runPod(t, c, ns, "nccl", `test -f /opt/modules/nccl/2.29.2/lib/libnccl.so.2`)
}

func TestDiamond(t *testing.T) {
	c := newClient(t)
	img := ncclImage(t, c)
	module(t, c, "e2e-left", "1", img, "nccl-2.29.2")
	module(t, c, "e2e-right", "1", img, "nccl-2.29.2")
	module(t, c, "e2e-top", "1", img, "e2e-left-1", "e2e-right-1")
	waitReason(t, c, "e2e-top-1", "Resolved")
	ns := namespace(t, c, "e2e-diamond")
	runPod(t, c, ns, "e2e-top-1", `
for m in nccl/2.29.2 e2e-left/1 e2e-right/1 e2e-top/1; do test -f /opt/modules/$m/lib/libnccl.so.2; done`)
}

func TestConflict(t *testing.T) {
	c := newClient(t)
	img := ncclImage(t, c)
	module(t, c, "nccl", "2.28.0", img)
	module(t, c, "e2e-both", "1", img, "nccl-2.29.2", "nccl-2.28.0")
	waitReason(t, c, "e2e-both-1", "Conflict")
	waitReason(t, c, "nccl-2.28.0", "Resolved")
	ns := namespace(t, c, "e2e-conflict")
	// The losing version is also "not loaded"; either policy may report first.
	expectRejected(t, c, ns, "nccl-2.29.2,nccl-2.28.0", "requires resolved.modules.isambard.ac.uk/nccl", "or in conflict with another loaded module")
	expectRejected(t, c, ns, "e2e-both-1", "is not resolved", "was not loaded")
}

func TestCycle(t *testing.T) {
	c := newClient(t)
	img := ncclImage(t, c)
	module(t, c, "e2e-cyca", "1", img, "e2e-cycb-1")
	module(t, c, "e2e-cycb", "1", img, "e2e-cyca-1")
	waitReason(t, c, "e2e-cyca-1", "DependencyCycle")
	ns := namespace(t, c, "e2e-cycle")
	expectRejected(t, c, ns, "e2e-cyca-1", "dependency cycle", "was not loaded")
}
