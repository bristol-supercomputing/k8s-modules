// Package controller reconciles Modules into status.resolved.
package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	"github.com/bristol-supercomputing/k8s-modules/internal/resolve"
)

// ErrPVCInvalid reports a pvc-sourced module whose claim is unusable.
type ErrPVCInvalid struct{ Module, Namespace, Claim, Problem string }

func (e ErrPVCInvalid) Error() string {
	return fmt.Sprintf("module %q needs PVC \"%s/%s\", which %s", e.Module, e.Namespace, e.Claim, e.Problem)
}

type ModuleReconciler struct {
	client.Client
	ModulesNamespace string
}

// CacheOptions limits the PVC informer to the modules namespace, which is
// all the controller's Role allows.
func CacheOptions(modulesNamespace string) cache.Options {
	return cache.Options{ByObject: map[client.Object]cache.ByObject{
		&corev1.PersistentVolumeClaim{}: {Namespaces: map[string]cache.Config{modulesNamespace: {}}},
	}}
}

func (r *ModuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("module").
		// Any create, delete or spec change can alter any closure; status
		// writes don't bump generation and are filtered out.
		Watches(&v1.Module{}, handler.EnqueueRequestsFromMapFunc(r.enqueueAll),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&corev1.PersistentVolumeClaim{}, handler.EnqueueRequestsFromMapFunc(r.enqueueAll)).
		Complete(r)
}

func (r *ModuleReconciler) enqueueAll(ctx context.Context, _ client.Object) []reconcile.Request {
	var list v1.ModuleList
	if err := r.List(ctx, &list); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "listing modules")
		return nil
	}
	reqs := make([]reconcile.Request, len(list.Items))
	for i := range list.Items {
		reqs[i] = reconcile.Request{NamespacedName: types.NamespacedName{Name: list.Items[i].Name}}
	}
	return reqs
}

func (r *ModuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var list v1.ModuleList
	if err := r.List(ctx, &list); err != nil {
		return ctrl.Result{}, err
	}
	catalogue := make(map[string]*v1.Module, len(list.Items))
	for i := range list.Items {
		catalogue[list.Items[i].Name] = &list.Items[i]
	}
	m, ok := catalogue[req.Name]
	if !ok {
		return ctrl.Result{}, nil
	}

	resolved, err := resolve.Resolve(m.Name, catalogue)
	if err == nil {
		err = r.checkPVCs(ctx, resolved, catalogue)
		var invalid ErrPVCInvalid
		if err != nil && !errors.As(err, &invalid) {
			return ctrl.Result{}, err
		}
	}

	desired := m.DeepCopy()
	setStatus(&desired.Status, m.Generation, resolved, err)
	if equality.Semantic.DeepEqual(m.Status, desired.Status) {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, r.Status().Patch(ctx, desired, client.MergeFrom(m))
}

func (r *ModuleReconciler) checkPVCs(ctx context.Context, resolved *v1.Resolved, catalogue map[string]*v1.Module) error {
	for _, name := range resolved.Modules {
		src := catalogue[name].Spec.Source.PVC
		if src == nil {
			continue
		}
		var pvc corev1.PersistentVolumeClaim
		err := r.Get(ctx, types.NamespacedName{Namespace: r.ModulesNamespace, Name: src.ClaimName}, &pvc)
		if apierrors.IsNotFound(err) {
			return ErrPVCInvalid{Module: name, Namespace: r.ModulesNamespace, Claim: src.ClaimName, Problem: "does not exist"}
		}
		if err != nil {
			return err
		}
		if !slices.Contains(pvc.Spec.AccessModes, corev1.ReadOnlyMany) {
			return ErrPVCInvalid{Module: name, Namespace: r.ModulesNamespace, Claim: src.ClaimName, Problem: "does not have the ReadOnlyMany access mode"}
		}
	}
	return nil
}

func setStatus(st *v1.ModuleStatus, gen int64, resolved *v1.Resolved, err error) {
	res := metav1.Condition{Type: v1.ConditionResolved, ObservedGeneration: gen}
	cf := metav1.Condition{Type: v1.ConditionConflictFree, ObservedGeneration: gen,
		Status: metav1.ConditionUnknown, Reason: "NotEvaluated",
		Message: "conflicts are checked once dependencies resolve"}

	switch {
	case err == nil:
		res.Status, res.Reason = metav1.ConditionTrue, "Resolved"
		res.Message = fmt.Sprintf("resolved %d modules", len(resolved.Modules))
		cf.Status, cf.Reason, cf.Message = metav1.ConditionTrue, "NoConflicts", "no family or env conflicts"
	case errors.As(err, new(resolve.ErrDependencyNotFound)):
		res.Status, res.Reason = metav1.ConditionFalse, "DependencyNotFound"
	case errors.As(err, new(resolve.ErrDependencyCycle)):
		res.Status, res.Reason = metav1.ConditionFalse, "DependencyCycle"
	case errors.As(err, new(resolve.ErrFamilyConflict)):
		res.Status, res.Reason = metav1.ConditionFalse, "Conflict"
		cf.Status, cf.Reason, cf.Message = metav1.ConditionFalse, "FamilyConflict", err.Error()
	case errors.As(err, new(resolve.ErrEnvConflict)):
		res.Status, res.Reason = metav1.ConditionFalse, "Conflict"
		cf.Status, cf.Reason, cf.Message = metav1.ConditionFalse, "EnvConflict", err.Error()
	case errors.As(err, new(ErrPVCInvalid)):
		res.Status, res.Reason = metav1.ConditionFalse, "PVCInvalid"
	default:
		res.Status, res.Reason = metav1.ConditionFalse, "Error"
	}
	if err != nil {
		res.Message = err.Error()
		resolved = nil
	}
	st.Resolved = resolved
	meta.SetStatusCondition(&st.Conditions, res)
	meta.SetStatusCondition(&st.Conditions, cf)
}
