// Package fsutil resolves paths inside a directory tree that stands in for
// another filesystem's root, such as a host root or a container rootfs.
package fsutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ErrEscape reports a path whose ".." components climb above the root.
var ErrEscape = errors.New("path escapes root")

const maxSymlinks = 40

// ResolveIn resolves p, an absolute path as seen inside root, to its real
// path inside root. Symlinks are followed as the kernel would if root were
// "/": absolute targets restart at root. Unlike the kernel, a ".." above
// root is an error rather than being clamped, so a link can never name a
// file outside the tree.
func ResolveIn(root, p string) (string, error) {
	pending := strings.Split(p, "/")
	cur := "/"
	links := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			if cur == "/" {
				return "", fmt.Errorf("%s: %w", p, ErrEscape)
			}
			cur = path.Dir(cur)
			continue
		}
		next := path.Join(cur, part)
		fi, err := os.Lstat(Host(root, next))
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			cur = next
			continue
		}
		if links++; links > maxSymlinks {
			return "", fmt.Errorf("%s: too many levels of symbolic links", p)
		}
		target, err := os.Readlink(Host(root, next))
		if err != nil {
			return "", err
		}
		if path.IsAbs(target) {
			cur = "/"
		}
		pending = append(strings.Split(target, "/"), pending...)
	}
	return cur, nil
}

// Host returns the path under root for p, an absolute in-root path.
func Host(root, p string) string {
	return filepath.Join(root, filepath.FromSlash(p))
}
