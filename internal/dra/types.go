// Package dra is the module system's DRA kubelet plugin. Every Module is a
// shareable device that containers request as an extended resource; at
// prepare the plugin writes CDI edits that bind host and PVC files, add
// module-hook and set a BRICS_MODULE_* marker for each module.
package dra

import (
	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/discovery"
)

// Catalogue reads Modules, normally from an informer cache.
type Catalogue interface {
	Module(name string) (*v1.Module, bool)
	List() []v1.Module
}

// HostModules returns this node's latest discovery result for a module.
type HostModules interface {
	HostModule(name string) (discovery.Result, bool)
}

// PVCs locates files inside the claims module-nri mounts.
type PVCs interface {
	HostPath(claim, subPath string) (string, error)
	Mounted(claim string) bool
}
