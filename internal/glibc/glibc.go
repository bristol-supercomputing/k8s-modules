// Package glibc knows which libraries belong to glibc and how its symbol
// versions compare.
package glibc

import (
	"strconv"
	"strings"
)

var sonames = map[string]bool{
	"libc.so.6": true, "libm.so.6": true, "libpthread.so.0": true, "libdl.so.2": true, "librt.so.1": true,
}

// Excluded reports whether soname is part of glibc. Host glibc must never
// enter a container.
func Excluded(soname string) bool {
	return sonames[soname] || strings.HasPrefix(soname, "ld-linux")
}

// Version extracts "2.36" from the symbol version name "GLIBC_2.36".
// GLIBC_PRIVATE and other non-numeric names are not versions.
func Version(name string) (string, bool) {
	v, ok := strings.CutPrefix(name, "GLIBC_")
	if !ok || v == "" {
		return "", false
	}
	for _, p := range strings.Split(v, ".") {
		if _, err := strconv.Atoi(p); err != nil {
			return "", false
		}
	}
	return v, true
}

// Less compares dotted numeric versions. The empty string is lowest.
func Less(a, b string) bool {
	if a == "" || b == "" {
		return a == "" && b != ""
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(as), len(bs)); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// Multiarch is the Debian multiarch triplet for a Go architecture.
func Multiarch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64-linux-gnu"
	case "arm64":
		return "aarch64-linux-gnu"
	}
	return ""
}
