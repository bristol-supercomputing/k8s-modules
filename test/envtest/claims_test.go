package integration

import (
	"strings"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func claimFor(ns, class string) *resourcev1.ResourceClaim {
	return &resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "c-", Namespace: ns},
		Spec: resourcev1.ResourceClaimSpec{Devices: resourcev1.DeviceClaim{Requests: []resourcev1.DeviceRequest{{
			Name: "m", Exactly: &resourcev1.ExactDeviceRequest{DeviceClassName: class}}}}},
	}
}

func asUser(t *testing.T, user string) client.Client {
	cfg := rest.CopyConfig(restCfg)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: []string{"system:masters", "system:authenticated"}}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestResourceClaimGuard(t *testing.T) {
	ns := newNamespace(t, nil)
	for _, class := range []string{"nccl-2.29.2.modules.isambard.ac.uk", "nccl.family.modules.isambard.ac.uk"} {
		err := k8s.Create(ctx, claimFor(ns, class))
		if err == nil || !strings.Contains(err.Error(), "module device classes") {
			t.Errorf("claim for %s: err = %v", class, err)
		}
	}
	tmpl := &resourcev1.ResourceClaimTemplate{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "t-", Namespace: ns},
		Spec:       resourcev1.ResourceClaimTemplateSpec{Spec: claimFor(ns, "x-1.modules.isambard.ac.uk").Spec},
	}
	if err := k8s.Create(ctx, tmpl); err == nil || !strings.Contains(err.Error(), "module device classes") {
		t.Errorf("template: err = %v", err)
	}
	firstAvail := claimFor(ns, "")
	firstAvail.Spec.Devices.Requests[0].Exactly = nil
	firstAvail.Spec.Devices.Requests[0].FirstAvailable = []resourcev1.DeviceSubRequest{{Name: "a", DeviceClassName: "x-1.modules.isambard.ac.uk"}}
	if err := k8s.Create(ctx, firstAvail); err == nil || !strings.Contains(err.Error(), "module device classes") {
		t.Errorf("firstAvailable: err = %v", err)
	}

	// The scheduler creates the extended-resource claim itself.
	sched := claimFor(ns, "x-1.modules.isambard.ac.uk")
	if err := asUser(t, "system:kube-scheduler").Create(ctx, sched); err != nil {
		t.Errorf("scheduler's claim: %v", err)
	} else {
		_ = k8s.Delete(ctx, sched)
	}
	other := claimFor(ns, "gpu.example.com")
	create(t, other)
}
