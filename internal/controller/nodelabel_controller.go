package controller

import (
	"context"
	"maps"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
)

// NodeLabelReconciler labels each node modules.brics.ac.uk/host-<family>
// for every host module its ModuleNodeReport shows ready.
type NodeLabelReconciler struct {
	client.Client
}

func (r *NodeLabelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("nodelabels").
		// Nodes change constantly (heartbeats); only label edits matter.
		For(&corev1.Node{}, builder.WithPredicates(predicate.LabelChangedPredicate{})).
		Watches(&v1.ModuleNodeReport{}, handler.EnqueueRequestsFromMapFunc(
			func(_ context.Context, o client.Object) []reconcile.Request {
				return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: o.GetName()}}}
			})).
		Complete(r)
}

func (r *NodeLabelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	node := &corev1.Node{}
	if err := r.Get(ctx, req.NamespacedName, node); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	rep := &v1.ModuleNodeReport{}
	if err := r.Get(ctx, req.NamespacedName, rep); apierrors.IsNotFound(err) {
		rep = nil
	} else if err != nil {
		return ctrl.Result{}, err
	}
	labels, changed := HostLabels(node.Labels, rep)
	if !changed {
		return ctrl.Result{}, nil
	}
	orig := node.DeepCopy()
	node.Labels = labels
	return ctrl.Result{}, r.Patch(ctx, node, client.MergeFrom(orig))
}

// HostLabels returns current with its host-* labels replaced by those the
// report implies. Other labels are untouched; current is not modified.
func HostLabels(current map[string]string, rep *v1.ModuleNodeReport) (map[string]string, bool) {
	want := map[string]bool{}
	if rep != nil {
		for _, m := range rep.Status.Modules {
			if m.Ready {
				want[v1.LabelHostPrefix+m.Family] = true
			}
		}
	}
	out := maps.Clone(current)
	if out == nil {
		out = map[string]string{}
	}
	changed := false
	for k := range out {
		if strings.HasPrefix(k, v1.LabelHostPrefix) && !want[k] {
			delete(out, k)
			changed = true
		}
	}
	for k := range want {
		if out[k] != "true" {
			out[k] = "true"
			changed = true
		}
	}
	return out, changed
}
