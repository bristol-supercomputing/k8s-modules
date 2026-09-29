package dra

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"

	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

type fakePods map[string]*corev1.Pod

func (f fakePods) Pod(_ context.Context, ns, name string) (*corev1.Pod, error) {
	if p, ok := f[ns+"/"+name]; ok {
		return p, nil
	}
	return nil, errors.New("pods \"" + name + "\" not found")
}

func driverFor(t *testing.T, c fakeCatalogue, pod *corev1.Pod) *Driver {
	return &Driver{Planner: planner(c, nil), Pods: fakePods{pod.Namespace + "/" + pod.Name: pod}, CDIDir: t.TempDir(), Log: logr.Discard()}
}

func reserve(claim *resourceapi.ResourceClaim, pod *corev1.Pod) {
	pod.UID = "puid"
	claim.Status.ReservedFor = []resourceapi.ResourceClaimConsumerReference{{Resource: "pods", Name: pod.Name, UID: pod.UID}}
}

func TestPrepare(t *testing.T) {
	c := catalogue(t, withLib(mod("nccl", "2", img)))
	claim, pod := claimAndPod(c,
		req{"a", resolve.ResourceName("nccl-2"), "nccl-2"},
		req{"a", resolve.ResourceName("nccl-2"), "nccl-2"})
	reserve(claim, pod)
	d := driverFor(t, c, pod)
	res, err := d.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	if err != nil {
		t.Fatal(err)
	}
	r := res[claim.UID]
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	want := []kubeletplugin.Device{
		{Requests: []string{"container-0-request-0"}, PoolName: "n1", DeviceName: resolve.DeviceName("nccl-2"),
			CDIDeviceIDs: []string{CDIDeviceID(claim.UID, "container-0-request-0")}},
		{Requests: []string{"container-0-request-1"}, PoolName: "n1", DeviceName: resolve.DeviceName("nccl-2")},
	}
	if !reflect.DeepEqual(r.Devices, want) {
		t.Errorf("devices = %+v\nwant %+v", r.Devices, want)
	}
	if _, err := os.Stat(SpecPath(d.CDIDir, claim.UID)); err != nil {
		t.Error(err)
	}
}

func TestPrepareIdempotent(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	claim, pod := claimAndPod(c, req{"a", resolve.ResourceName("x-1"), "x-1"})
	reserve(claim, pod)
	d := driverFor(t, c, pod)
	first, err := d.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	if err != nil || first[claim.UID].Err != nil {
		t.Fatalf("first prepare: %v %v", err, first[claim.UID].Err)
	}
	a, err := os.ReadFile(SpecPath(d.CDIDir, claim.UID))
	if err != nil || len(a) == 0 {
		t.Fatalf("spec after first prepare: %q %v", a, err)
	}
	again, err := d.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	if err != nil || again[claim.UID].Err != nil {
		t.Fatalf("second prepare: %v %v", err, again[claim.UID].Err)
	}
	b, err := os.ReadFile(SpecPath(d.CDIDir, claim.UID))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, again) || string(a) != string(b) {
		t.Error("prepare must be idempotent")
	}
}

func TestPrepareRepeatedRequestGetsOneCDIID(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	claim, pod := claimAndPod(c, req{"a", resolve.ResourceName("x-1"), "x-1"})
	reserve(claim, pod)
	results := &claim.Status.Allocation.Devices.Results
	if len(*results) == 0 {
		t.Fatal("no allocation results")
	}
	*results = append(*results, (*results)[0])
	res, err := driverFor(t, c, pod).PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	if err != nil || res[claim.UID].Err != nil {
		t.Fatalf("prepare: %v %v", err, res[claim.UID].Err)
	}
	devs := res[claim.UID].Devices
	withID := 0
	for _, d := range devs {
		if len(d.CDIDeviceIDs) > 0 {
			withID++
		}
	}
	if len(devs) != 2 || withID != 1 || len(devs[0].CDIDeviceIDs) != 1 {
		t.Errorf("devices = %+v", devs)
	}
}

func TestPrepareReplacedPod(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	claim, pod := claimAndPod(c, req{"a", resolve.ResourceName("x-1"), "x-1"})
	reserve(claim, pod)
	claim.Status.ReservedFor[0].UID = types.UID("older")
	res, _ := driverFor(t, c, pod).PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	if err := res[claim.UID].Err; err == nil || !strings.Contains(err.Error(), "was replaced") {
		t.Errorf("err = %v", err)
	}
}

func TestUnprepare(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	claim, pod := claimAndPod(c, req{"a", resolve.ResourceName("x-1"), "x-1"})
	reserve(claim, pod)
	d := driverFor(t, c, pod)
	_, _ = d.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	res, err := d.UnprepareResourceClaims(context.Background(), []kubeletplugin.NamespacedObject{{UID: claim.UID}})
	if err != nil || res[claim.UID] != nil {
		t.Fatalf("unprepare: %v %v", res, err)
	}
	if _, err := os.Stat(SpecPath(d.CDIDir, claim.UID)); !os.IsNotExist(err) {
		t.Errorf("spec still present: %v", err)
	}
}
