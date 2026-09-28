package nri

import (
	"sync/atomic"

	"github.com/bristol-supercomputing/k8s-modules/internal/discovery"
)

// HostState holds the latest discovery results. Discovery replaces the
// whole map at once, so CreateContainer never sees a half-updated state.
type HostState struct {
	results atomic.Pointer[map[string]discovery.Result]
}

func (s *HostState) Set(m map[string]discovery.Result) { s.results.Store(&m) }

func (s *HostState) HostModule(name string) (discovery.Result, bool) {
	m := s.results.Load()
	if m == nil {
		return discovery.Result{}, false
	}
	r, ok := (*m)[name]
	return r, ok
}
