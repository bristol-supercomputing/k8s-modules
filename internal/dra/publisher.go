package dra

import (
	"context"
	"sort"
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

	// unpublished holds the Modules left out of the last publish and why,
	// so only changes are logged.
	unpublished map[string]string
}

func (p *Publisher) Run(ctx context.Context, trigger <-chan struct{}) {
	for {
		p.publish(ctx)
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

// publish publishes this node's devices once and logs each Module that
// stopped or started being published since the last call.
func (p *Publisher) publish(ctx context.Context) {
	devs, unpublished := devicesAndUnpublished(p.Catalogue, p.Hosts, p.PVCs)
	down, up := unpublishedChanges(p.unpublished, unpublished, func(n string) bool {
		_, ok := p.Catalogue.Module(n)
		return ok
	})
	for _, n := range down {
		p.Log.Info("module not published on this node", "module", n, "reason", unpublished[n])
	}
	for _, n := range up {
		p.Log.Info("module published on this node", "module", n)
	}
	p.unpublished = unpublished
	if err := p.Publish(ctx, Resources(p.Pool, devs)); err != nil {
		p.Log.Error(err, "publishing ResourceSlices")
	} else {
		p.Log.V(1).Info("published devices", "count", len(devs))
	}
}

// unpublishedChanges compares two sets of unpublished Modules (name to
// reason). down lists the Modules newly unpublished or unpublished for a new
// reason; up lists those no longer unpublished that still exist.
func unpublishedChanges(prev, cur map[string]string, exists func(string) bool) (down, up []string) {
	for n, why := range cur {
		if old, ok := prev[n]; !ok || old != why {
			down = append(down, n)
		}
	}
	for n := range prev {
		if _, ok := cur[n]; !ok && exists(n) {
			up = append(up, n)
		}
	}
	sort.Strings(down)
	sort.Strings(up)
	return down, up
}
