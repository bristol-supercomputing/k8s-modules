package integration

import (
	"fmt"
	"testing"
	"time"

	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

func TestDeviceClasses(t *testing.T) {
	fam := newFamily("dc")
	m := imageModule(fam, "1", withDefault())
	create(t, m)
	waitResolved(t, m.Name)

	expect := map[string]string{
		resolve.DeviceClassName(m.Name): resolve.ResourceName(m.Name),
		resolve.FamilyClassName(fam):    resolve.FamilyResourceName(fam),
	}
	eventually(t, 15*time.Second, func() error {
		for name, res := range expect {
			dc := &resourcev1.DeviceClass{}
			if err := k8s.Get(ctx, client.ObjectKey{Name: name}, dc); err != nil {
				return err
			}
			if dc.Labels[v1.LabelManaged] != "true" || dc.Spec.ExtendedResourceName == nil || *dc.Spec.ExtendedResourceName != res {
				return fmt.Errorf("class %s = %+v", name, dc)
			}
		}
		return nil
	})

	if err := k8s.Delete(ctx, m); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, func() error {
		for name := range expect {
			err := k8s.Get(ctx, client.ObjectKey{Name: name}, &resourcev1.DeviceClass{})
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("class %s still present (err %v)", name, err)
			}
		}
		return nil
	})
}
