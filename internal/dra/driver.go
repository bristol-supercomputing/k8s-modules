package dra

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
)

// Pods reads the pod a claim is reserved for.
type Pods interface {
	Pod(ctx context.Context, namespace, name string) (*corev1.Pod, error)
}

// ClientsetPods reads pods from the API server; prepare needs the pod's
// current status, which a cache might not have yet.
type ClientsetPods struct{ Client kubernetes.Interface }

func (c ClientsetPods) Pod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	return c.Client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
}

// Driver is the kubelet plugin: it prepares the scheduler's
// extended-resource claims by writing their CDI specs.
type Driver struct {
	Planner *Planner
	Pods    Pods
	CDIDir  string
	Log     logr.Logger
}

var _ kubeletplugin.DRAPlugin = (*Driver)(nil)

func (d *Driver) PrepareResourceClaims(ctx context.Context, claims []*resourceapi.ResourceClaim) (map[types.UID]kubeletplugin.PrepareResult, error) {
	out := map[types.UID]kubeletplugin.PrepareResult{}
	for _, c := range claims {
		out[c.UID] = d.prepare(ctx, c)
	}
	return out, nil
}

func (d *Driver) prepare(ctx context.Context, c *resourceapi.ResourceClaim) kubeletplugin.PrepareResult {
	id := c.Namespace + "/" + c.Name
	fail := func(err error) kubeletplugin.PrepareResult {
		d.Log.Info("refusing claim", "claim", id, "reason", err.Error())
		return kubeletplugin.PrepareResult{Err: fmt.Errorf("modules: %w", err)}
	}
	if len(c.Status.ReservedFor) != 1 || c.Status.ReservedFor[0].Resource != "pods" {
		return fail(fmt.Errorf("claim %s must be reserved for exactly one pod", id))
	}
	ref := c.Status.ReservedFor[0]
	pod, err := d.Pods.Pod(ctx, c.Namespace, ref.Name)
	if err != nil {
		return fail(err)
	}
	if pod.UID != ref.UID {
		return fail(fmt.Errorf("pod %s/%s was replaced after claim %s was reserved", c.Namespace, ref.Name, id))
	}
	edits, err := d.Planner.Plan(c, pod)
	if err != nil {
		return fail(err)
	}
	if err := WriteSpec(d.CDIDir, c.UID, edits); err != nil {
		return fail(err)
	}
	var res kubeletplugin.PrepareResult
	for _, r := range c.Status.Allocation.Devices.Results {
		if r.Driver != v1.DriverName {
			continue
		}
		dev := kubeletplugin.Device{Requests: []string{r.Request}, PoolName: r.Pool, DeviceName: r.Device, ShareID: r.ShareID}
		if edits[r.Request] != nil {
			dev.CDIDeviceIDs = []string{CDIDeviceID(c.UID, r.Request)}
		}
		res.Devices = append(res.Devices, dev)
	}
	d.Log.Info("prepared claim", "claim", id, "pod", pod.Name)
	return res
}

func (d *Driver) UnprepareResourceClaims(_ context.Context, claims []kubeletplugin.NamespacedObject) (map[types.UID]error, error) {
	out := map[types.UID]error{}
	for _, c := range claims {
		out[c.UID] = RemoveSpec(d.CDIDir, c.UID)
	}
	return out, nil
}

func (d *Driver) HandleError(_ context.Context, err error, msg string) {
	d.Log.Error(err, msg)
}
