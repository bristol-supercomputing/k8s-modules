package nri

import (
	"context"
	"fmt"

	api "github.com/containerd/nri/pkg/api"
	"github.com/go-logr/logr"
)

// Plugin is the NRI stub handler.
type Plugin struct {
	Adjuster *Adjuster
	Log      logr.Logger
}

func (p *Plugin) CreateContainer(_ context.Context, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	adj, err := p.Adjuster.Adjust(pod, ctr)
	id := pod.GetNamespace() + "/" + pod.GetName() + "/" + ctr.GetName()
	if err != nil {
		p.Log.Info("refusing container", "container", id, "reason", err.Error())
		return nil, nil, fmt.Errorf("modules: %w", err)
	}
	if adj != nil {
		p.Log.Info("applied modules", "container", id, "modules", envValue(adj, "BRICS_MODULES"))
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
