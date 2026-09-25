package resolve

import (
	"sort"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

// Resolve flattens the dependency closure of the named Module.
func Resolve(name string, catalogue map[string]*v1.Module) (*v1.Resolved, error) {
	if _, ok := catalogue[name]; !ok {
		return nil, ErrDependencyNotFound{Module: name, Missing: name}
	}
	w := &walker{catalogue: catalogue, rank: map[string]int{}, onPath: map[string]bool{}}
	if _, err := w.visit(name); err != nil {
		return nil, err
	}

	members := make([]string, 0, len(w.rank))
	for n := range w.rank {
		members = append(members, n)
	}
	sort.Slice(members, func(i, j int) bool {
		a, b := members[i], members[j]
		if w.rank[a] != w.rank[b] {
			return w.rank[a] < w.rank[b]
		}
		return a < b
	})

	if err := familyConflict(members, catalogue); err != nil {
		return nil, err
	}
	env, err := mergeEnv(members, catalogue)
	if err != nil {
		return nil, err
	}

	out := &v1.Resolved{Modules: members, Env: env, Annotations: map[string]string{}}
	for _, n := range members {
		m := catalogue[n]
		out.Annotations[ResolvedAnnotationKey(m.Spec.Family)] = AnnotationValue(m, w.rank[n])
		if img := m.Spec.Source.Image; img != nil {
			vn := VolumeName(n)
			out.Volumes = append(out.Volumes, v1.ResolvedVolume{Name: vn, Reference: img.Reference})
			out.Mounts = append(out.Mounts, v1.ResolvedMount{Name: vn, MountPath: MountPath(m)})
		}
	}
	for _, e := range env {
		out.Annotations[EnvAnnotationKey(e.Name)] = e.Value
	}
	return out, nil
}

type walker struct {
	catalogue map[string]*v1.Module
	rank      map[string]int
	path      []string
	onPath    map[string]bool
}

func (w *walker) visit(name string) (int, error) {
	if r, ok := w.rank[name]; ok {
		return r, nil
	}
	if w.onPath[name] {
		for i, p := range w.path {
			if p == name {
				cycle := append(append([]string{}, w.path[i:]...), name)
				return 0, ErrDependencyCycle{Path: cycle}
			}
		}
	}
	w.onPath[name] = true
	w.path = append(w.path, name)
	maxDep := 0
	for _, dep := range w.catalogue[name].Spec.DependsOn {
		if _, ok := w.catalogue[dep]; !ok {
			return 0, ErrDependencyNotFound{Module: name, Missing: dep}
		}
		r, err := w.visit(dep)
		if err != nil {
			return 0, err
		}
		maxDep = max(maxDep, r)
	}
	w.path = w.path[:len(w.path)-1]
	delete(w.onPath, name)
	rank := 10 * (1 + maxDep/10)
	w.rank[name] = rank
	return rank, nil
}

func familyConflict(members []string, catalogue map[string]*v1.Module) error {
	byFamily := map[string][]string{}
	for _, n := range members {
		f := catalogue[n].Spec.Family
		byFamily[f] = append(byFamily[f], n)
	}
	families := make([]string, 0, len(byFamily))
	for f := range byFamily {
		families = append(families, f)
	}
	sort.Strings(families)
	for _, f := range families {
		if ms := byFamily[f]; len(ms) > 1 {
			sort.Strings(ms)
			return ErrFamilyConflict{Family: f, Modules: ms}
		}
	}
	return nil
}

func mergeEnv(members []string, catalogue map[string]*v1.Module) ([]v1.ResolvedEnvVar, error) {
	type setter struct{ module, value string }
	byName := map[string][]setter{}
	for _, n := range members {
		if e := catalogue[n].Spec.Env; e != nil {
			for k, v := range e.Set {
				byName[k] = append(byName[k], setter{n, v})
			}
		}
	}
	names := make([]string, 0, len(byName))
	for k := range byName {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]v1.ResolvedEnvVar, 0, len(names))
	for _, k := range names {
		ss := byName[k]
		sort.Slice(ss, func(i, j int) bool { return ss[i].module < ss[j].module })
		for _, s := range ss[1:] {
			if s.value != ss[0].value {
				e := ErrEnvConflict{Name: k}
				for _, s := range ss {
					e.Modules = append(e.Modules, s.module)
					e.Values = append(e.Values, s.value)
				}
				return nil, e
			}
		}
		out = append(out, v1.ResolvedEnvVar{Name: k, Value: ss[0].value})
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
