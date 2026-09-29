package dra

import (
	"sort"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	"k8s.io/utils/ptr"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

// MaxDevicesPerSlice keeps slices well under the API limit.
const MaxDevicesPerSlice = 64

// Ready reports whether every member of m's closure can run on this node,
// and if not, why.
func Ready(m *v1.Module, cat Catalogue, hosts HostModules, pvcs PVCs) (bool, string) {
	if m.Status.Resolved == nil {
		return false, "module " + m.Name + " is not resolved"
	}
	for _, n := range m.Status.Resolved.Modules {
		dep, ok := cat.Module(n)
		if !ok {
			return false, "module " + n + " is not in the catalogue"
		}
		switch src := dep.Spec.Source; {
		case src.Host != nil:
			if r, ok := hosts.HostModule(n); !ok || !r.Ready {
				return false, "host module " + n + " is not ready on this node"
			}
		case src.PVC != nil:
			if !pvcs.Mounted(src.PVC.ClaimName) {
				return false, "pvc claim " + src.PVC.ClaimName + " is not mounted by module-nri"
			}
		}
	}
	return true, ""
}

// Devices lists one shareable device per Module that is Ready, sorted by
// name, so the scheduler only places pods where their modules can run.
func Devices(cat Catalogue, hosts HostModules, pvcs PVCs) []resourceapi.Device {
	var out []resourceapi.Device
	for _, m := range cat.List() {
		if ok, _ := Ready(&m, cat, hosts, pvcs); !ok {
			continue
		}
		out = append(out, resourceapi.Device{
			Name:                     resolve.DeviceName(m.Name),
			AllowMultipleAllocations: ptr.To(true),
			Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				v1.AttrModule:  {StringValue: ptr.To(m.Name)},
				v1.AttrFamily:  {StringValue: ptr.To(m.Spec.Family)},
				v1.AttrDefault: {BoolValue: ptr.To(m.Labels[v1.LabelDefault] == "true")},
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Resources puts devs in one pool, split into slices of MaxDevicesPerSlice.
func Resources(pool string, devs []resourceapi.Device) resourceslice.DriverResources {
	var ss []resourceslice.Slice
	for len(devs) > MaxDevicesPerSlice {
		ss = append(ss, resourceslice.Slice{Devices: devs[:MaxDevicesPerSlice]})
		devs = devs[MaxDevicesPerSlice:]
	}
	ss = append(ss, resourceslice.Slice{Devices: devs})
	return resourceslice.DriverResources{Pools: map[string]resourceslice.Pool{pool: {Slices: ss}}}
}
