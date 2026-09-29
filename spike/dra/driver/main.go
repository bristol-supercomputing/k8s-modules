// Spike: a combined DRA kubelet plugin + NRI plugin, to answer the open
// questions about extended-resource-backed module devices.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	"k8s.io/utils/ptr"
)

const (
	driverName = "modules.isambard.ac.uk"
	cdiDir     = "/var/run/cdi"
	cdiKind    = "modules.isambard.ac.uk/module"
)

// Devices the spike advertises.
var devices = map[string]string{"nccl-2-29-2": "nccl-2.29.2", "aws-ofi-nccl-1-18-0": "aws-ofi-nccl-1.18.0"}

type driver struct{ nodeName string }

func (d *driver) HandleError(_ context.Context, err error, msg string) {
	fmt.Fprintf(os.Stderr, "DRA error: %s: %v\n", msg, err)
}

// One CDI device per (claim, request) so the log shows how the kubelet maps
// request names to containers.
func (d *driver) PrepareResourceClaims(_ context.Context, claims []*resourceapi.ResourceClaim) (map[types.UID]kubeletplugin.PrepareResult, error) {
	out := map[types.UID]kubeletplugin.PrepareResult{}
	for _, c := range claims {
		var res kubeletplugin.PrepareResult
		spec := map[string]any{"cdiVersion": "0.6.0", "kind": cdiKind}
		var cdiDevs []map[string]any
		for _, r := range c.Status.Allocation.Devices.Results {
			if r.Driver != driverName {
				continue
			}
			name := fmt.Sprintf("%s-%s", c.UID, r.Request)
			fmt.Printf("PREPARE claim=%s/%s uid=%s request=%q device=%s shareID=%v\n",
				c.Namespace, c.Name, c.UID, r.Request, r.Device, r.ShareID)
			cdiDevs = append(cdiDevs, map[string]any{
				"name": name,
				"annotations": map[string]string{"spike.modules/dev-" + r.Device: "1"},
				"containerEdits": map[string]any{
					"env": []string{
						"SPIKE_CDI_REQUEST=" + r.Request,
						"SPIKE_CDI_DEVICE=" + r.Device,
						"SPIKE_MOD_" + strings.ToUpper(strings.ReplaceAll(r.Device, "-", "_")) + "=1",
						"LD_LIBRARY_PATH=/from/cdi/" + r.Device,
					},
				},
			})
			dev := kubeletplugin.Device{
				Requests: []string{r.Request}, PoolName: r.Pool, DeviceName: r.Device,
				CDIDeviceIDs: []string{cdiKind + "=" + name},
			}
			if r.ShareID != nil {
				dev.ShareID = r.ShareID
			}
			res.Devices = append(res.Devices, dev)
		}
		spec["devices"] = cdiDevs
		b, _ := json.MarshalIndent(spec, "", " ")
		f := filepath.Join(cdiDir, "modules.isambard.ac.uk-"+string(c.UID)+".json")
		if err := os.WriteFile(f, b, 0o644); err != nil {
			res.Err = err
		}
		out[c.UID] = res
	}
	return out, nil
}

func (d *driver) UnprepareResourceClaims(_ context.Context, claims []kubeletplugin.NamespacedObject) (map[types.UID]error, error) {
	out := map[types.UID]error{}
	for _, c := range claims {
		fmt.Printf("UNPREPARE claim=%s/%s uid=%s\n", c.Namespace, c.Name, c.UID)
		_ = os.Remove(filepath.Join(cdiDir, "modules.isambard.ac.uk-"+string(c.UID)+".json"))
		out[c.UID] = nil
	}
	return out, nil
}

// NRI plugin: logs what env it sees at CreateContainer and prepends to
// LD_LIBRARY_PATH, like module-nri does.
type nriPlugin struct{}

func (nriPlugin) CreateContainer(_ context.Context, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	if pod.Namespace != "spike" {
		return nil, nil, nil
	}
	var seen []string
	ld := ""
	for _, e := range ctr.Env {
		if strings.HasPrefix(e, "SPIKE_") || strings.HasPrefix(e, "LD_LIBRARY_PATH=") {
			seen = append(seen, e)
		}
		if v, ok := strings.CutPrefix(e, "LD_LIBRARY_PATH="); ok {
			ld = v
		}
	}
	fmt.Printf("NRI CreateContainer pod=%s ctr=%s env-seen=%v annotations=%v\n", pod.Name, ctr.Name, seen, ctr.Annotations)
	adj := &api.ContainerAdjustment{}
	nv := "/from/nri"
	if ld != "" {
		nv += ":" + ld
	}
	adj.AddEnv("LD_LIBRARY_PATH", nv)
	return adj, nil, nil
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	node := os.Getenv("NODE_NAME")
	cfg, err := rest.InClusterConfig()
	must(err)
	kc, err := kubernetes.NewForConfig(cfg)
	must(err)

	d := &driver{nodeName: node}
	helper, err := kubeletplugin.Start(ctx, d,
		kubeletplugin.DriverName(driverName),
		kubeletplugin.KubeClient(kc),
		kubeletplugin.NodeName(node),
		kubeletplugin.CDIDirectory(cdiDir),
	)
	must(err)
	defer helper.Stop()

	var devs []resourceapi.Device
	for n, mod := range devices {
		devs = append(devs, resourceapi.Device{
			Name:                     n,
			AllowMultipleAllocations: ptr.To(true),
			Attributes:               map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{"module": {StringValue: ptr.To(mod)}},
		})
	}
	must(helper.PublishResources(ctx, resourceslice.DriverResources{Pools: map[string]resourceslice.Pool{
		node: {Slices: []resourceslice.Slice{{Devices: devs}}},
	}}))

	s, err := stub.New(nriPlugin{}, stub.WithPluginName("spike-modules"), stub.WithPluginIdx("99"))
	must(err)
	go func() { must(s.Run(ctx)) }()
	fmt.Println("spike driver running on", node)
	<-ctx.Done()
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
