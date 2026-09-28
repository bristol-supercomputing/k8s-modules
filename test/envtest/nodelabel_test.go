package integration

import (
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
)

func TestNodeLabelsFromReport(t *testing.T) {
	name := fmt.Sprintf("label-node-%d", familySeq.Add(1))
	create(t, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name,
		Labels: map[string]string{"keep": "me", v1.LabelHostPrefix + "stale": "true"}}})
	rep := &v1.ModuleNodeReport{ObjectMeta: metav1.ObjectMeta{Name: name}}
	create(t, rep)

	setReady := func(ready bool) {
		t.Helper()
		eventually(t, 5*time.Second, func() error {
			if err := k8s.Get(ctx, client.ObjectKeyFromObject(rep), rep); err != nil {
				return err
			}
			rep.Status.Modules = []v1.HostModuleReport{{Module: "fab-host", Family: "fab", Ready: ready}}
			return k8s.Status().Update(ctx, rep)
		})
	}
	expect := func(want map[string]string) {
		t.Helper()
		eventually(t, 15*time.Second, func() error {
			n := &corev1.Node{}
			if err := k8s.Get(ctx, client.ObjectKey{Name: name}, n); err != nil {
				return err
			}
			for k, v := range want {
				got, ok := n.Labels[k]
				if v == "" && ok || v != "" && got != v {
					return fmt.Errorf("label %s = %q (present=%v), want %q", k, got, ok, v)
				}
			}
			return nil
		})
	}
	fab, stale := v1.LabelHostPrefix+"fab", v1.LabelHostPrefix+"stale"

	setReady(true)
	expect(map[string]string{fab: "true", stale: "", "keep": "me"})
	setReady(false)
	expect(map[string]string{fab: "", "keep": "me"})
	setReady(true)
	expect(map[string]string{fab: "true"})
	if err := k8s.Delete(ctx, rep); err != nil {
		t.Fatal(err)
	}
	expect(map[string]string{fab: "", "keep": "me"})
}
