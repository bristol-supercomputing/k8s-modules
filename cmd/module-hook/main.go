// Command module-hook is the module system's OCI createContainer hook.
// module-nri adds it to containers that load host modules.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bristol-supercomputing/k8s-modules/internal/hook"
)

type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

func main() {
	var deps, glibcs repeated
	fs := flag.NewFlagSet("module-hook", flag.ContinueOnError)
	ldconfig := fs.String("ldconfig", besideSelf("ldconfig"), "trusted static ldconfig (never the image's)")
	fs.Var(&deps, "deps", "container path of a host module deps directory (repeatable)")
	fs.Var(&glibcs, "glibc", "<module>=<version> the module needs (repeatable)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if err := run(os.Stdin, *ldconfig, deps, glibcs); err != nil {
		fmt.Fprintln(os.Stderr, "module-hook:", err)
		os.Exit(1)
	}
}

func besideSelf(name string) string {
	exe, err := os.Executable()
	if err != nil {
		return name
	}
	return filepath.Join(filepath.Dir(exe), name)
}

func run(stdin io.Reader, ldconfig string, deps, glibcs []string) error {
	state, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	rootfs, err := hook.RootFromState(state)
	if err != nil {
		return err
	}
	req := map[string]string{}
	for _, g := range glibcs {
		m, v, ok := strings.Cut(g, "=")
		if !ok || m == "" || v == "" {
			return fmt.Errorf("--glibc %q: want <module>=<version>", g)
		}
		req[m] = v
	}
	return hook.Apply(rootfs, hook.Options{Deps: deps, Glibc: req, Arch: runtime.GOARCH, Ldconfig: ldconfig},
		hook.TrustedRunner{Timeout: 30 * time.Second})
}
