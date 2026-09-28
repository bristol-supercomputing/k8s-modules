//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

const (
	clientImage    = "kind-registry:5000/fakefabric-client:dev"
	clientOldImage = "kind-registry:5000/fakefabric-client-old:dev"
)

func createModule(t *testing.T, c client.Client, m *v1.Module) {
	t.Helper()
	_ = c.Delete(ctx, m.DeepCopy())
	waitFor(t, 30*time.Second, func() error { return c.Create(ctx, m.DeepCopy()) })
	t.Cleanup(func() { _ = c.Delete(ctx, m) })
	waitReason(t, c, m.Name, "Resolved")
}

func hostFabric(version string, prefer ...string) *v1.Module {
	return &v1.Module{ObjectMeta: metav1.ObjectMeta{Name: "fakefabric-" + version},
		Spec: v1.ModuleSpec{Family: "fakefabric", Version: version, Source: v1.ModuleSource{Host: &v1.HostSource{
			Discover: v1.HostDiscover{Roots: []string{"libfakefabric.so.1"}}, PreferHost: prefer}}}}
}

// waitContainerError waits until the pod's container fails to start with a
// message containing substr.
func waitContainerError(t *testing.T, c client.Client, p *corev1.Pod, substr string) {
	t.Helper()
	waitFor(t, 3*time.Minute, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			return err
		}
		for _, s := range p.Status.ContainerStatuses {
			for _, msg := range []string{waitingMsg(s.State), terminatedMsg(s.State), terminatedMsg(s.LastTerminationState)} {
				if strings.Contains(msg, substr) {
					return nil
				}
			}
		}
		return fmt.Errorf("pod %s: no container error containing %q yet: %+v", p.Name, substr, p.Status.ContainerStatuses)
	})
}

func waitingMsg(s corev1.ContainerState) string {
	if s.Waiting != nil {
		return s.Waiting.Message
	}
	return ""
}

func terminatedMsg(s corev1.ContainerState) string {
	if s.Terminated != nil {
		return s.Terminated.Message
	}
	return ""
}

func TestNRIImageModuleEnv(t *testing.T) {
	c := newClient(t)
	ns := namespace(t, c, "e2e-nri-env")
	runPodSpec(t, c, shellPod(ns, "env", "docker.io/library/busybox:1.37", "nccl-2.29.2", `
case "$LD_LIBRARY_PATH" in /opt/modules/nccl/2.29.2/lib*) ;; *) echo "LD_LIBRARY_PATH=$LD_LIBRARY_PATH"; exit 1;; esac
test "$BRICS_MODULES" = nccl-2.29.2`))
}

func TestNRIHostModule(t *testing.T) {
	c := newClient(t)
	createModule(t, c, hostFabric("host"))
	ns := namespace(t, c, "e2e-nri-host")
	runPodSpec(t, c, shellPod(ns, "host", clientImage, "fakefabric-host", `
test -f /opt/modules/fakefabric/host/lib/libfakefabric.so.1
test -f /opt/modules/fakefabric/host/deps/libfakedep.so.1
ldconfig -p | grep -q /opt/modules/fakefabric/host/deps/libfakedep.so.1
out=$(fakefabric-check); echo "$out"; test "$out" = fakedep=image`))
}

func TestNRIPreferHost(t *testing.T) {
	c := newClient(t)
	createModule(t, c, hostFabric("prefer", "libfakedep.so.1"))
	ns := namespace(t, c, "e2e-nri-prefer")
	runPodSpec(t, c, shellPod(ns, "prefer", clientImage, "fakefabric-prefer", `
out=$(fakefabric-check); echo "$out"; test "$out" = fakedep=host`))
}

func TestNRIGlibcTooOld(t *testing.T) {
	c := newClient(t)
	createModule(t, c, hostFabric("host"))
	ns := namespace(t, c, "e2e-nri-glibc")
	p := shellPod(ns, "old", clientOldImage, "fakefabric-host", "fakefabric-check")
	waitFor(t, 30*time.Second, func() error { return c.Create(ctx, p) })
	t.Cleanup(func() { _ = c.Delete(ctx, p) })
	waitContainerError(t, c, p, "module fakefabric-host needs GLIBC_2.36")
}

func TestNRIMaskPaths(t *testing.T) {
	c := newClient(t)
	createModule(t, c, &v1.Module{ObjectMeta: metav1.ObjectMeta{Name: "e2e-mask-1"},
		Spec: v1.ModuleSpec{Family: "e2e-mask", Version: "1", MaskPaths: []string{"/opt/masked"},
			Source: v1.ModuleSource{Image: &v1.ImageSource{Reference: ncclImage(t, c)}}}})
	ns := namespace(t, c, "e2e-nri-mask")
	runPodSpec(t, c, shellPod(ns, "mask", clientImage, "e2e-mask-1", `test ! -e /opt/masked/secret`))
}

func TestNRIPVCModule(t *testing.T) {
	c := newClient(t)
	createModule(t, c, &v1.Module{ObjectMeta: metav1.ObjectMeta{Name: "e2e-pvc-1"},
		Spec: v1.ModuleSpec{Family: "e2e-pvc", Version: "1",
			Source: v1.ModuleSource{PVC: &v1.PVCSource{ClaimName: "spack-apps", SubPath: "."}}}})
	ns := namespace(t, c, "e2e-nri-pvc")
	runPodSpec(t, c, shellPod(ns, "pvc", "docker.io/library/busybox:1.37", "e2e-pvc-1", `
test -f /opt/modules/e2e-pvc/1/lib/libpvc.so
! touch /opt/modules/e2e-pvc/1/x 2>/dev/null`))
}

func TestNRIFailClosed(t *testing.T) {
	c := newClient(t)
	ds := types.NamespacedName{Namespace: "module-system", Name: "module-nri"}
	setSelector := func(sel map[string]string) {
		patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"nodeSelector": sel}}}})
		if err := c.Patch(ctx, &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Namespace: ds.Namespace, Name: ds.Name}}, client.RawPatch(types.StrategicMergePatchType, patch)); err != nil {
			t.Fatal(err)
		}
	}
	dsReady := func(want int32) func() error {
		return func() error {
			d := &appsv1.DaemonSet{}
			if err := c.Get(ctx, ds, d); err != nil {
				return err
			}
			if d.Status.NumberReady != want || d.Status.DesiredNumberScheduled != want || d.Status.UpdatedNumberScheduled != want {
				return fmt.Errorf("daemonset ready=%d desired=%d, want %d", d.Status.NumberReady, d.Status.DesiredNumberScheduled, want)
			}
			return nil
		}
	}
	nodes := &corev1.NodeList{}
	if err := c.List(ctx, nodes); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		setSelector(nil)
		waitFor(t, 3*time.Minute, dsReady(int32(len(nodes.Items))))
	})
	setSelector(map[string]string{"brics-test/nowhere": "true"})
	waitFor(t, 2*time.Minute, func() error {
		pods := &corev1.PodList{}
		if err := c.List(ctx, pods, client.InNamespace(ds.Namespace), client.MatchingLabels{"app.kubernetes.io/name": "module-nri"}); err != nil {
			return err
		}
		if len(pods.Items) != 0 {
			return fmt.Errorf("%d module-nri pods still present", len(pods.Items))
		}
		return nil
	})

	ns := namespace(t, c, "e2e-nri-closed")
	p := shellPod(ns, "needs-plugin", "docker.io/library/busybox:1.37", "nccl-2.29.2", "true")
	waitFor(t, 30*time.Second, func() error { return c.Create(ctx, p) })
	t.Cleanup(func() { _ = c.Delete(ctx, p) })
	waitContainerError(t, c, p, `required plugin "modules" not present`)

	plain := shellPod(ns, "plain", "docker.io/library/busybox:1.37", "", "true")
	delete(plain.Annotations, v1.AnnotationLoad)
	runPodSpec(t, c, plain)
}

func TestNRINodeReportAndLabels(t *testing.T) {
	c := newClient(t)
	createModule(t, c, hostFabric("host"))
	nodes := &corev1.NodeList{}
	if err := c.List(ctx, nodes); err != nil {
		t.Fatal(err)
	}
	var hash string
	for _, n := range nodes.Items {
		waitFor(t, 2*time.Minute, func() error {
			r := &v1.ModuleNodeReport{}
			if err := c.Get(ctx, client.ObjectKey{Name: n.Name}, r); err != nil {
				return err
			}
			for _, m := range r.Status.Modules {
				if m.Module != "fakefabric-host" {
					continue
				}
				if !m.Ready {
					return fmt.Errorf("node %s: not ready: %s", n.Name, m.Message)
				}
				if hash == "" {
					hash = m.ClosureHash
				}
				if m.ClosureHash != hash {
					return fmt.Errorf("node %s hash %s differs from %s", n.Name, m.ClosureHash, hash)
				}
				return nil
			}
			return fmt.Errorf("node %s: fakefabric-host not reported", n.Name)
		})
		waitFor(t, time.Minute, func() error {
			node := &corev1.Node{}
			if err := c.Get(ctx, client.ObjectKey{Name: n.Name}, node); err != nil {
				return err
			}
			if node.Labels[v1.LabelHostPrefix+"fakefabric"] != "true" {
				return fmt.Errorf("node %s lacks %sfakefabric", n.Name, v1.LabelHostPrefix)
			}
			return nil
		})
	}
}
