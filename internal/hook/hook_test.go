package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct{ rootfs, ldconfig string }

func (f *fakeRunner) Run(rootfs, ldconfig string) error {
	f.rootfs, f.ldconfig = rootfs, ldconfig
	return nil
}

// rootfs builds a minimal glibc-2.31 image tree.
func rootfs(t *testing.T) string {
	t.Helper()
	r := t.TempDir()
	for _, d := range []string{"etc/ld.so.conf.d", "usr/lib", "usr/sbin", "lib64"} {
		if err := os.MkdirAll(filepath.Join(r, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	libc, err := os.ReadFile(filepath.Join("testdata", "libc231", "libc.so.6"))
	if err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(r, "usr/lib/libc.so.6"), libc, 0o755))
	must(t, os.WriteFile(filepath.Join(r, "usr/sbin/ldconfig"), []byte("#!"), 0o755))
	must(t, os.Symlink("usr/sbin", filepath.Join(r, "sbin")))
	must(t, os.WriteFile(filepath.Join(r, "etc/ld.so.conf"), []byte("include /etc/ld.so.conf.d/*.conf\n"), 0o644))
	return r
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestApplyWritesConfAndRunsLdconfig(t *testing.T) {
	r := rootfs(t)
	run := &fakeRunner{}
	err := Apply(r, Options{Deps: []string{"/opt/modules/fab/host/deps"}, Glibc: map[string]string{"fab-host": "2.28"}, Arch: "arm64", Ldconfig: "/opt/brics-modules/bin/ldconfig"}, run)
	if err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(filepath.Join(r, "etc/ld.so.conf.d/zz-brics-modules.conf"))
	if want := "/lib64\n/usr/lib\n/opt/modules/fab/host/deps\n"; string(conf) != want {
		t.Errorf("conf = %q, want %q (trusted dirs that exist, then deps)", conf, want)
	}
	if run.rootfs != r || run.ldconfig != "/opt/brics-modules/bin/ldconfig" {
		t.Errorf("runner got %q %q", run.rootfs, run.ldconfig)
	}
	main, _ := os.ReadFile(filepath.Join(r, "etc/ld.so.conf"))
	if strings.Count(string(main), "include") != 1 {
		t.Errorf("existing include must not be duplicated: %q", main)
	}
}

func TestApplyAddsIncludeWhenMissing(t *testing.T) {
	r := rootfs(t)
	must(t, os.WriteFile(filepath.Join(r, "etc/ld.so.conf"), []byte("/usr/local/lib"), 0o644))
	must(t, Apply(r, Options{Deps: []string{"/d"}}, &fakeRunner{}))
	main, _ := os.ReadFile(filepath.Join(r, "etc/ld.so.conf"))
	if string(main) != "/usr/local/lib\ninclude /etc/ld.so.conf.d/*.conf\n" {
		t.Errorf("ld.so.conf = %q", main)
	}
}

func TestApplyGlibcTooOld(t *testing.T) {
	err := Apply(rootfs(t), Options{Deps: []string{"/d"}, Glibc: map[string]string{"fab-host": "2.36"}, Arch: "arm64"}, &fakeRunner{})
	want := "module fab-host needs GLIBC_2.36; the image's libc.so.6 provides up to GLIBC_2.31"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestApplyErrors(t *testing.T) {
	noLibc := rootfs(t)
	must(t, os.Remove(filepath.Join(noLibc, "usr/lib/libc.so.6")))
	readOnly := rootfs(t)
	must(t, os.Chmod(filepath.Join(readOnly, "etc/ld.so.conf.d"), 0o555))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(readOnly, "etc/ld.so.conf.d"), 0o755) })

	cases := map[string]struct {
		root string
		want string
	}{
		"no libc":   {noLibc, "image has no glibc libc.so.6; host modules require a glibc-based image"},
		"read-only": {readOnly, "host modules need a writable /etc in the image (readOnlyRootFilesystem is not supported)"},
	}
	for name, c := range cases {
		err := Apply(c.root, Options{Deps: []string{"/d"}, Glibc: map[string]string{"m": "2.17"}, Arch: "arm64"}, &fakeRunner{})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestRootFromState(t *testing.T) {
	bundle := t.TempDir()
	must(t, os.WriteFile(filepath.Join(bundle, "config.json"), []byte(`{"root":{"path":"rootfs"}}`), 0o644))
	state, _ := json.Marshal(map[string]string{"bundle": bundle})
	got, err := RootFromState(state)
	if err != nil || got != filepath.Join(bundle, "rootfs") {
		t.Errorf("RootFromState = %q, %v", got, err)
	}
	must(t, os.WriteFile(filepath.Join(bundle, "config.json"), []byte(`{"root":{"path":"/abs/root"}}`), 0o644))
	if got, _ := RootFromState(state); got != "/abs/root" {
		t.Errorf("absolute root = %q", got)
	}
	if _, err := RootFromState([]byte(`{}`)); err == nil {
		t.Error("missing bundle must fail")
	}
}

func TestApplyNeverRunsImageLdconfig(t *testing.T) {
	r := rootfs(t)
	must(t, os.Remove(filepath.Join(r, "usr/sbin/ldconfig")))
	run := &fakeRunner{}
	if err := Apply(r, Options{Deps: []string{"/d"}, Ldconfig: "/trusted/ldconfig"}, run); err != nil {
		t.Fatalf("an image without ldconfig must work: %v", err)
	}
	if run.ldconfig != "/trusted/ldconfig" {
		t.Errorf("runner got %q, want the trusted ldconfig", run.ldconfig)
	}
}

// The hook writes into an untrusted rootfs as host root before pivot_root,
// so no image symlink may redirect a write outside the rootfs.
func TestApplyDoesNotWriteThroughImageSymlinks(t *testing.T) {
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	cases := map[string]func(r string){
		"conf file is an absolute symlink": func(r string) {
			must(t, os.Symlink(victim, filepath.Join(r, "etc/ld.so.conf.d", ConfName)))
		},
		"conf.d is an absolute symlink": func(r string) {
			must(t, os.RemoveAll(filepath.Join(r, "etc/ld.so.conf.d")))
			must(t, os.Symlink(outside, filepath.Join(r, "etc/ld.so.conf.d")))
		},
		"conf.d is a dangling absolute symlink": func(r string) {
			must(t, os.RemoveAll(filepath.Join(r, "etc/ld.so.conf.d")))
			must(t, os.Symlink(filepath.Join(outside, "newdir"), filepath.Join(r, "etc/ld.so.conf.d")))
		},
		"ld.so.conf is an absolute symlink": func(r string) {
			must(t, os.Remove(filepath.Join(r, "etc/ld.so.conf")))
			must(t, os.Symlink(victim, filepath.Join(r, "etc/ld.so.conf")))
		},
		"conf file is a relative symlink climbing out": func(r string) {
			rel, err := filepath.Rel(filepath.Join(r, "etc/ld.so.conf.d"), victim)
			must(t, err)
			must(t, os.Symlink(rel, filepath.Join(r, "etc/ld.so.conf.d", ConfName)))
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			must(t, os.WriteFile(victim, []byte("host file\n"), 0o644))
			r := rootfs(t)
			setup(r)
			_ = Apply(r, Options{Deps: []string{"/d"}, Ldconfig: "/trusted/ldconfig"}, &fakeRunner{})
			if b, _ := os.ReadFile(victim); string(b) != "host file\n" {
				t.Errorf("host file outside the rootfs was modified: %q", b)
			}
			entries, _ := os.ReadDir(outside)
			for _, e := range entries {
				if e.Name() != "victim" {
					t.Errorf("hook created %s outside the rootfs", e.Name())
				}
			}
		})
	}
}

func TestTrustedRunner(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := filepath.Join(dir, "ldconfig")
	must(t, os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\" > "+argsFile+"\n"), 0o755))
	if err := (TrustedRunner{Timeout: 5 * time.Second}).Run("/some/rootfs", script); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(argsFile); string(b) != "-X -r /some/rootfs\n" {
		t.Errorf("ldconfig args = %q, want -X -r /some/rootfs", b)
	}
	slow := filepath.Join(dir, "slow")
	must(t, os.WriteFile(slow, []byte("#!/bin/sh\nsleep 5\n"), 0o755))
	if err := (TrustedRunner{Timeout: 200 * time.Millisecond}).Run("/r", slow); err == nil {
		t.Error("a hanging ldconfig must time out")
	}
}
