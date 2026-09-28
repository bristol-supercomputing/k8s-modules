// Package hook implements module-hook: it adds host-module dependency
// directories to an image's ld cache and checks the image's glibc. It runs
// as an OCI createContainer hook, as host root before pivot_root, on an
// untrusted image rootfs. So it never executes code from the image, and
// every rootfs access goes through os.Root, which confines symlink
// resolution to the rootfs in the kernel (no resolve-then-open race).
package hook

import (
	"bytes"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/isambard-sc/k8s-modules/internal/glibc"
)

// Options are module-hook's arguments.
type Options struct {
	Deps     []string          // container paths of deps directories
	Glibc    map[string]string // module name -> required GLIBC version
	Arch     string            // Go architecture, for multiarch lookup
	Ldconfig string            // trusted static ldconfig, shipped with module-hook
}

// Runner rebuilds rootfs's ld cache with the trusted ldconfig.
type Runner interface {
	Run(rootfs, ldconfig string) error
}

// ConfName is the ld.so.conf.d file module-hook owns.
const ConfName = "zz-brics-modules.conf"

// ldconfig scans these last, after every ld.so.conf entry. Listing the ones
// the image has before the deps directories keeps the image's own copies
// ahead of host copies in the cache.
var trustedDirs = []string{"/lib64", "/usr/lib64", "/lib", "/usr/lib"}

// Apply writes the ld config, rebuilds the ld cache and checks glibc.
func Apply(rootfs string, o Options, r Runner) error {
	root, err := os.OpenRoot(rootfs)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := writeConf(root, o.Deps); err != nil {
		return err
	}
	if err := r.Run(rootfs, o.Ldconfig); err != nil {
		return fmt.Errorf("updating the image's ld cache: %w", err)
	}
	if len(o.Glibc) == 0 {
		return nil
	}
	return checkGlibc(root, o)
}

func writeConf(root *os.Root, deps []string) error {
	var b strings.Builder
	for _, d := range trustedDirs {
		if fi, err := root.Stat(rel(d)); err == nil && fi.IsDir() {
			b.WriteString(d + "\n")
		}
	}
	for _, d := range deps {
		b.WriteString(d + "\n")
	}
	const confd = "etc/ld.so.conf.d"
	if err := root.MkdirAll(confd, 0o755); err != nil {
		return writeErr(err)
	}
	// Replace, never follow, whatever the image put at our file's name.
	name := path.Join(confd, ConfName)
	if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return writeErr(err)
	}
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return writeErr(err)
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		return writeErr(err)
	}
	if err := f.Close(); err != nil {
		return writeErr(err)
	}
	return ensureInclude(root)
}

func ensureInclude(root *os.Root) error {
	const conf = "etc/ld.so.conf"
	data, err := root.ReadFile(conf)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return writeErr(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "include" && (f[1] == "ld.so.conf.d/*.conf" || f[1] == "/etc/ld.so.conf.d/*.conf") {
			return nil
		}
	}
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	data = append(data, "include /etc/ld.so.conf.d/*.conf\n"...)
	if err := root.WriteFile(conf, data, 0o644); err != nil {
		return writeErr(err)
	}
	return nil
}

func writeErr(err error) error {
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS) {
		return fmt.Errorf("host modules need a writable /etc in the image (readOnlyRootFilesystem is not supported): %w", err)
	}
	return fmt.Errorf("writing the image's ld config: %w", err)
}

// rel turns an absolute in-image path into an os.Root-relative name.
func rel(p string) string { return strings.TrimPrefix(path.Clean(p), "/") }

func checkGlibc(root *os.Root, o Options) error {
	have, err := imageGlibc(root, o.Arch)
	if err != nil {
		return err
	}
	mods := make([]string, 0, len(o.Glibc))
	for m := range o.Glibc {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	for _, m := range mods {
		if glibc.Less(have, o.Glibc[m]) {
			return fmt.Errorf("module %s needs GLIBC_%s; the image's libc.so.6 provides up to GLIBC_%s", m, o.Glibc[m], have)
		}
	}
	return nil
}

func imageGlibc(root *os.Root, goarch string) (string, error) {
	dirs := []string{"/lib64", "/usr/lib64"}
	if ma := glibc.Multiarch(goarch); ma != "" {
		dirs = append(dirs, "/lib/"+ma, "/usr/lib/"+ma)
	}
	dirs = append(dirs, "/lib", "/usr/lib")
	for _, d := range dirs {
		have, ok := libcVersion(root, rel(path.Join(d, "libc.so.6")))
		if ok {
			return have, nil
		}
	}
	return "", errors.New("image has no glibc libc.so.6; host modules require a glibc-based image")
}

func libcVersion(root *os.Root, name string) (string, bool) {
	file, err := root.Open(name)
	if err != nil {
		return "", false
	}
	defer file.Close()
	f, err := elf.NewFile(file)
	if err != nil {
		return "", false
	}
	vers, err := f.DynamicVersions()
	if err != nil {
		return "", false
	}
	have := ""
	for _, v := range vers {
		if g, ok := glibc.Version(v.Name); ok && glibc.Less(have, g) {
			have = g
		}
	}
	return have, have != ""
}

// RootFromState reads the OCI state JSON a hook receives on stdin and
// returns the container's rootfs path from the bundle's config.json.
func RootFromState(state []byte) (string, error) {
	var st struct {
		Bundle string `json:"bundle"`
	}
	if err := json.Unmarshal(state, &st); err != nil || st.Bundle == "" {
		return "", fmt.Errorf("reading OCI state: no bundle (%v)", err)
	}
	cfg, err := os.ReadFile(filepath.Join(st.Bundle, "config.json"))
	if err != nil {
		return "", err
	}
	var spec struct {
		Root *struct {
			Path string `json:"path"`
		} `json:"root"`
	}
	if err := json.Unmarshal(cfg, &spec); err != nil || spec.Root == nil || spec.Root.Path == "" {
		return "", fmt.Errorf("bundle config.json has no root.path (%v)", err)
	}
	if filepath.IsAbs(spec.Root.Path) {
		return spec.Root.Path, nil
	}
	return filepath.Join(st.Bundle, spec.Root.Path), nil
}
