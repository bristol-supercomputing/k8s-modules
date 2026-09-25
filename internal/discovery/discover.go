package discovery

import (
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bristol-supercomputing/k8s-modules/internal/fsutil"
	"github.com/bristol-supercomputing/k8s-modules/internal/glibc"
)

// Library is one file of a closure. Path is the real host path.
type Library struct {
	Soname string
	Path   string
	Size   int64
}

// Result is a host module's closure on one node.
type Result struct {
	Ready         bool
	Lib, Deps     []Library
	Missing       []string
	GlibcRequired string
	Hash          string
	Message       string
}

// Host is a node filesystem mounted at Root.
type Host struct {
	Root  string
	Arch  string
	cache Cache
}

// NewHost reads Root/etc/ld.so.cache.
func NewHost(root, goarch string) (*Host, error) {
	data, err := os.ReadFile(filepath.Join(root, "etc", "ld.so.cache"))
	if err != nil {
		return nil, err
	}
	c, err := ParseCache(data, goarch)
	if err != nil {
		return nil, fmt.Errorf("/etc/ld.so.cache: %w", err)
	}
	return &Host{Root: root, Arch: goarch, cache: c}, nil
}

// Discover resolves roots through the cache and walks DT_NEEDED. Roots and
// preferHost entries are placed in Lib, everything else in Deps. glibc is
// never included.
func (h *Host) Discover(roots, preferHost []string) Result {
	var res Result
	fail := func(format string, args ...any) {
		if res.Message == "" {
			res.Message = fmt.Sprintf(format, args...)
		}
	}
	isRoot, prefer := setOf(roots), setOf(preferHost)
	visited := map[string]bool{}
	type item struct{ soname, path string }
	var queue []item
	for _, r := range roots {
		if visited[r] {
			continue
		}
		visited[r] = true
		p, ok := h.cache[r]
		if !ok {
			res.Missing = append(res.Missing, r)
			fail("root %s not in /etc/ld.so.cache", r)
			continue
		}
		queue = append(queue, item{r, p})
	}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		real, err := fsutil.ResolveIn(h.Root, it.path)
		if err != nil {
			fail("%s: %v", it.soname, err)
			continue
		}
		full := fsutil.Host(h.Root, real)
		fi, err := os.Stat(full)
		if err != nil || !fi.Mode().IsRegular() {
			fail("%s: %s is not a regular file", it.soname, real)
			continue
		}
		f, err := elf.Open(full)
		if err != nil {
			fail("%s: %v", it.soname, err)
			continue
		}
		needed, _ := f.DynString(elf.DT_NEEDED)
		runpath, _ := f.DynString(elf.DT_RUNPATH)
		rpath, _ := f.DynString(elf.DT_RPATH)
		if v := glibcFloor(f); glibc.Less(res.GlibcRequired, v) {
			res.GlibcRequired = v
		}
		f.Close()

		lib := Library{Soname: it.soname, Path: real, Size: fi.Size()}
		if isRoot[it.soname] || prefer[it.soname] {
			res.Lib = append(res.Lib, lib)
		} else {
			res.Deps = append(res.Deps, lib)
		}
		// $ORIGIN is the directory the loader found the object in, which
		// is the unresolved lookup path.
		origin := path.Dir(it.path)
		for _, n := range needed {
			if glibc.Excluded(n) || visited[n] {
				continue
			}
			visited[n] = true
			p, ok := h.lookup(n, origin, rpath, runpath)
			if !ok {
				res.Missing = append(res.Missing, n)
				fail("%s needs %s: not found", it.soname, n)
				continue
			}
			queue = append(queue, item{n, p})
		}
	}
	bySoname := func(ls []Library) {
		sort.Slice(ls, func(i, j int) bool { return ls[i].Soname < ls[j].Soname })
	}
	bySoname(res.Lib)
	bySoname(res.Deps)
	sort.Strings(res.Missing)
	res.Ready = res.Message == ""
	res.Hash = closureHash(res)
	return res
}

// lookup follows ld.so's order: DT_RPATH (only without DT_RUNPATH),
// DT_RUNPATH, the cache, then the default directories.
func (h *Host) lookup(soname, origin string, rpath, runpath []string) (string, bool) {
	var dirs []string
	if len(runpath) == 0 {
		dirs = append(dirs, expand(rpath, origin)...)
	}
	dirs = append(dirs, expand(runpath, origin)...)
	for _, d := range dirs {
		if p := path.Join(d, soname); h.isFile(p) {
			return p, true
		}
	}
	if p, ok := h.cache[soname]; ok {
		return p, true
	}
	for _, d := range defaultDirs(h.Arch) {
		if p := path.Join(d, soname); h.isFile(p) {
			return p, true
		}
	}
	return "", false
}

func (h *Host) isFile(p string) bool {
	real, err := fsutil.ResolveIn(h.Root, p)
	if err != nil {
		return false
	}
	fi, err := os.Stat(fsutil.Host(h.Root, real))
	return err == nil && fi.Mode().IsRegular()
}

func expand(entries []string, origin string) []string {
	var out []string
	for _, e := range entries {
		for _, d := range strings.Split(e, ":") {
			if d == "" || strings.Contains(d, "$LIB") || strings.Contains(d, "${LIB}") ||
				strings.Contains(d, "$PLATFORM") || strings.Contains(d, "${PLATFORM}") {
				continue
			}
			d = strings.ReplaceAll(strings.ReplaceAll(d, "${ORIGIN}", origin), "$ORIGIN", origin)
			if path.IsAbs(d) {
				out = append(out, path.Clean(d))
			}
		}
	}
	return out
}

func defaultDirs(goarch string) []string {
	dirs := []string{"/lib64", "/usr/lib64", "/lib", "/usr/lib"}
	if ma := glibc.Multiarch(goarch); ma != "" {
		dirs = append(dirs, "/lib/"+ma, "/usr/lib/"+ma)
	}
	return dirs
}

func glibcFloor(f *elf.File) string {
	needs, err := f.DynamicVersionNeeds()
	if err != nil {
		return ""
	}
	floor := ""
	for _, n := range needs {
		if !glibc.Excluded(n.Name) {
			continue
		}
		for _, d := range n.Needs {
			if v, ok := glibc.Version(d.Dep); ok && glibc.Less(floor, v) {
				floor = v
			}
		}
	}
	return floor
}

func closureHash(r Result) string {
	var lines []string
	for _, l := range r.Lib {
		lines = append(lines, fmt.Sprintf("lib %s %s %d", l.Soname, l.Path, l.Size))
	}
	for _, l := range r.Deps {
		lines = append(lines, fmt.Sprintf("deps %s %s %d", l.Soname, l.Path, l.Size))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func setOf(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
