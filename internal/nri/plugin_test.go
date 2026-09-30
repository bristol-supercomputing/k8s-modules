package nri

import (
	"context"
	"strings"
	"testing"

	api "github.com/containerd/nri/pkg/api"
	"github.com/go-logr/logr"
)

func TestCreateContainerRefusesLegacyPod(t *testing.T) {
	p := &Plugin{Log: logr.Discard()}
	pod := &api.PodSandbox{Namespace: "ns", Name: "old",
		Annotations: map[string]string{"modules.isambard.ac.uk/containers": "main"}}
	adj, upd, err := p.CreateContainer(context.Background(), pod, ctr("main", marker("x", "1", 0)))
	if err == nil {
		t.Fatalf("CreateContainer = %v, %v, nil; want error", adj, upd)
	}
	if !strings.Contains(err.Error(), "older module system") ||
		!strings.Contains(err.Error(), "modules.isambard.ac.uk/containers") {
		t.Errorf("error = %q", err)
	}
	// A container without markers in a legacy pod is refused too: the old
	// system applied its modules without markers.
	if _, _, err := p.CreateContainer(context.Background(), pod, ctr("main")); err == nil {
		t.Error("legacy pod container without markers: want error")
	}
}

func TestCreateContainerCurrentPod(t *testing.T) {
	p := &Plugin{Log: logr.Discard()}
	pod := &api.PodSandbox{Namespace: "ns", Name: "new",
		Annotations: map[string]string{"other": "x"}}
	adj, _, err := p.CreateContainer(context.Background(), pod, ctr("main"))
	if err != nil || adj != nil {
		t.Fatalf("no markers: adj=%v err=%v; want nil, nil", adj, err)
	}
	adj, _, err = p.CreateContainer(context.Background(), pod, ctr("main", marker("x", "1", 0)))
	if err != nil || adj == nil {
		t.Fatalf("markers: adj=%v err=%v; want adjustment", adj, err)
	}
	if envOf(adj)["BRICS_MODULES"] != "x-1" {
		t.Errorf("BRICS_MODULES = %q", envOf(adj)["BRICS_MODULES"])
	}
}
