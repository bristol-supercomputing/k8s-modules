package nri

import (
	"context"
	"fmt"

	api "github.com/containerd/nri/pkg/api"
	"github.com/go-logr/logr"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
)

// legacyContainersAnnotation marks a pod admitted by the pre-DRA module
// system, which applied host and PVC modules from annotations. The current
// system applies them only through DRA claims, which such a pod lacks, so its
// containers are refused rather than started without their modules.
const legacyContainersAnnotation = "modules.isambard.ac.uk/containers"

// Plugin is the NRI stub handler.
type Plugin struct {
	Log logr.Logger
}

func (p *Plugin) CreateContainer(_ context.Context, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	id := pod.GetNamespace() + "/" + pod.GetName() + "/" + ctr.GetName()
	if _, ok := pod.GetAnnotations()[legacyContainersAnnotation]; ok {
		err := fmt.Errorf("pod was admitted by an older module system (annotation %s); recreate the pod", legacyContainersAnnotation)
		p.Log.Info("refusing container", "container", id, "reason", err.Error())
		return nil, nil, fmt.Errorf("modules: %w", err)
	}
	adj, err := Adjust(ctr)
	if err != nil {
		p.Log.Info("refusing container", "container", id, "reason", err.Error())
		return nil, nil, fmt.Errorf("modules: %w", err)
	}
	if adj != nil {
		p.Log.Info("applied modules", "container", id, "modules", envValue(adj, v1.EnvModules))
	}
	return adj, nil, nil
}

func envValue(adj *api.ContainerAdjustment, key string) string {
	for _, kv := range adj.GetEnv() {
		if kv.Key == key {
			return kv.Value
		}
	}
	return ""
}
