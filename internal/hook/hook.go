// Package hook implements module-hook: it adds host-module dependency
// directories to an image's ld cache and checks the image's glibc. It runs
// as an OCI createContainer hook, so paths here are host paths into the
// container's rootfs, and every in-image path is resolved with
// fsutil.ResolveIn so image symlinks cannot reach outside the rootfs.
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

	"github.com/bristol-supercomputing/k8s-modules/internal/fsutil"
	"github.com/bristol-supercomputing/k8s-modules/internal/glibc"
)

// Options are module-hook's arguments.
type Options struct {
	Deps  []string          // container paths of deps directories
	Glibc map[string]string // module name -> required GLIBC version
	Arch  string            // Go architecture, for multiarch lookup
}

// Runner runs the image's ldconfig with rootfs as its root.
type Runner interface {
	Run(rootfs, ldconfig string) error
}

// ConfName is the ld.so.conf.d file module-hook owns.
const ConfName = "zz-brics-modules.conf"

// ldconfig scans these last, after every ld.so.conf entry. Listing the ones
// the image has before the deps directories keeps the image's own copies
// ahead of host copies in the cache.
var trustedDirs = []string{"/lib64", "/usr/lib64", "/lib", "/usr/lib"}

// Apply writes the ld config, runs ldconfig and checks glibc.
func Apply(rootfs string, o Options, r Runner) error {
	if err := writeConf(rootfs, o.Deps); err != nil {
		return err
	}
	ldconfig, err := findLdconfig(rootfs)
	if err != nil {
		return err
	}
	if err := r.Run(rootfs, ldconfig); err != nil {
		return fmt.Errorf("updating the image's ld cache: %w", err)
	}
	if len(o.Glibc) == 0 {
		return nil
	}
	return checkGlibc(rootfs, o)
}

func writeConf(rootfs string, deps []string) error {
	var b strings.Builder
	for _, d := range trustedDirs {
		if isDir(rootfs, d) {
			b.WriteString(d + "\n")
		}
	}
	for _, d := range deps {
		b.WriteString(d + "\n")
	}
	confd, err := resolveForWrite(rootfs, "/etc/ld.so.conf.d")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(fsutil.Host(rootfs, confd), 0o755); err != nil {
		return readOnly(err)
	}
	if err := os.WriteFile(fsutil.Host(rootfs, path.Join(confd, ConfName)), []byte(b.String()), 0o644); err != nil {
		return readOnly(err)
	}
	return ensureInclude(rootfs)
}

func ensureInclude(rootfs string) error {
	conf, err := resolveForWrite(rootfs, "/etc/ld.so.conf")
	if err != nil {
		return err
	}
	file := fsutil.Host(rootfs, conf)
	data, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
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
	if err := os.WriteFile(file, data, 0o644); err != nil {
		return readOnly(err)
	}
	return nil
}

// resolveForWrite resolves p inside rootfs; when p does not exist yet, its
// parent is resolved and p's base name joined to it.
func resolveForWrite(rootfs, p string) (string, error) {
	real, err := fsutil.ResolveIn(rootfs, p)
	if err == nil {
		return real, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	parent, err := fsutil.ResolveIn(rootfs, path.Dir(p))
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return "", err
	}
	return path.Join(parent, path.Base(p)), nil
}

func readOnly(err error) error {
	return fmt.Errorf("host modules need a writable /etc in the image (readOnlyRootFilesystem is not supported): %w", err)
}

func isDir(rootfs, p string) bool {
	real, err := fsutil.ResolveIn(rootfs, p)
	if err != nil {
		return false
	}
	fi, err := os.Stat(fsutil.Host(rootfs, real))
	return err == nil && fi.IsDir()
}

func findLdconfig(rootfs string) (string, error) {
	for _, p := range []string{"/sbin/ldconfig", "/usr/sbin/ldconfig"} {
		real, err := fsutil.ResolveIn(rootfs, p)
		if err != nil {
			continue
		}
		if fi, err := os.Stat(fsutil.Host(rootfs, real)); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return real, nil
		}
	}
	return "", errors.New("image has no ldconfig; host modules require a glibc-based image")
}

func checkGlibc(rootfs string, o Options) error {
	have, err := imageGlibc(rootfs, o.Arch)
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

func imageGlibc(rootfs, goarch string) (string, error) {
	dirs := []string{"/lib64", "/usr/lib64"}
	if ma := glibc.Multiarch(goarch); ma != "" {
		dirs = append(dirs, "/lib/"+ma, "/usr/lib/"+ma)
	}
	dirs = append(dirs, "/lib", "/usr/lib")
	for _, d := range dirs {
		real, err := fsutil.ResolveIn(rootfs, path.Join(d, "libc.so.6"))
		if err != nil {
			continue
		}
		f, err := elf.Open(fsutil.Host(rootfs, real))
		if err != nil {
			continue
		}
		vers, err := f.DynamicVersions()
		f.Close()
		if err != nil {
			continue
		}
		have := ""
		for _, v := range vers {
			if g, ok := glibc.Version(v.Name); ok && glibc.Less(have, g) {
				have = g
			}
		}
		if have != "" {
			return have, nil
		}
	}
	return "", errors.New("image has no glibc libc.so.6; host modules require a glibc-based image")
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
