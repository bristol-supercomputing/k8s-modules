// Package resolve computes the flattened closure of a Module. It is pure and
// has no Kubernetes client.
package resolve

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strconv"
	"strings"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
)

// MountPath is where a module is mounted in every container.
func MountPath(m *v1.Module) string {
	return v1.MountRoot + "/" + m.Spec.Family + "/" + m.Spec.Version
}

// VolumeName turns a Module name into a pod volume name (a DNS label).
func VolumeName(moduleName string) string {
	n := "mod-" + strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, strings.ToLower(moduleName))
	if len(n) <= 63 {
		return n
	}
	sum := sha256.Sum256([]byte(moduleName))
	return n[:54] + "-" + hex.EncodeToString(sum[:])[:8]
}

func ResolvedAnnotationKey(family string) string { return v1.AnnotationResolvedPrefix + family }

func EnvAnnotationKey(name string) string { return v1.AnnotationEnvPrefix + name }

// AnnotationValue is the canonical resolved.modules.brics.ac.uk/<family> value.
func AnnotationValue(m *v1.Module, rank int) string {
	mount := MountPath(m)
	parts := []string{m.Name, "rank=" + strconv.Itoa(rank)}
	src := m.Spec.Source
	switch {
	case src.Image != nil:
		parts = append(parts, "src=image")
	case src.Host != nil:
		parts = append(parts, "src=host")
	case src.PVC != nil:
		parts = append(parts, "src=pvc:"+src.PVC.ClaimName+"/"+src.PVC.SubPath)
	}
	parts = append(parts, "mount="+mount)

	var libs []string
	if src.Host != nil {
		libs = []string{mount + "/lib"}
	} else if m.Spec.Env != nil {
		for _, p := range m.Spec.Env.PrependPath[v1.EnvLDLibraryPath] {
			libs = append(libs, path.Join(mount, p))
		}
	}
	if len(libs) > 0 {
		parts = append(parts, "lib="+strings.Join(libs, ":"))
	}
	if len(m.Spec.MaskPaths) > 0 {
		parts = append(parts, "mask="+strings.Join(m.Spec.MaskPaths, ":"))
	}
	return strings.Join(parts, ";")
}
