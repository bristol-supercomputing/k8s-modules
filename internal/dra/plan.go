package dra

import (
	"fmt"
	"path"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

// Mount, Hook and ContainerEdits are CDI containerEdits (cdiVersion 0.6.0).
type Mount struct {
	HostPath      string   `json:"hostPath"`
	ContainerPath string   `json:"containerPath"`
	Type          string   `json:"type,omitempty"`
	Options       []string `json:"options,omitempty"`
}

type Hook struct {
	HookName string   `json:"hookName"`
	Path     string   `json:"path"`
	Args     []string `json:"args,omitempty"`
}

type ContainerEdits struct {
	Env    []string `json:"env,omitempty"`
	Mounts []Mount  `json:"mounts,omitempty"`
	Hooks  []Hook   `json:"hooks,omitempty"`
}

var (
	bindOptions  = []string{"bind", "ro", "nosuid", "nodev", "rprivate"}
	rbindOptions = []string{"rbind", "ro", "nosuid", "nodev", "rprivate"}
)

// Planner turns the scheduler's extended-resource claim into CDI edits.
type Planner struct {
	Catalogue Catalogue
	Hosts     HostModules
	PVCs      PVCs
	NodeName  string
	HookPath  string
}

type member struct {
	value resolve.ModuleValue
	raw   string
}

// Plan maps each of the claim's requests for this driver to the CDI edits
// its container gets. A container's edits all go on its lowest-index
// request, and its other requests map to nil, so overlapping closures never
// bind a file twice.
func (p *Planner) Plan(claim *resourceapi.ResourceClaim, pod *corev1.Pod) (map[string]*ContainerEdits, error) {
	st := pod.Status.ExtendedResourceClaimStatus
	if st == nil || st.ResourceClaimName != claim.Name {
		return nil, fmt.Errorf("pod %s/%s has no extended resource claim status for claim %s yet", pod.Namespace, pod.Name, claim.Name)
	}
	if claim.Status.Allocation == nil {
		return nil, fmt.Errorf("claim %s/%s is not allocated", claim.Namespace, claim.Name)
	}
	type mapping struct{ container, resource string }
	byRequest := map[string]mapping{}
	for _, m := range st.RequestMappings {
		byRequest[m.RequestName] = mapping{m.ContainerName, m.ResourceName}
	}
	byDevice := map[string]*v1.Module{}
	for _, m := range p.Catalogue.List() {
		byDevice[resolve.DeviceName(m.Name)] = &m
	}

	requests := map[string][]string{}
	modules := map[string][]*v1.Module{}
	for _, r := range claim.Status.Allocation.Devices.Results {
		if r.Driver != v1.DriverName {
			continue
		}
		mp, ok := byRequest[r.Request]
		if !ok {
			return nil, fmt.Errorf("request %s is not mapped to a container", r.Request)
		}
		m, ok := byDevice[r.Device]
		if !ok || m.Status.Resolved == nil {
			return nil, fmt.Errorf("device %s is not a resolved Module (deleted or changed after scheduling); recreate the pod", r.Device)
		}
		if fam, ok := strings.CutPrefix(mp.resource, v1.FamilyResourcePrefix); ok {
			ann := pod.Annotations[resolve.ResolvedAnnotationKey(fam)]
			if !strings.HasPrefix(ann, m.Name+";") {
				admitted, _, _ := strings.Cut(ann, ";")
				return nil, fmt.Errorf("default for family %s changed from %s to %s after admission; recreate the pod", fam, admitted, m.Name)
			}
		}
		requests[mp.container] = append(requests[mp.container], r.Request)
		modules[mp.container] = append(modules[mp.container], m)
	}

	out := map[string]*ContainerEdits{}
	for c, reqs := range requests {
		// container-<i>-request-<j>: within one container only j varies, so
		// shorter sorts first gives numeric order.
		sort.Slice(reqs, func(i, j int) bool {
			if len(reqs[i]) != len(reqs[j]) {
				return len(reqs[i]) < len(reqs[j])
			}
			return reqs[i] < reqs[j]
		})
		e, err := p.edits(modules[c])
		if err != nil {
			return nil, fmt.Errorf("container %s: %w", c, err)
		}
		out[reqs[0]] = e
		for _, r := range reqs[1:] {
			out[r] = nil
		}
	}
	return out, nil
}

// edits builds one container's edits from the union of its modules'
// closures. A member in several closures keeps its highest rank.
func (p *Planner) edits(mods []*v1.Module) (*ContainerEdits, error) {
	members := map[string]member{}
	for _, m := range mods {
		for _, n := range m.Status.Resolved.Modules {
			dep, ok := p.Catalogue.Module(n)
			if !ok {
				return nil, fmt.Errorf("module %s (needed by %s) is not in the catalogue", n, m.Name)
			}
			raw := m.Status.Resolved.Annotations[resolve.ResolvedAnnotationKey(dep.Spec.Family)]
			mv, err := resolve.ParseValue(raw)
			if err != nil {
				return nil, fmt.Errorf("module %s: %w", m.Name, err)
			}
			if cur, ok := members[n]; !ok || mv.Rank > cur.value.Rank {
				members[n] = member{mv, raw}
			}
		}
	}
	ordered := make([]member, 0, len(members))
	for _, mb := range members {
		ordered = append(ordered, mb)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i].value, ordered[j].value
		if a.Rank != b.Rank {
			return a.Rank > b.Rank
		}
		return a.Name < b.Name
	})

	e := &ContainerEdits{}
	var hookArgs []string
	for _, mb := range ordered {
		m := mb.value
		e.Env = append(e.Env, resolve.MarkerKey(m.Name)+"="+mb.raw)
		switch {
		case m.Src == "host":
			r, ok := p.Hosts.HostModule(m.Name)
			if !ok || !r.Ready {
				why := "not discovered yet"
				if ok {
					why = r.Message
				}
				return nil, fmt.Errorf("host module %s is not available on node %s: %s", m.Name, p.NodeName, why)
			}
			for _, l := range r.Lib {
				e.Mounts = append(e.Mounts, Mount{HostPath: l.Path, ContainerPath: path.Join(m.Mount, "lib", l.Soname), Type: "bind", Options: bindOptions})
			}
			for _, l := range r.Deps {
				e.Mounts = append(e.Mounts, Mount{HostPath: l.Path, ContainerPath: path.Join(m.Mount, "deps", l.Soname), Type: "bind", Options: bindOptions})
			}
			if len(r.Deps) > 0 {
				hookArgs = append(hookArgs, "--deps", path.Join(m.Mount, "deps"))
			}
			if r.GlibcRequired != "" {
				hookArgs = append(hookArgs, "--glibc", m.Name+"="+r.GlibcRequired)
			}
		case strings.HasPrefix(m.Src, "pvc:"):
			claim, sub, _ := strings.Cut(strings.TrimPrefix(m.Src, "pvc:"), "/")
			src, err := p.PVCs.HostPath(claim, sub)
			if err != nil {
				return nil, err
			}
			e.Mounts = append(e.Mounts, Mount{HostPath: src, ContainerPath: m.Mount, Type: "bind", Options: rbindOptions})
		}
	}
	if len(hookArgs) > 0 {
		e.Hooks = []Hook{{HookName: "createContainer", Path: p.HookPath, Args: append([]string{"module-hook"}, hookArgs...)}}
	}
	return e, nil
}
