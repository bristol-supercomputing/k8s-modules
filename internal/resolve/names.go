// Package resolve computes the flattened closure of a Module. It is pure and
// has no Kubernetes client.
package resolve

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strconv"
	"strings"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
)

// MountPath is where a module is mounted in every container.
func MountPath(m *v1.Module) string {
	return v1.MountRoot + "/" + m.Spec.Family + "/" + m.Spec.Version
}

// VolumeName turns a Module name into a pod volume name (a DNS label).
func VolumeName(moduleName string) string { return dnsLabel("mod-", moduleName) }

// DeviceName turns a Module name into its DRA device name (a DNS label).
func DeviceName(moduleName string) string { return dnsLabel("", moduleName) }

// dnsLabel prefixes a sanitised name. Sanitising is lossy (nccl-2.29.2 and
// nccl-2-29.2 both become nccl-2-29-2), so any changed or overlong name gets
// a hash suffix.
func dnsLabel(prefix, name string) string {
	n := prefix + strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, strings.ToLower(name))
	if len(n) <= 63 && n == prefix+name {
		return n
	}
	return n[:min(len(n), 54)] + "-" + shortHash(name)
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// MarkerKey is the env var the DRA driver sets for a module. '-' maps
// reversibly to '_' (Module names have no '_'); anything else, such as a
// '.', is lossy and adds a hash suffix.
func MarkerKey(moduleName string) string {
	var b strings.Builder
	lossy := false
	for _, r := range moduleName {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-':
			b.WriteByte('_')
		default:
			lossy = true
			b.WriteByte('_')
		}
	}
	k := v1.EnvMarkerPrefix + b.String()
	if lossy {
		k += "_" + strings.ToUpper(shortHash(moduleName))
	}
	return k
}

func DeviceClassName(moduleName string) string { return moduleName + v1.DeviceClassSuffix }
func FamilyClassName(family string) string     { return family + v1.FamilyClassSuffix }
func ResourceName(moduleName string) string    { return v1.ResourcePrefix + moduleName }
func FamilyResourceName(family string) string  { return v1.FamilyResourcePrefix + family }

func ResolvedAnnotationKey(family string) string { return v1.AnnotationResolvedPrefix + family }

func EnvAnnotationKey(name string) string { return v1.AnnotationEnvPrefix + name }

// AnnotationValue is the canonical resolved.modules.isambard.ac.uk/<family> value.
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
