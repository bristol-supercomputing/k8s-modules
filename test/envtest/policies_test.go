package integration

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

const probeNS = "policy-probe"

func installPolicies() error {
	out, err := exec.Command("helm", "template", "k8s-modules", chart,
		"--namespace", modulesNamespace,
		"--set", "registryAllowlist[0]="+testRegistry).Output()
	if err != nil {
		return fmt.Errorf("helm template: %w", err)
	}
	dec := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(out), 4096)
	for {
		u := &unstructured.Unstructured{}
		if err := dec.Decode(&u.Object); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}
		if !strings.HasPrefix(u.GetAPIVersion(), "admissionregistration.k8s.io/") {
			continue
		}
		if err := k8s.Create(ctx, u); err != nil {
			return fmt.Errorf("create %s %s: %w", u.GetKind(), u.GetName(), err)
		}
	}
	return waitForPolicies()
}

// waitForPolicies blocks until each policy and its param informer is live,
// so tests never race policy start-up.
func waitForPolicies() error {
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: probeNS}}); err != nil {
		return err
	}
	probe := imageModule("probe", "1", withDefault())
	if err := k8s.Create(ctx, probe); err != nil {
		return err
	}
	probePod := func(anns map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "probe-", Namespace: probeNS, Annotations: anns},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "busybox"}}},
		}
	}
	checks := []struct {
		name string
		try  func() (bool, error)
	}{
		{"mutating policy", func() (bool, error) {
			p := probePod(map[string]string{v1.AnnotationLoad: "probe-1"})
			if err := k8s.Create(ctx, p); err != nil {
				return false, nil
			}
			defer k8s.Delete(ctx, p)
			_, ok := p.Annotations[v1.AnnotationResolvedPrefix+"probe"]
			return ok, nil
		}},
		{"pods policy", func() (bool, error) { return rejected(probePod(map[string]string{v1.AnnotationLoad: "Bad!"})), nil }},
		{"pod-consistency policy", func() (bool, error) {
			return rejected(probePod(map[string]string{v1.AnnotationResolvedPrefix + "probe": "probe-1;rank=99"})), nil
		}},
		{"module-guards policy", func() (bool, error) {
			m := imageModule("probe", "2")
			m.Spec.Source.Image.Reference = "evil.test/x" + digest()
			return rejected(m), nil
		}},
		{"module-default policy", func() (bool, error) { return rejected(imageModule("probe", "3", withDefault())), nil }},
	}
	for _, c := range checks {
		deadline := time.Now().Add(60 * time.Second)
		for {
			ok, err := c.try()
			if err != nil {
				return err
			}
			if ok {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%s never became active", c.name)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	return nil
}

// rejected creates obj and reports whether admission refused it. An accepted
// object is deleted again.
func rejected(obj client.Object) bool {
	if err := k8s.Create(ctx, obj); err != nil {
		return true
	}
	_ = k8s.Delete(ctx, obj)
	return false
}
