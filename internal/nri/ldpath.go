package nri

import "strings"

// PrependLibraryPath returns dirs followed by the container's current
// LD_LIBRARY_PATH, without duplicates or empty entries. The first
// LD_LIBRARY_PATH in env wins, matching glibc getenv. Ported from the CXI
// DRA driver (internal/cdi/env.go).
func PrependLibraryPath(env []string, dirs []string) string {
	var current string
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "LD_LIBRARY_PATH="); ok {
			current = v
			break
		}
	}
	seen := map[string]bool{}
	var out []string
	add := func(d string) {
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	for _, d := range dirs {
		add(d)
	}
	for d := range strings.SplitSeq(current, ":") {
		add(d)
	}
	return strings.Join(out, ":")
}
