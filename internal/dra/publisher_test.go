package dra

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/dynamic-resource-allocation/resourceslice"
)

func TestPublisherRepublishesOnTrigger(t *testing.T) {
	c := catalogue(t, mod("x", "1", img))
	var mu sync.Mutex
	var counts []int
	pub := &Publisher{Pool: "n1", Catalogue: c, Hosts: fakeHosts{}, PVCs: fakePVCs{}, Debounce: 10 * time.Millisecond, Log: logr.Discard(),
		Publish: func(_ context.Context, r resourceslice.DriverResources) error {
			mu.Lock()
			defer mu.Unlock()
			counts = append(counts, len(r.Pools["n1"].Slices[0].Devices))
			return nil
		}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	trigger := make(chan struct{}, 1)
	go pub.Run(ctx, trigger)

	waitCount := func(n int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			mu.Lock()
			got := len(counts)
			mu.Unlock()
			if got >= n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("published %d times, want %d", got, n)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitCount(1)
	y := mod("y", "1", img)
	for k, v := range catalogue(t, y) {
		c[k] = v
	}
	trigger <- struct{}{}
	waitCount(2)
	mu.Lock()
	defer mu.Unlock()
	if counts[0] != 1 || counts[1] != 2 {
		t.Errorf("device counts = %v", counts)
	}
}
