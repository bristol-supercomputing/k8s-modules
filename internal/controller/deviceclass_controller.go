package controller

import (
	"context"
	"fmt"
	"sort"

	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

// DeviceClassReconciler keeps one DeviceClass per resolved Module and one
// per family, so containers can request modules as extended resources.
type DeviceClassReconciler struct {
	client.Client
}

// All classes are reconciled together under one key.
var allClasses = reconcile.Request{NamespacedName: types.NamespacedName{Name: "deviceclasses"}}

func (r *DeviceClassReconciler) SetupWithManager(mgr ctrl.Manager) error {
	all := handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []reconcile.Request {
		return []reconcile.Request{allClasses}
	})
	return ctrl.NewControllerManagedBy(mgr).
		Named("deviceclass").
		Watches(&v1.Module{}, all).
		Watches(&resourcev1.DeviceClass{}, all, builder.WithPredicates(predicate.NewPredicateFuncs(func(o client.Object) bool {
			return o.GetLabels()[v1.LabelManaged] == "true"
		}))).
		Complete(r)
}

func (r *DeviceClassReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	var mods v1.ModuleList
	if err := r.List(ctx, &mods); err != nil {
		return ctrl.Result{}, err
	}
	want := map[string]resourcev1.DeviceClass{}
	for _, dc := range DesiredDeviceClasses(mods.Items) {
		want[dc.Name] = dc
	}
	var have resourcev1.DeviceClassList
	if err := r.List(ctx, &have, client.MatchingLabels{v1.LabelManaged: "true"}); err != nil {
		return ctrl.Result{}, err
	}
	existing := map[string]*resourcev1.DeviceClass{}
	for i := range have.Items {
		dc := &have.Items[i]
		if _, ok := want[dc.Name]; !ok {
			if err := r.Delete(ctx, dc); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			continue
		}
		existing[dc.Name] = dc
	}
	for name, dc := range want {
		cur, ok := existing[name]
		if !ok {
			if err := r.Create(ctx, &dc); err != nil && !apierrors.IsAlreadyExists(err) {
				return ctrl.Result{}, err
			}
			continue
		}
		if !equality.Semantic.DeepEqual(cur.Spec.Selectors, dc.Spec.Selectors) ||
			!equality.Semantic.DeepEqual(cur.Spec.ExtendedResourceName, dc.Spec.ExtendedResourceName) {
			cur.Spec.Selectors, cur.Spec.ExtendedResourceName = dc.Spec.Selectors, dc.Spec.ExtendedResourceName
			if err := r.Update(ctx, cur); err != nil {
				return ctrl.Result{}, err
			}
		}
	}
	return ctrl.Result{}, nil
}

// DesiredDeviceClasses returns a class for each resolved Module and for each
// family with any Module, sorted by name. The family class selects the
// device whose default attribute is true, which the driver sets from the
// default label, so moving the default never changes a class.
func DesiredDeviceClasses(mods []v1.Module) []resourcev1.DeviceClass {
	attr := fmt.Sprintf("device.attributes[%q]", v1.DriverName)
	drv := fmt.Sprintf("device.driver == %q", v1.DriverName)
	var out []resourcev1.DeviceClass
	families := map[string]bool{}
	for _, m := range mods {
		families[m.Spec.Family] = true
		if m.Status.Resolved == nil {
			continue
		}
		out = append(out, deviceClass(resolve.DeviceClassName(m.Name), resolve.ResourceName(m.Name),
			fmt.Sprintf("%s && %s.%s == %q", drv, attr, v1.AttrModule, m.Name)))
	}
	for f := range families {
		out = append(out, deviceClass(resolve.FamilyClassName(f), resolve.FamilyResourceName(f),
			fmt.Sprintf("%s && %s.%s == %q && %s.%s == true", drv, attr, v1.AttrFamily, f, attr, v1.AttrDefault)))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func deviceClass(name, resource, expr string) resourcev1.DeviceClass {
	return resourcev1.DeviceClass{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{v1.LabelManaged: "true"}},
		Spec: resourcev1.DeviceClassSpec{
			Selectors:            []resourcev1.DeviceSelector{{CEL: &resourcev1.CELDeviceSelector{Expression: expr}}},
			ExtendedResourceName: ptr.To(resource),
		},
	}
}
