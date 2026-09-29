package dra

import (
	"fmt"
	"testing"

	resourceapi "k8s.io/api/resource/v1"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/discovery"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

func names(devs []resourceapi.Device) []string {
	var out []string
	for _, d := range devs {
		out = append(out, *d.Attributes[v1.AttrModule].StringValue)
	}
	return out
}

func TestDevicesReadiness(t *testing.T) {
	fab := mod("fab", "host", hst)
	top := mod("ofi", "1", img, "fab-host")
	sp := mod("sp", "1", pvc)
	other := mod("other", "1", v1.ModuleSource{PVC: &v1.PVCSource{ClaimName: "unmounted", SubPath: "x"}})
	c := catalogue(t, fab, top, sp, other)
	unresolved := mod("new", "1", img)
	c[unresolved.Name] = unresolved

	cases := map[string]struct {
		hosts fakeHosts
		want  string
	}{
		"host ready":     {fakeHosts{"fab-host": hostResult()}, "[fab-host ofi-1 sp-1]"},
		"host not ready": {fakeHosts{"fab-host": discovery.Result{Message: "missing"}}, "[sp-1]"},
		"not discovered": {fakeHosts{}, "[sp-1]"},
	}
	for name, tc := range cases {
		got := fmt.Sprint(names(Devices(c, tc.hosts, fakePVCs{"spack-apps": "/pv"})))
		if got != tc.want {
			t.Errorf("%s: devices %s, want %s", name, got, tc.want)
		}
	}
}

func TestDeviceShape(t *testing.T) {
	m := mod("nccl", "2.29.2", img)
	m.Labels[v1.LabelDefault] = "true"
	devs := Devices(catalogue(t, m), fakeHosts{}, fakePVCs{})
	if len(devs) != 1 {
		t.Fatalf("devices = %+v", devs)
	}
	d := devs[0]
	if d.Name != resolve.DeviceName("nccl-2.29.2") || d.AllowMultipleAllocations == nil || !*d.AllowMultipleAllocations {
		t.Errorf("device = %+v", d)
	}
	if *d.Attributes[v1.AttrFamily].StringValue != "nccl" || !*d.Attributes[v1.AttrDefault].BoolValue {
		t.Errorf("attributes = %+v", d.Attributes)
	}
}

func TestReadyReason(t *testing.T) {
	c := catalogue(t, mod("fab", "host", hst))
	ok, why := Ready(c["fab-host"], c, fakeHosts{}, fakePVCs{})
	if ok || why != "host module fab-host is not ready on this node" {
		t.Errorf("Ready = %v %q", ok, why)
	}
}

func TestResourcesChunks(t *testing.T) {
	devs := make([]resourceapi.Device, 130)
	for i := range devs {
		devs[i].Name = fmt.Sprintf("d%d", i)
	}
	r := Resources("node-1", devs)
	p, ok := r.Pools["node-1"]
	if !ok || len(p.Slices) != 3 || len(p.Slices[0].Devices) != 64 || len(p.Slices[2].Devices) != 2 {
		t.Errorf("pools = %+v", r.Pools)
	}
	if empty := Resources("n", nil); len(empty.Pools["n"].Slices) != 1 || len(empty.Pools["n"].Slices[0].Devices) != 0 {
		t.Errorf("no devices: want one empty slice, got %+v", empty.Pools)
	}
}
