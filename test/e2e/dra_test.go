//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
)

const busybox = "docker.io/library/busybox:1.37"

func limits(keys ...string) corev1.ResourceRequirements {
	l := corev1.ResourceList{}
	for _, k := range keys {
		l[corev1.ResourceName(k)] = resource.MustParse("1")
	}
	return corev1.ResourceRequirements{Limits: l}
}

func sh(name, image, script string, res corev1.ResourceRequirements) corev1.Container {
	return corev1.Container{Name: name, Image: image, Command: []string{"sh", "-ec", script}, Resources: res}
}

func multiPod(ns, name string, anns map[string]string, cs ...corev1.Container) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Annotations: anns},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: cs}}
}

const (
	hasNCCL = `test -d /opt/modules/nccl/2.29.2
case "$LD_LIBRARY_PATH" in /opt/modules/nccl/2.29.2/lib*) ;; *) echo "LD_LIBRARY_PATH=$LD_LIBRARY_PATH"; exit 1;; esac
test "$BRICS_MODULES" = nccl-2.29.2`
	noModules = `test ! -e /opt/modules/nccl
test -z "${BRICS_MODULES:-}"
! env | grep -q '^BRICS_MODULE_'`
)

func TestDRAPerContainer(t *testing.T) {
	c := newClient(t)
	ns := namespace(t, c, "e2e-dra-per-container")
	runPodSpec(t, c, multiPod(ns, "per-container", nil,
		sh("trainer", busybox, hasNCCL, limits("modules.isambard.ac.uk/nccl-2.29.2")),
		sh("sidecar", busybox, noModules, corev1.ResourceRequirements{})))
}

func TestDRALoadContainers(t *testing.T) {
	c := newClient(t)
	ns := namespace(t, c, "e2e-dra-load-containers")
	runPodSpec(t, c, multiPod(ns, "load-containers",
		map[string]string{v1.AnnotationLoad: "nccl", v1.AnnotationLoadContainers: "trainer"},
		sh("trainer", busybox, hasNCCL, corev1.ResourceRequirements{}),
		sh("sidecar", busybox, noModules, corev1.ResourceRequirements{})))
}

func TestDRAFamilyLimit(t *testing.T) {
	c := newClient(t)
	ns := namespace(t, c, "e2e-dra-family")
	runPodSpec(t, c, multiPod(ns, "family", nil,
		sh("trainer", busybox, hasNCCL, limits("family.modules.isambard.ac.uk/nccl"))))
}

// One container requests two modules and a dependency of each. Every file
// is bound once (a duplicate CDI mount would fail container creation) and
// each library dir appears once, in rank order.
func TestDRAOverlappingClosures(t *testing.T) {
	c := newClient(t)
	img := ncclImage(t, c)
	createModule(t, c, hostFabric("host"))
	module(t, c, "e2e-ofi", "1", img, "nccl-2.29.2")
	module(t, c, "e2e-fabuser", "1", img, "fakefabric-host")
	waitReason(t, c, "e2e-ofi-1", "Resolved")
	waitReason(t, c, "e2e-fabuser-1", "Resolved")
	ns := namespace(t, c, "e2e-dra-overlap")
	runPodSpec(t, c, multiPod(ns, "overlap", nil, sh("check", clientImage, `
test "$BRICS_MODULES" = "e2e-fabuser-1:e2e-ofi-1:fakefabric-host:nccl-2.29.2" || { echo "BRICS_MODULES=$BRICS_MODULES"; exit 1; }
if echo "$LD_LIBRARY_PATH" | tr : '\n' | sort | uniq -d | grep .; then echo "duplicate in LD_LIBRARY_PATH=$LD_LIBRARY_PATH"; exit 1; fi
out=$(fakefabric-check); echo "$out"; test "$out" = fakedep=image`,
		limits("modules.isambard.ac.uk/nccl-2.29.2", "modules.isambard.ac.uk/e2e-ofi-1",
			"modules.isambard.ac.uk/fakefabric-host", "modules.isambard.ac.uk/e2e-fabuser-1"))))
}

// nodeShell runs script as root in the node's own filesystem on node and
// waits for it to succeed.
func nodeShell(t *testing.T, c client.Client, ns, node, script string) {
	t.Helper()
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "node-shell-", Namespace: ns},
		Spec: corev1.PodSpec{
			NodeName: node, RestartPolicy: corev1.RestartPolicyNever,
			Tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
			Containers: []corev1.Container{{Name: "sh", Image: busybox,
				Command:         []string{"chroot", "/host", "sh", "-ec", script},
				SecurityContext: &corev1.SecurityContext{Privileged: ptr.To(true)},
				VolumeMounts:    []corev1.VolumeMount{{Name: "host", MountPath: "/host"}}}},
			Volumes: []corev1.Volume{{Name: "host", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/"}}}},
		},
	}
	if err := c.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	defer c.Delete(ctx, p)
	waitFor(t, 2*time.Minute, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			return err
		}
		switch p.Status.Phase {
		case corev1.PodSucceeded:
			return nil
		case corev1.PodFailed:
			t.Fatalf("node shell on %s failed: %s", node, podLogs(p))
		}
		return fmt.Errorf("node shell phase %s", p.Status.Phase)
	})
}

func unschedulable(c client.Client, p *corev1.Pod) error {
	if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
		return err
	}
	if p.Spec.NodeName != "" {
		return fmt.Errorf("pod was scheduled to %s", p.Spec.NodeName)
	}
	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == corev1.PodReasonUnschedulable {
			return nil
		}
	}
	return fmt.Errorf("pod not marked unschedulable yet: %+v", p.Status.Conditions)
}

// A pod needing a host module that no node has stays Pending, then runs
// once the library appears and the next discovery republishes the device.
func TestDRAHostReadiness(t *testing.T) {
	c := newClient(t)
	createModule(t, c, &v1.Module{ObjectMeta: metav1.ObjectMeta{Name: "fakelate-1"},
		Spec: v1.ModuleSpec{Family: "fakelate", Version: "1", Source: v1.ModuleSource{Host: &v1.HostSource{
			Discover: v1.HostDiscover{Roots: []string{"libfakelate.so.1"}}}}}})
	ns := namespace(t, c, "e2e-dra-ready")
	nodes := &corev1.NodeList{}
	if err := c.List(ctx, nodes); err != nil {
		t.Fatal(err)
	}
	setLate := func(on bool) {
		script := `rm -f /etc/ld.so.conf.d/brics-late.conf && ldconfig`
		if on {
			script = `echo /usr/lib/brics-late > /etc/ld.so.conf.d/brics-late.conf && ldconfig`
		}
		for _, n := range nodes.Items {
			nodeShell(t, c, ns, n.Name, script)
		}
	}
	setLate(false)
	t.Cleanup(func() { setLate(false) })

	p := multiPod(ns, "late", nil, sh("check", busybox, `test -f /opt/modules/fakelate/1/lib/libfakelate.so.1`,
		limits("modules.isambard.ac.uk/fakelate-1")))
	waitFor(t, 30*time.Second, func() error { return c.Create(ctx, p) })
	t.Cleanup(func() { _ = c.Delete(ctx, p) })
	waitFor(t, time.Minute, func() error { return unschedulable(c, p) })
	time.Sleep(30 * time.Second) // two discovery intervals
	if err := unschedulable(c, p); err != nil {
		t.Fatalf("pod scheduled before the library existed: %v", err)
	}

	setLate(true)
	waitFor(t, 3*time.Minute, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			return err
		}
		switch p.Status.Phase {
		case corev1.PodSucceeded:
			return nil
		case corev1.PodFailed:
			t.Fatalf("pod failed: %s", podLogs(p))
		}
		return fmt.Errorf("pod phase %s %+v", p.Status.Phase, p.Status.Conditions)
	})
}

// A user's own ResourceClaim for a module DeviceClass is denied, since it
// would bypass the pod policies (namespace access).
func TestDRAUserClaimRejected(t *testing.T) {
	c := newClient(t)
	ns := namespace(t, c, "e2e-dra-claim")
	waitFor(t, 30*time.Second, func() error {
		rc := &resourcev1.ResourceClaim{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "mine-", Namespace: ns},
			Spec: resourcev1.ResourceClaimSpec{Devices: resourcev1.DeviceClaim{Requests: []resourcev1.DeviceRequest{{
				Name: "m", Exactly: &resourcev1.ExactDeviceRequest{DeviceClassName: "nccl-2.29.2.modules.isambard.ac.uk"}}}}},
		}
		err := c.Create(ctx, rc)
		if err == nil {
			_ = c.Delete(ctx, rc)
			return fmt.Errorf("user ResourceClaim %s for a module class was admitted", rc.Name)
		}
		if !strings.Contains(err.Error(), "module device classes") {
			return fmt.Errorf("unexpected error: %v", err)
		}
		return nil
	})
}
