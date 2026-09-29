// Package nri is the module system's NRI plugin: at container creation it
// applies the modules the DRA driver's CDI markers name, and it runs
// host-module discovery.
package nri

import (
	"fmt"
	"sort"
	"strings"

	api "github.com/containerd/nri/pkg/api"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

var maskOptions = []string{"ro", "nosuid", "nodev", "noexec", "size=0"}

// Adjust applies the modules named by a container's BRICS_MODULE_* markers,
// which the DRA driver's CDI edits set, and returns nil for containers
// without markers. A user can set markers too, so Adjust only does what a
// container may do to itself: order LD_LIBRARY_PATH, mask paths and set
// BRICS_MODULES. Host and PVC binds and module-hook come only from CDI. A
// malformed marker fails container creation.
func Adjust(ctr *api.Container) (*api.ContainerAdjustment, error) {
	byName := map[string]resolve.ModuleValue{}
	for _, e := range ctr.GetEnv() {
		k, v, ok := strings.Cut(e, "=")
		if !ok || !strings.HasPrefix(k, v1.EnvMarkerPrefix) {
			continue
		}
		mv, err := resolve.ParseValue(v)
		if err != nil {
			return nil, fmt.Errorf("marker %s: %w", k, err)
		}
		if cur, ok := byName[mv.Name]; !ok || mv.Rank > cur.Rank {
			byName[mv.Name] = mv
		}
	}
	if len(byName) == 0 {
		return nil, nil
	}
	mods := make([]resolve.ModuleValue, 0, len(byName))
	for _, mv := range byName {
		mods = append(mods, mv)
	}
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].Rank != mods[j].Rank {
			return mods[i].Rank > mods[j].Rank
		}
		return mods[i].Name < mods[j].Name
	})

	adj := &api.ContainerAdjustment{}
	var libDirs, names []string
	for _, m := range mods {
		names = append(names, m.Name)
		libDirs = append(libDirs, m.Lib...)
		for _, p := range m.Mask {
			adj.AddMount(&api.Mount{Destination: p, Type: "tmpfs", Source: "tmpfs", Options: maskOptions})
		}
	}
	if len(libDirs) > 0 {
		adj.AddEnv(v1.EnvLDLibraryPath, PrependLibraryPath(ctr.GetEnv(), libDirs))
	}
	adj.AddEnv(v1.EnvModules, strings.Join(names, ":"))
	return adj, nil
}
