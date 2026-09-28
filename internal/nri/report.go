package nri

import (
	"context"
	"sort"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/discovery"
)

// BuildReport lists every host Module's result, sorted by module name.
func BuildReport(modules []v1.Module, results map[string]discovery.Result) []v1.HostModuleReport {
	var out []v1.HostModuleReport
	for _, m := range modules {
		if m.Spec.Source.Host == nil {
			continue
		}
		r := results[m.Name]
		out = append(out, v1.HostModuleReport{
			Module: m.Name, Family: m.Spec.Family, Ready: r.Ready,
			ClosureHash: r.Hash, GlibcRequired: r.GlibcRequired,
			Lib: files(r.Lib), Deps: files(r.Deps), Missing: r.Missing, Message: r.Message,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Module < out[j].Module })
	return out
}

func files(ls []discovery.Library) []v1.LibraryFile {
	var out []v1.LibraryFile
	for _, l := range ls {
		out = append(out, v1.LibraryFile{Soname: l.Soname, Path: l.Path})
	}
	return out
}

// Discoverer runs discovery for every host Module and publishes the result
// to the in-memory state and to the node's ModuleNodeReport.
type Discoverer struct {
	HostRoot, Arch, NodeName string
	Reader                   client.Reader // informer cache, for Modules
	Writer                   client.Client // direct client, for the report
	State                    *HostState
	Log                      logr.Logger
}

func (d *Discoverer) RunOnce(ctx context.Context) error {
	var list v1.ModuleList
	if err := d.Reader.List(ctx, &list); err != nil {
		return err
	}
	host, hostErr := discovery.NewHost(d.HostRoot, d.Arch)
	results := map[string]discovery.Result{}
	for _, m := range list.Items {
		h := m.Spec.Source.Host
		if h == nil {
			continue
		}
		if hostErr != nil {
			results[m.Name] = discovery.Result{Message: hostErr.Error()}
			continue
		}
		results[m.Name] = host.Discover(h.Discover.Roots, h.PreferHost)
	}
	d.State.Set(results)
	return d.writeReport(ctx, BuildReport(list.Items, results))
}

func (d *Discoverer) writeReport(ctx context.Context, modules []v1.HostModuleReport) error {
	rep := &v1.ModuleNodeReport{}
	err := d.Writer.Get(ctx, client.ObjectKey{Name: d.NodeName}, rep)
	if apierrors.IsNotFound(err) {
		node := &corev1.Node{}
		if err := d.Writer.Get(ctx, client.ObjectKey{Name: d.NodeName}, node); err != nil {
			return err
		}
		rep = &v1.ModuleNodeReport{ObjectMeta: metav1.ObjectMeta{
			Name:            d.NodeName,
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Node", Name: node.Name, UID: node.UID}},
		}}
		if err := d.Writer.Create(ctx, rep); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if rep.Status.DiscoveredAt != nil && equality.Semantic.DeepEqual(rep.Status.Modules, modules) {
		return nil
	}
	now := metav1.Now()
	rep.Status = v1.ModuleNodeReportStatus{DiscoveredAt: &now, Modules: modules}
	return d.Writer.Status().Update(ctx, rep)
}

// Run repeats discovery every interval and whenever trigger fires.
func (d *Discoverer) Run(ctx context.Context, interval time.Duration, trigger <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-trigger:
		}
		if err := d.RunOnce(ctx); err != nil {
			d.Log.Error(err, "discovery")
		}
	}
}
