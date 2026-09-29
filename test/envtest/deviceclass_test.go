package integration

import (
	"fmt"
	"testing"
	"time"

	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/ptr"
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

func getClass(name string) (*resourcev1.DeviceClass, error) {
	dc := &resourcev1.DeviceClass{}
	err := k8s.Get(ctx, client.ObjectKey{Name: name}, dc)
	return dc, err
}

func classAbsent(name string) error {
	if _, err := getClass(name); !apierrors.IsNotFound(err) {
		return fmt.Errorf("class %s present or errored (err %v)", name, err)
	}
	return nil
}

// TestDeviceClassesHeal checks that the reconciler restores a deleted class
// and reverts edits to a managed class's selectors or extended resource name.
func TestDeviceClassesHeal(t *testing.T) {
	fam := newFamily("dch")
	m := imageModule(fam, "1", withDefault())
	create(t, m)
	waitResolved(t, m.Name)
	name, res := resolve.DeviceClassName(m.Name), resolve.ResourceName(m.Name)

	var orig *resourcev1.DeviceClass
	eventually(t, 15*time.Second, func() error {
		var err error
		orig, err = getClass(name)
		return err
	})

	// Delete: recreated.
	if err := k8s.Delete(ctx, orig); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, func() error {
		dc, err := getClass(name)
		if err != nil {
			return err
		}
		if dc.UID == orig.UID {
			return fmt.Errorf("class %s not yet recreated", name)
		}
		return nil
	})

	// Edit: reverted.
	dc, err := getClass(name)
	if err != nil {
		t.Fatal(err)
	}
	dc.Spec.Selectors = []resourcev1.DeviceSelector{{CEL: &resourcev1.CELDeviceSelector{Expression: "true"}}}
	dc.Spec.ExtendedResourceName = ptr.To("example.com/wrong")
	if err := k8s.Update(ctx, dc); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, func() error {
		got, err := getClass(name)
		if err != nil {
			return err
		}
		if got.Spec.ExtendedResourceName == nil || *got.Spec.ExtendedResourceName != res ||
			len(got.Spec.Selectors) != 1 || got.Spec.Selectors[0].CEL == nil ||
			got.Spec.Selectors[0].CEL.Expression != orig.Spec.Selectors[0].CEL.Expression {
			return fmt.Errorf("class %s not reverted: %+v", name, got.Spec)
		}
		return nil
	})
}

// TestDeviceClassesUnresolvedAndFamily checks that an unresolved Module gets
// no Module class but its family still gets a family class, and that the
// family class survives while another Module of the family remains.
func TestDeviceClassesUnresolvedAndFamily(t *testing.T) {
	fam := newFamily("dcu")
	pending := imageModule(fam, "1", withDeps("no-such-module-"+fam))
	create(t, pending)
	famClass := resolve.FamilyClassName(fam)
	eventually(t, 15*time.Second, func() error {
		_, err := getClass(famClass)
		return err
	})
	got := &v1.Module{}
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(pending), got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Resolved != nil {
		t.Fatalf("module %s unexpectedly resolved", pending.Name)
	}
	if err := classAbsent(resolve.DeviceClassName(pending.Name)); err != nil {
		t.Fatal(err)
	}

	// A second Module of the family; delete the first, family class stays.
	second := imageModule(fam, "2")
	create(t, second)
	waitResolved(t, second.Name)
	eventually(t, 15*time.Second, func() error {
		_, err := getClass(resolve.DeviceClassName(second.Name))
		return err
	})
	if err := k8s.Delete(ctx, pending); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, func() error {
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(pending), &v1.Module{}); !apierrors.IsNotFound(err) {
			return fmt.Errorf("module %s still present (err %v)", pending.Name, err)
		}
		return nil
	})
	// Give the reconciler time to act on the deletion, then require both classes.
	time.Sleep(2 * time.Second)
	for _, n := range []string{famClass, resolve.DeviceClassName(second.Name)} {
		if _, err := getClass(n); err != nil {
			t.Fatalf("class %s: %v", n, err)
		}
	}

	// Deleting the last Module removes the family class.
	if err := k8s.Delete(ctx, second); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, func() error { return classAbsent(famClass) })
}
