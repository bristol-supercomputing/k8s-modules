package dra

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
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

func TestUnpublishedChanges(t *testing.T) {
	prev := map[string]string{"a": "r1", "b": "r2", "gone": "r3"}
	cur := map[string]string{"a": "r1", "b": "r2b", "c": "r4"}
	published := map[string]bool{"x": true}
	down, up := unpublishedChanges(prev, cur, func(n string) bool { return n == "gone" || published[n] })
	if fmt.Sprint(down) != "[b c]" || fmt.Sprint(up) != "[gone]" {
		t.Errorf("down=%v up=%v; want [b c] [gone]", down, up)
	}
	down, up = unpublishedChanges(cur, cur, func(string) bool { return false })
	if len(down) != 0 || len(up) != 0 {
		t.Errorf("unchanged: down=%v up=%v", down, up)
	}
	// A Module deleted while unpublished is not reported as published.
	_, up = unpublishedChanges(map[string]string{"del": "r"}, nil, func(string) bool { return false })
	if len(up) != 0 {
		t.Errorf("deleted module reported published: %v", up)
	}
}

func TestPublisherLogsUnpublishedOnce(t *testing.T) {
	c := catalogue(t, mod("fab", "host", hst), mod("x", "1", img))
	hosts := fakeHosts{}
	var lines []string
	log := funcr.New(func(prefix, args string) { lines = append(lines, args) }, funcr.Options{})
	pub := &Publisher{Pool: "n1", Catalogue: c, Hosts: hosts, PVCs: fakePVCs{}, Log: log,
		Publish: func(context.Context, resourceslice.DriverResources) error { return nil }}

	pub.publish(context.Background())
	pub.publish(context.Background())
	if len(lines) != 1 || !strings.Contains(lines[0], `"module"="fab-host"`) ||
		!strings.Contains(lines[0], "host module fab-host is not ready on this node") {
		t.Fatalf("after two publishes, logs = %q; want one unpublished line for fab-host", lines)
	}
	hosts["fab-host"] = hostResult()
	pub.publish(context.Background())
	pub.publish(context.Background())
	if len(lines) != 2 || !strings.Contains(lines[1], `"module"="fab-host"`) || !strings.Contains(lines[1], "published") {
		t.Errorf("after recovery, logs = %q; want one published line for fab-host", lines)
	}
}
