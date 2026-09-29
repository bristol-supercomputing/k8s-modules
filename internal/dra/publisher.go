package dra

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/dynamic-resource-allocation/resourceslice"
)

// Publisher republishes this node's devices whenever trigger fires:
// Module changes and every discovery run.
type Publisher struct {
	Pool      string
	Catalogue Catalogue
	Hosts     HostModules
	PVCs      PVCs
	Publish   func(context.Context, resourceslice.DriverResources) error
	Debounce  time.Duration
	Log       logr.Logger
}

func (p *Publisher) Run(ctx context.Context, trigger <-chan struct{}) {
	for {
		devs := Devices(p.Catalogue, p.Hosts, p.PVCs)
		if err := p.Publish(ctx, Resources(p.Pool, devs)); err != nil {
			p.Log.Error(err, "publishing ResourceSlices")
		} else {
			p.Log.V(1).Info("published devices", "count", len(devs))
		}
		select {
		case <-ctx.Done():
			return
		case <-trigger:
		}
		// Coalesce bursts: a Module change also starts a discovery run.
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.Debounce):
		}
	drain:
		for {
			select {
			case <-trigger:
			default:
				break drain
			}
		}
	}
}
