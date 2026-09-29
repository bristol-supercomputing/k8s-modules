package nri

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
)

// CacheCatalogue reads Modules from an informer-backed reader, so lookups
// are in-memory and fit containerd's per-request timeout.
type CacheCatalogue struct{ Reader client.Reader }

func (c CacheCatalogue) Module(name string) (*v1.Module, bool) {
	m := &v1.Module{}
	if err := c.Reader.Get(context.Background(), client.ObjectKey{Name: name}, m); err != nil {
		return nil, false
	}
	return m, true
}

// List returns every Module in the cache; nil if the cache can't be read.
func (c CacheCatalogue) List() []v1.Module {
	var l v1.ModuleList
	if err := c.Reader.List(context.Background(), &l); err != nil {
		return nil
	}
	return l.Items
}
