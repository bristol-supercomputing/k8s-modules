package dra

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

// req is one request of the scheduler's claim: container, resource, device module.
type req struct{ container, resource, module string }

// claimAndPod builds an allocated claim and its pod, as the scheduler and
// admission leave them. Request names follow the scheduler's form.
func claimAndPod(c fakeCatalogue, reqs ...req) (*resourceapi.ResourceClaim, *corev1.Pod) {
	claim := &resourceapi.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: "p-extended-resources-x", Namespace: "ns", UID: "cuid"},
		Status: resourceapi.ResourceClaimStatus{Allocation: &resourceapi.AllocationResult{}}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", Annotations: map[string]string{}},
		Status: corev1.PodStatus{ExtendedResourceClaimStatus: &corev1.PodExtendedResourceClaimStatus{ResourceClaimName: claim.Name}}}
	perContainer := map[string]int{}
	containerIdx := map[string]int{}
	for _, r := range reqs {
		if _, ok := containerIdx[r.container]; !ok {
			containerIdx[r.container] = len(containerIdx)
		}
		name := "container-" + itoa(containerIdx[r.container]) + "-request-" + itoa(perContainer[r.container])
		perContainer[r.container]++
		claim.Status.Allocation.Devices.Results = append(claim.Status.Allocation.Devices.Results, resourceapi.DeviceRequestAllocationResult{
			Request: name, Driver: v1.DriverName, Pool: "n1", Device: resolve.DeviceName(r.module)})
		pod.Status.ExtendedResourceClaimStatus.RequestMappings = append(pod.Status.ExtendedResourceClaimStatus.RequestMappings,
			corev1.ContainerExtendedResourceRequest{ContainerName: r.container, ResourceName: r.resource, RequestName: name})
		for k, v := range c[r.module].Status.Resolved.Annotations {
			pod.Annotations[k] = v
		}
	}
	return claim, pod
}

func itoa(i int) string { return strconv.Itoa(i) }

func planner(c fakeCatalogue, h fakeHosts) *Planner {
	return &Planner{Catalogue: c, Hosts: h, PVCs: fakePVCs{"spack-apps": "/var/lib/pv"}, NodeName: "n1", HookPath: "/opt/brics-modules/bin/module-hook"}
}

func envMap(e *ContainerEdits) map[string]string {
	m := map[string]string{}
	for _, kv := range e.Env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func mountAt(e *ContainerEdits, dst string) *Mount {
	for i := range e.Mounts {
		if e.Mounts[i].ContainerPath == dst {
			return &e.Mounts[i]
		}
	}
	return nil
}

func TestPlanHostAndImageClosure(t *testing.T) {
	nccl := withLib(mod("nccl", "2", img))
	fab := mod("fab", "host", hst)
	top := withLib(mod("ofi", "1", img, "nccl-2", "fab-host"))
	c := catalogue(t, nccl, fab, top)
	claim, pod := claimAndPod(c, req{"main", resolve.ResourceName("ofi-1"), "ofi-1"})

	out, err := planner(c, fakeHosts{"fab-host": hostResult()}).Plan(claim, pod)
	if err != nil {
		t.Fatal(err)
	}
	e := out["container-0-request-0"]
	if e == nil {
		t.Fatalf("plan = %+v", out)
	}
	env := envMap(e)
	for _, n := range []string{"ofi-1", "nccl-2", "fab-host"} {
		if !strings.HasPrefix(env[resolve.MarkerKey(n)], n+";") {
			t.Errorf("marker for %s = %q", n, env[resolve.MarkerKey(n)])
		}
	}
	if m := mountAt(e, "/opt/modules/fab/host/lib/libfab.so.1"); m == nil || m.HostPath != "/usr/lib64/libfab.so.1.2" || m.Type != "bind" ||
		!reflect.DeepEqual(m.Options, []string{"bind", "ro", "nosuid", "nodev", "rprivate"}) {
		t.Errorf("lib bind = %+v", m)
	}
	if m := mountAt(e, "/opt/modules/fab/host/deps/libdep.so.1"); m == nil || m.HostPath != "/usr/lib64/libdep.so.1.0" {
		t.Errorf("deps bind = %+v", m)
	}
	want := []Hook{{HookName: "createContainer", Path: "/opt/brics-modules/bin/module-hook",
		Args: []string{"module-hook", "--deps", "/opt/modules/fab/host/deps", "--glibc", "fab-host=2.36"}}}
	if !reflect.DeepEqual(e.Hooks, want) {
		t.Errorf("hooks = %+v", e.Hooks)
	}
}

func TestPlanPVC(t *testing.T) {
	c := catalogue(t, mod("sp", "1", pvc))
	claim, pod := claimAndPod(c, req{"main", resolve.ResourceName("sp-1"), "sp-1"})
	out, err := planner(c, nil).Plan(claim, pod)
	if err != nil {
		t.Fatal(err)
	}
	m := mountAt(out["container-0-request-0"], "/opt/modules/sp/1")
	if m == nil || m.HostPath != "/var/lib/pv/nccl/1" || !reflect.DeepEqual(m.Options, []string{"rbind", "ro", "nosuid", "nodev", "rprivate"}) {
		t.Errorf("pvc bind = %+v", m)
	}
}

// A container requesting a module and one of its dependencies gets each
// file once, all on its first request.
func TestPlanOverlappingClosures(t *testing.T) {
	fab := mod("fab", "host", hst)
	top := mod("user", "1", img, "fab-host")
	c := catalogue(t, fab, top)
	claim, pod := claimAndPod(c,
		req{"main", resolve.ResourceName("user-1"), "user-1"},
		req{"main", resolve.ResourceName("fab-host"), "fab-host"})
	out, err := planner(c, fakeHosts{"fab-host": hostResult()}).Plan(claim, pod)
	if err != nil {
		t.Fatal(err)
	}
	first, second := out["container-0-request-0"], out["container-0-request-1"]
	if first == nil || second != nil {
		t.Fatalf("edits must all be on the first request: %+v", out)
	}
	seen := map[string]bool{}
	for _, m := range first.Mounts {
		if seen[m.ContainerPath] {
			t.Errorf("duplicate mount %s", m.ContainerPath)
		}
		seen[m.ContainerPath] = true
	}
	if len(first.Hooks) != 1 {
		t.Errorf("hooks = %+v", first.Hooks)
	}
	// The dependency's marker keeps its rank inside user-1's closure (10),
	// and user-1 (20) comes first.
	if first.Env[0] != resolve.MarkerKey("user-1")+"="+c["user-1"].Status.Resolved.Annotations[resolve.ResolvedAnnotationKey("user")] {
		t.Errorf("env = %v", first.Env)
	}
}

func TestPlanSameModuleTwoContainers(t *testing.T) {
	c := catalogue(t, withLib(mod("nccl", "2", img)))
	claim, pod := claimAndPod(c,
		req{"a", resolve.ResourceName("nccl-2"), "nccl-2"},
		req{"b", resolve.ResourceName("nccl-2"), "nccl-2"})
	out, err := planner(c, nil).Plan(claim, pod)
	if err != nil {
		t.Fatal(err)
	}
	if out["container-0-request-0"] == nil || out["container-1-request-0"] == nil {
		t.Errorf("each container needs its own edits: %+v", out)
	}
}

func TestPlanFamilyDefaultMoved(t *testing.T) {
	old, cur := mod("nccl", "1", img), mod("nccl", "2", img)
	c := catalogue(t, old, cur)
	claim, pod := claimAndPod(c, req{"main", resolve.FamilyResourceName("nccl"), "nccl-2"})
	// Admission picked nccl-1; the family class then allocated nccl-2.
	pod.Annotations[resolve.ResolvedAnnotationKey("nccl")] = old.Status.Resolved.Annotations[resolve.ResolvedAnnotationKey("nccl")]
	_, err := planner(c, nil).Plan(claim, pod)
	if err == nil || err.Error() != "default for family nccl changed from nccl-1 to nccl-2 after admission; recreate the pod" {
		t.Errorf("err = %v", err)
	}
}

func TestPlanUnknownDevice(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	claim, pod := claimAndPod(c, req{"main", resolve.ResourceName("x-1"), "x-1"})
	delete(c, "x-1")
	_, err := planner(c, nil).Plan(claim, pod)
	if err == nil || !strings.Contains(err.Error(), "device "+resolve.DeviceName("x-1")+" is not a resolved Module") {
		t.Errorf("err = %v", err)
	}
}

func TestPlanHostNotReady(t *testing.T) {
	c := catalogue(t, mod("fab", "host", hst))
	claim, pod := claimAndPod(c, req{"main", resolve.ResourceName("fab-host"), "fab-host"})
	_, err := planner(c, fakeHosts{"fab-host": {Message: "root libfab.so.1 not in /etc/ld.so.cache"}}).Plan(claim, pod)
	if err == nil || !strings.Contains(err.Error(), "host module fab-host is not available on node n1: root libfab.so.1 not in /etc/ld.so.cache") {
		t.Errorf("err = %v", err)
	}
}

func TestPlanNeedsRequestMappings(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	claim, pod := claimAndPod(c, req{"main", resolve.ResourceName("x-1"), "x-1"})
	pod.Status.ExtendedResourceClaimStatus = nil
	if _, err := planner(c, nil).Plan(claim, pod); err == nil || !strings.Contains(err.Error(), "no extended resource claim status") {
		t.Errorf("err = %v", err)
	}
}

func TestPlanIgnoresOtherDrivers(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	claim, pod := claimAndPod(c, req{"main", resolve.ResourceName("x-1"), "x-1"})
	claim.Status.Allocation.Devices.Results = append(claim.Status.Allocation.Devices.Results,
		resourceapi.DeviceRequestAllocationResult{Request: "gpu", Driver: "gpu.example.com", Pool: "n1", Device: "gpu0"})
	out, err := planner(c, nil).Plan(claim, pod)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["gpu"]; ok {
		t.Error("other drivers' requests must not be planned")
	}
}

// An extended-resource limit above 1 allocates several devices under one
// request name; the request must keep its edits, without duplicates.
func TestPlanDuplicateRequestName(t *testing.T) {
	c := catalogue(t, withLib(mod("nccl", "2", img)))
	claim, pod := claimAndPod(c, req{"main", resolve.ResourceName("nccl-2"), "nccl-2"})
	claim.Status.Allocation.Devices.Results = append(claim.Status.Allocation.Devices.Results, claim.Status.Allocation.Devices.Results[0])
	out, err := planner(c, nil).Plan(claim, pod)
	if err != nil {
		t.Fatal(err)
	}
	e := out["container-0-request-0"]
	if e == nil {
		t.Fatalf("request lost its edits: %+v", out)
	}
	if len(e.Env) != 1 {
		t.Errorf("env = %v", e.Env)
	}
	seen := map[string]bool{}
	for _, m := range e.Mounts {
		if seen[m.ContainerPath] {
			t.Errorf("duplicate mount %s", m.ContainerPath)
		}
		seen[m.ContainerPath] = true
	}
}

// The family class allocated the default admission recorded.
func TestPlanFamilyDefault(t *testing.T) {
	fab := mod("fab", "host", hst)
	cur := withLib(mod("nccl", "2", img, "fab-host"))
	c := catalogue(t, mod("nccl", "1", img), cur, fab)
	claim, pod := claimAndPod(c, req{"main", resolve.FamilyResourceName("nccl"), "nccl-2"})
	if !strings.HasPrefix(pod.Annotations[resolve.ResolvedAnnotationKey("nccl")], "nccl-2;") {
		t.Fatalf("fixture: resolved annotation = %q", pod.Annotations[resolve.ResolvedAnnotationKey("nccl")])
	}
	out, err := planner(c, fakeHosts{"fab-host": hostResult()}).Plan(claim, pod)
	if err != nil {
		t.Fatal(err)
	}
	e := out["container-0-request-0"]
	if e == nil {
		t.Fatalf("plan = %+v", out)
	}
	env := envMap(e)
	for _, n := range []string{"nccl-2", "fab-host"} {
		if !strings.HasPrefix(env[resolve.MarkerKey(n)], n+";") {
			t.Errorf("marker for %s = %q", n, env[resolve.MarkerKey(n)])
		}
	}
	if _, ok := env[resolve.MarkerKey("nccl-1")]; ok {
		t.Error("nccl-1 must not be planned")
	}
	if mountAt(e, "/opt/modules/fab/host/lib/libfab.so.1") == nil || len(e.Hooks) != 1 {
		t.Errorf("edits = %+v", e)
	}
}

func TestPlanUnallocatedClaim(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	claim, pod := claimAndPod(c, req{"main", resolve.ResourceName("x-1"), "x-1"})
	claim.Status.Allocation = nil
	_, err := planner(c, nil).Plan(claim, pod)
	if err == nil || err.Error() != "claim ns/p-extended-resources-x is not allocated" {
		t.Errorf("err = %v", err)
	}
}

func TestPlanUnmappedRequest(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	claim, pod := claimAndPod(c, req{"main", resolve.ResourceName("x-1"), "x-1"})
	pod.Status.ExtendedResourceClaimStatus.RequestMappings = nil
	_, err := planner(c, nil).Plan(claim, pod)
	if err == nil || err.Error() != "request container-0-request-0 is not mapped to a container" {
		t.Errorf("err = %v", err)
	}
}
