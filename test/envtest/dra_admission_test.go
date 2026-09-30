package integration

import (
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/resolve"
)

func ctrWith(name string, limits ...string) corev1.Container {
	c := corev1.Container{Name: name, Image: "busybox"}
	if len(limits) > 0 {
		c.Resources.Limits = corev1.ResourceList{}
		for _, l := range limits {
			c.Resources.Limits[corev1.ResourceName(l)] = resource.MustParse("1")
		}
	}
	return c
}

func container(p *corev1.Pod, name string) corev1.Container {
	for _, c := range p.Spec.Containers {
		if c.Name == name {
			return c
		}
	}
	return corev1.Container{}
}

// mounted reports whether a container mounts the module's volume.
func mounted(c corev1.Container, module string) bool {
	for _, m := range c.VolumeMounts {
		if m.Name == resolve.VolumeName(module) {
			return true
		}
	}
	return false
}

func limitOne(c corev1.Container, key string) bool {
	l, r := c.Resources.Limits[corev1.ResourceName(key)], c.Resources.Requests[corev1.ResourceName(key)]
	return l.String() == "1" && r.String() == "1"
}

// onlyIn checks that exactly the named containers mount the module.
func onlyIn(module string, names ...string) func(*corev1.Pod) error {
	return func(p *corev1.Pod) error {
		want := map[string]bool{}
		for _, n := range names {
			want[n] = true
		}
		for _, c := range p.Spec.Containers {
			if mounted(c, module) != want[c.Name] {
				return fmt.Errorf("container %s mounts %s = %v, want %v (mounts %+v)", c.Name, module, mounted(c, module), want[c.Name], c.VolumeMounts)
			}
		}
		return nil
	}
}

func TestAdmissionExactLimit(t *testing.T) {
	fam := newFamily("exact")
	m := imageModule(fam, "1", withLib("lib"), withEnv("NCCL_DEBUG", "INFO"))
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	p := admit(t, func() *corev1.Pod {
		return newPod(ns, "", ctrWith("trainer", resolve.ResourceName(m.Name)), ctrWith("sidecar"))
	}, onlyIn(m.Name, "trainer"))
	if err := hasAnnotations(m.Status.Resolved.Annotations)(p); err != nil {
		t.Error(err)
	}
	if p.Annotations[v1.AnnotationRequiredPlugins] != v1.RequiredPluginsValue {
		t.Error("required-plugins annotation missing")
	}
	if _, ok := envValue(container(p, "trainer"), "NCCL_DEBUG"); !ok {
		t.Error("trainer lacks env.set")
	}
	if _, ok := envValue(container(p, "sidecar"), "NCCL_DEBUG"); ok {
		t.Error("sidecar must get nothing")
	}
}

func TestAdmissionFamilyLimit(t *testing.T) {
	fam := newFamily("famlim")
	m := imageModule(fam, "1", withDefault())
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	admit(t, func() *corev1.Pod {
		return newPod(ns, "", ctrWith("a", resolve.FamilyResourceName(fam)), ctrWith("b"))
	}, onlyIn(m.Name, "a"))
}

func TestAdmissionDependencyMountsOnRequester(t *testing.T) {
	base := imageModule(newFamily("dep"), "1")
	top := imageModule(newFamily("top"), "1", withDeps(base.Name))
	create(t, base)
	create(t, top)
	waitResolved(t, top.Name)
	ns := newNamespace(t, nil)
	admit(t, func() *corev1.Pod {
		return newPod(ns, "", ctrWith("a", resolve.ResourceName(top.Name)), ctrWith("b"))
	}, func(p *corev1.Pod) error {
		if err := onlyIn(top.Name, "a")(p); err != nil {
			return err
		}
		return onlyIn(base.Name, "a")(p)
	})
}

func TestAdmissionLoadAddsLimits(t *testing.T) {
	m := imageModule(newFamily("loadlim"), "1")
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	withCPU := ctrWith("a")
	withCPU.Resources.Limits = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")}
	admit(t, func() *corev1.Pod { return newPod(ns, m.Name, withCPU, ctrWith("b")) }, func(p *corev1.Pod) error {
		for _, c := range p.Spec.Containers {
			if !limitOne(c, resolve.ResourceName(m.Name)) {
				return fmt.Errorf("container %s resources = %+v", c.Name, c.Resources)
			}
		}
		if cpu := container(p, "a").Resources.Limits[corev1.ResourceCPU]; cpu.String() != "500m" {
			return fmt.Errorf("existing cpu limit lost: %+v", container(p, "a").Resources)
		}
		return onlyIn(m.Name, "a", "b")(p)
	})
}

func TestAdmissionLoadContainers(t *testing.T) {
	fam := newFamily("loadctr")
	m := imageModule(fam, "1", withDefault())
	create(t, m)
	m = waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	admit(t, func() *corev1.Pod {
		p := newPod(ns, fam, ctrWith("trainer"), ctrWith("sidecar"))
		p.Annotations[v1.AnnotationLoadContainers] = "trainer"
		return p
	}, func(p *corev1.Pod) error {
		if !limitOne(container(p, "trainer"), resolve.ResourceName(m.Name)) {
			return fmt.Errorf("trainer lacks the exact limit: %+v", container(p, "trainer").Resources)
		}
		if len(container(p, "sidecar").Resources.Limits) != 0 {
			return fmt.Errorf("sidecar got limits: %+v", container(p, "sidecar").Resources)
		}
		return onlyIn(m.Name, "trainer")(p)
	})
}

func TestAdmissionLoadContainersInvalid(t *testing.T) {
	m := imageModule(newFamily("loadbad"), "1")
	create(t, m)
	waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	for name, anns := range map[string]map[string]string{
		"unknown container": {v1.AnnotationLoad: m.Name, v1.AnnotationLoadContainers: "nope"},
		"empty":             {v1.AnnotationLoad: m.Name, v1.AnnotationLoadContainers: " , "},
		"without load":      {v1.AnnotationLoadContainers: "main"},
	} {
		t.Run(name, func(t *testing.T) {
			reject(t, func() *corev1.Pod {
				p := newPod(ns, "")
				for k, v := range anns {
					p.Annotations[k] = v
				}
				return p
			}, "modules.isambard.ac.uk/load-containers must")
		})
	}
}

func TestAdmissionUnknownLimit(t *testing.T) {
	fam := newFamily("unkfam")
	create(t, imageModule(fam, "1")) // no default
	waitResolved(t, fam+"-1")
	ns := newNamespace(t, nil)
	reject(t, func() *corev1.Pod { return newPod(ns, "", ctrWith("a", resolve.ResourceName("ghost-1"))) }, `module "ghost-1" was not loaded`)
	reject(t, func() *corev1.Pod { return newPod(ns, "", ctrWith("a", resolve.FamilyResourceName(fam))) }, `module "`+fam+`" was not loaded`)
	// An exact limit naming a family is not satisfied by that family's default.
	def := newFamily("exactfam")
	create(t, imageModule(def, "1", withDefault()))
	waitResolved(t, def+"-1")
	reject(t, func() *corev1.Pod {
		return newPod(ns, def, ctrWith("a", resolve.ResourceName(def)))
	}, `module "`+def+`" was not loaded`)
}

func TestAdmissionInitContainerLimit(t *testing.T) {
	m := imageModule(newFamily("init"), "1")
	create(t, m)
	waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	reject(t, func() *corev1.Pod {
		p := newPod(ns, "")
		p.Spec.InitContainers = []corev1.Container{ctrWith("setup", resolve.ResourceName(m.Name))}
		return p
	}, "init containers cannot load modules")
}

func TestAdmissionLimitQuantity(t *testing.T) {
	m := imageModule(newFamily("qty"), "1")
	create(t, m)
	waitResolved(t, m.Name)
	ns := newNamespace(t, nil)
	reject(t, func() *corev1.Pod {
		c := ctrWith("a")
		c.Resources.Limits = corev1.ResourceList{corev1.ResourceName(resolve.ResourceName(m.Name)): resource.MustParse("2")}
		return newPod(ns, "", c)
	}, "module resources must have quantity 1")
}

func TestAdmissionAccessViaLimit(t *testing.T) {
	m := imageModule(newFamily("acclim"), "1", withAccess(map[string]string{"team": "a"}))
	create(t, m)
	waitResolved(t, m.Name)
	ns := newNamespace(t, map[string]string{"team": "b"})
	reject(t, func() *corev1.Pod { return newPod(ns, "", ctrWith("a", resolve.ResourceName(m.Name))) },
		"not permitted in namespace", "was not loaded")
}

// Validation runs after mutation, so each applied load module is also an
// exact limit; the load cap must not count it twice.
func TestAdmissionLoadCap(t *testing.T) {
	names := make([]string, 17)
	for i := range names {
		m := imageModule(newFamily("cap"), "1")
		create(t, m)
		names[i] = m.Name
	}
	for _, n := range names {
		waitResolved(t, n)
	}
	ns := newNamespace(t, nil)
	admit(t, func() *corev1.Pod { return newPod(ns, strings.Join(names, ",")) }, func(p *corev1.Pod) error {
		for _, n := range names {
			if !limitOne(container(p, "main"), resolve.ResourceName(n)) {
				return fmt.Errorf("main lacks the limit for %s: %+v", n, container(p, "main").Resources)
			}
		}
		return nil
	})
	many := make([]string, 33)
	for i := range many {
		many[i] = fmt.Sprintf("capm%d", i)
	}
	reject(t, func() *corev1.Pod { return newPod(ns, strings.Join(many, ",")) }, "more than 32 modules")
}

// Each (container, module limit) pair is one request of the scheduler's
// ResourceClaim, which holds at most 32.
func TestAdmissionLimitCap(t *testing.T) {
	var limits []string
	for range 4 {
		m := imageModule(newFamily("lcap"), "1")
		create(t, m)
		waitResolved(t, m.Name)
		limits = append(limits, resolve.ResourceName(m.Name))
	}
	ns := newNamespace(t, nil)
	pod := func(modules, containers int) func() *corev1.Pod {
		return func() *corev1.Pod {
			cs := make([]corev1.Container, containers)
			for i := range cs {
				cs[i] = ctrWith(fmt.Sprintf("c%d", i), limits[:modules]...)
			}
			return newPod(ns, "", cs...)
		}
	}
	admit(t, pod(4, 8), func(p *corev1.Pod) error {
		for _, c := range p.Spec.Containers {
			for _, l := range limits {
				if !limitOne(c, l) {
					return fmt.Errorf("container %s lacks %s: %+v", c.Name, l, c.Resources)
				}
			}
		}
		return nil
	})
	reject(t, pod(3, 11), "at most 32 module resource limits")
}

// forged builds a pod carrying genuine resolved annotations and the
// required-plugins annotation, so only pod-consistency can deny it.
func forged(ns string, anns map[string]string, containers ...corev1.Container) func() *corev1.Pod {
	return func() *corev1.Pod {
		p := newPod(ns, "", containers...)
		for k, v := range anns {
			p.Annotations[k] = v
		}
		p.Annotations[v1.AnnotationRequiredPlugins] = v1.RequiredPluginsValue
		return p
	}
}

func TestAdmissionConsistencyFamilyLimitAccess(t *testing.T) {
	fam := newFamily("cfam")
	d := imageModule(fam, "1", withDefault(), withAccess(map[string]string{"team": "a"}))
	e := imageModule(fam, "2")
	create(t, d)
	create(t, e)
	waitResolved(t, d.Name)
	e = waitResolved(t, e.Name)
	ns := newNamespace(t, map[string]string{"team": "b"})
	reject(t, forged(ns, e.Status.Resolved.Annotations, ctrWith("a", resolve.FamilyResourceName(fam))),
		"not permitted in namespace")
}

func TestAdmissionConsistencyExactLimitUnresolved(t *testing.T) {
	fam := newFamily("cunres")
	u := imageModule(fam, "1", withDeps("missing-9.9"))
	create(t, u)
	u = waitCondition(t, u.Name, v1.ConditionResolved, metav1.ConditionFalse, "DependencyNotFound")
	if u.Status.Resolved != nil {
		t.Fatalf("unresolved module has status.resolved: %+v", u.Status.Resolved)
	}
	// An unresolved Module has no status.resolved.annotations to copy, so
	// write the value the controller would give it.
	value := fmt.Sprintf("%s;rank=10;src=image;mount=%s/%s/1", u.Name, v1.MountRoot, fam)
	ns := newNamespace(t, nil)
	reject(t, forged(ns, map[string]string{v1.AnnotationResolvedPrefix + fam: value}, ctrWith("a", resolve.ResourceName(u.Name))),
		"is not resolved")
}
