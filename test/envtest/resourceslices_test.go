package integration

import (
	"fmt"
	"strings"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
)

const moduleNRIUser = "system:serviceaccount:" + modulesNamespace + ":module-nri"

// impersonate returns a client acting as user, bound to node when node is
// not empty. system:masters keeps RBAC out of the way, so only admission
// decides.
func impersonate(user, node string) (client.Client, error) {
	cfg := rest.CopyConfig(restCfg)
	cfg.Impersonate = rest.ImpersonationConfig{
		UserName: user,
		Groups:   []string{"system:masters", "system:authenticated"},
	}
	if node != "" {
		cfg.Impersonate.Extra = map[string][]string{"authentication.kubernetes.io/node-name": {node}}
	}
	return client.New(cfg, client.Options{Scheme: scheme})
}

// nodeSlice is a ResourceSlice as the kubelet-plugin helper writes it: pool
// named after the node, spec.nodeName set.
func nodeSlice(driver, node string) *resourcev1.ResourceSlice {
	return &resourcev1.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "0-" + strings.ReplaceAll(driver, ".", "-") + "-" + node + "-"},
		Spec: resourcev1.ResourceSliceSpec{
			Driver:   driver,
			NodeName: ptr.To(node),
			Pool:     resourcev1.ResourcePool{Name: node, Generation: 1, ResourceSliceCount: 1},
		},
	}
}

func TestModuleNRIResourceSlices(t *testing.T) {
	n := familySeq.Add(1)
	a, b := fmt.Sprintf("slice-a-%d", n), fmt.Sprintf("slice-b-%d", n)
	nri := func(node string) client.Client {
		c, err := impersonate(moduleNRIUser, node)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	deny := func(what string, err error, substr string) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: admitted; want denial %q", what, substr)
		} else if !strings.Contains(err.Error(), substr) {
			t.Errorf("%s: %v; want %q", what, err, substr)
		}
	}

	// Own node, own driver: create, update and delete all work.
	own := nodeSlice(v1.DriverName, a)
	if err := nri(a).Create(ctx, own); err != nil {
		t.Fatalf("own slice create: %v", err)
	}
	own.Spec.Pool.Generation = 2
	if err := nri(a).Update(ctx, own); err != nil {
		t.Errorf("own slice update: %v", err)
	}
	if err := nri(a).Delete(ctx, own); err != nil {
		t.Errorf("own slice delete: %v", err)
	}

	deny("other node's slice create", nri(a).Create(ctx, nodeSlice(v1.DriverName, b)),
		"may only write ResourceSlices of its own node")
	deny("other driver's slice create", nri(a).Create(ctx, nodeSlice("gpu.example.com", a)),
		"may only write ResourceSlices of driver modules.isambard.ac.uk")
	deny("slice create without node-bound token", nri("").Create(ctx, nodeSlice(v1.DriverName, a)),
		"node-bound service account token")

	// Slices written by others (here an admin, standing in for another
	// node's module-nri or another driver) can't be changed or deleted.
	other := nodeSlice(v1.DriverName, b)
	create(t, other)
	t.Cleanup(func() { _ = k8s.Delete(ctx, other) })
	moved := other.DeepCopy()
	moved.Spec.Pool.Generation = 2
	deny("other node's slice update", nri(a).Update(ctx, moved), "may only write ResourceSlices of its own node")
	deny("other node's slice delete", nri(a).Delete(ctx, other.DeepCopy()), "may only write ResourceSlices of its own node")
	gpu := nodeSlice("gpu.example.com", a)
	create(t, gpu)
	t.Cleanup(func() { _ = k8s.Delete(ctx, gpu) })
	deny("other driver's slice delete", nri(a).Delete(ctx, gpu.DeepCopy()),
		"may only write ResourceSlices of driver modules.isambard.ac.uk")

	// Other callers, even node-bound ones, are unaffected.
	kubelet, err := impersonate("system:node:"+a, a)
	if err != nil {
		t.Fatal(err)
	}
	foreign := nodeSlice("gpu.example.com", b)
	if err := kubelet.Create(ctx, foreign); err != nil {
		t.Errorf("non-module-nri create: %v", err)
	} else if err := kubelet.Delete(ctx, foreign); err != nil {
		t.Errorf("non-module-nri delete: %v", err)
	}
}
