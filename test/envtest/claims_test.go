package integration

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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

func TestResourceClaimTemplateGuardVariants(t *testing.T) {
	ns := newNamespace(t, nil)
	tmpl := &resourcev1.ResourceClaimTemplate{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "t-", Namespace: ns},
		Spec:       resourcev1.ResourceClaimTemplateSpec{Spec: claimFor(ns, "").Spec},
	}
	tmpl.Spec.Spec.Devices.Requests[0].Exactly = nil
	tmpl.Spec.Spec.Devices.Requests[0].FirstAvailable = []resourcev1.DeviceSubRequest{{Name: "a", DeviceClassName: "x-1.modules.isambard.ac.uk"}}
	if err := k8s.Create(ctx, tmpl); err == nil || !strings.Contains(err.Error(), "module device classes") {
		t.Errorf("firstAvailable template: err = %v", err)
	}
	// Even the scheduler may only create claims, never templates.
	st := &resourcev1.ResourceClaimTemplate{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "t-", Namespace: ns},
		Spec:       resourcev1.ResourceClaimTemplateSpec{Spec: claimFor(ns, "x-1.modules.isambard.ac.uk").Spec},
	}
	if err := asUser(t, "system:kube-scheduler").Create(ctx, st); err == nil || !strings.Contains(err.Error(), "module device classes") {
		t.Errorf("scheduler template: err = %v", err)
	}
}

func TestImplicitModuleClassNamesRejected(t *testing.T) {
	ns := newNamespace(t, nil)
	rl := func(key string) corev1.ResourceList {
		return corev1.ResourceList{corev1.ResourceName(key): resource.MustParse("1")}
	}
	pod := func(mut func(*corev1.Pod)) func() *corev1.Pod {
		return func() *corev1.Pod {
			p := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{GenerateName: "ic-", Namespace: ns},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "busybox"}}},
			}
			mut(p)
			return p
		}
	}
	const msg = "module device classes"
	const mod = "deviceclass.resource.kubernetes.io/x-1.modules.isambard.ac.uk"
	const fam = "deviceclass.resource.kubernetes.io/x.family.modules.isambard.ac.uk"
	reject(t, pod(func(p *corev1.Pod) {
		p.Spec.Containers[0].Resources.Limits = rl(mod)
	}), msg)
	reject(t, pod(func(p *corev1.Pod) {
		p.Spec.Containers[0].Resources.Limits = rl(fam)
	}), msg)
	// Extended resources need requests equal to limits, so a request-only
	// key cannot be built; set both and let the requests branch match too.
	reject(t, pod(func(p *corev1.Pod) {
		p.Spec.Containers[0].Resources.Requests = rl(mod)
		p.Spec.Containers[0].Resources.Limits = rl(mod)
	}), msg)
	reject(t, pod(func(p *corev1.Pod) {
		p.Spec.InitContainers = []corev1.Container{{Name: "i", Image: "busybox", Resources: corev1.ResourceRequirements{Limits: rl(mod)}}}
	}), msg)

	// An unrelated implicit name is not this policy's business.
	admit(t, pod(func(p *corev1.Pod) {
		p.Spec.Containers[0].Resources.Limits = rl("deviceclass.resource.kubernetes.io/gpu.example.com")
	}), func(*corev1.Pod) error { return nil })
}
