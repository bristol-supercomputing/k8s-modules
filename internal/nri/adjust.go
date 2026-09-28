package nri

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	api "github.com/containerd/nri/pkg/api"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	"github.com/bristol-supercomputing/k8s-modules/internal/discovery"
)

// Catalogue looks up Modules, normally from an informer cache.
type Catalogue interface {
	Module(name string) (*v1.Module, bool)
}

// HostModules returns this node's latest discovery result for a module.
type HostModules interface {
	HostModule(name string) (discovery.Result, bool)
}

// PVCs locates the host path of a claim the DaemonSet mounts.
type PVCs interface {
	HostPath(claim string) (string, error)
}

// Adjuster turns a pod's module annotations into a container adjustment.
type Adjuster struct {
	Catalogue Catalogue
	Hosts     HostModules
	PVCs      PVCs
	NodeName  string
	HookPath  string
}

var (
	bindOptions  = []string{"bind", "ro", "nosuid", "nodev", "rprivate"}
	rbindOptions = []string{"rbind", "ro", "nosuid", "nodev", "rprivate"}
	maskOptions  = []string{"ro", "nosuid", "nodev", "noexec", "size=0"}
)

// Adjust returns nil for containers that load no modules. Every error
// fails container creation, so pods never start without their modules.
func (a *Adjuster) Adjust(pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, error) {
	anns := pod.GetAnnotations()
	var keys []string
	for k := range anns {
		if strings.HasPrefix(k, v1.AnnotationResolvedPrefix) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 || !slices.Contains(strings.Split(anns[v1.AnnotationContainers], ","), ctr.GetName()) {
		return nil, nil
	}
	sort.Strings(keys)

	mods := make([]ModuleValue, 0, len(keys))
	for _, k := range keys {
		v := anns[k]
		name, _, _ := strings.Cut(v, ";")
		m, ok := a.Catalogue.Module(name)
		if !ok || m.Status.Resolved == nil || m.Status.Resolved.Annotations[k] != v {
			return nil, fmt.Errorf("module annotation %s=%s does not match the Module catalogue (not found, not resolved, or changed); the pod must be recreated", k, v)
		}
		mv, err := ParseValue(v)
		if err != nil {
			return nil, err
		}
		mods = append(mods, mv)
	}
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].Rank != mods[j].Rank {
			return mods[i].Rank > mods[j].Rank
		}
		return mods[i].Name < mods[j].Name
	})

	adj := &api.ContainerAdjustment{}
	var libDirs, names, hookArgs []string
	for _, m := range mods {
		names = append(names, m.Name)
		libDirs = append(libDirs, m.Lib...)
		for _, p := range m.Mask {
			adj.AddMount(&api.Mount{Destination: p, Type: "tmpfs", Source: "tmpfs", Options: maskOptions})
		}
		switch {
		case m.Src == "host":
			r, ok := a.Hosts.HostModule(m.Name)
			if !ok || !r.Ready {
				why := "not discovered yet"
				if ok {
					why = r.Message
				}
				return nil, fmt.Errorf("host module %s is not available on node %s: %s", m.Name, a.NodeName, why)
			}
			for _, l := range r.Lib {
				adj.AddMount(&api.Mount{Destination: path.Join(m.Mount, "lib", l.Soname), Type: "bind", Source: l.Path, Options: bindOptions})
			}
			for _, l := range r.Deps {
				adj.AddMount(&api.Mount{Destination: path.Join(m.Mount, "deps", l.Soname), Type: "bind", Source: l.Path, Options: bindOptions})
			}
			if len(r.Deps) > 0 {
				hookArgs = append(hookArgs, "--deps", path.Join(m.Mount, "deps"))
			}
			if r.GlibcRequired != "" {
				hookArgs = append(hookArgs, "--glibc", m.Name+"="+r.GlibcRequired)
			}
		case strings.HasPrefix(m.Src, "pvc:"):
			claim, sub, _ := strings.Cut(strings.TrimPrefix(m.Src, "pvc:"), "/")
			hp, err := a.PVCs.HostPath(claim)
			if err != nil {
				return nil, err
			}
			adj.AddMount(&api.Mount{Destination: m.Mount, Type: "bind", Source: path.Join(hp, sub), Options: rbindOptions})
		}
	}
	if len(libDirs) > 0 {
		adj.AddEnv("LD_LIBRARY_PATH", PrependLibraryPath(ctr.GetEnv(), libDirs))
	}
	adj.AddEnv("BRICS_MODULES", strings.Join(names, ":"))
	if len(hookArgs) > 0 {
		adj.AddHooks(&api.Hooks{CreateContainer: []*api.Hook{{
			Path: a.HookPath,
			Args: append([]string{"module-hook"}, hookArgs...),
		}}})
	}
	return adj, nil
}
