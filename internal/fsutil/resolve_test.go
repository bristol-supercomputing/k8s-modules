package fsutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveIn(t *testing.T) {
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "usr/lib/x"), 0o755))
	must(os.WriteFile(filepath.Join(root, "usr/lib/x/libreal.so.1.2"), []byte("elf"), 0o644))
	must(os.Symlink("libreal.so.1.2", filepath.Join(root, "usr/lib/x/libreal.so.1")))
	must(os.Symlink("usr/lib", filepath.Join(root, "lib")))                                // relative dir link
	must(os.Symlink("/usr/lib/x/libreal.so.1", filepath.Join(root, "usr/lib/abs.so")))     // absolute: stays in root
	must(os.Symlink("../../../../etc/passwd", filepath.Join(root, "usr/lib/x/escape.so"))) // climbs above root
	must(os.Symlink("loop2", filepath.Join(root, "loop1")))
	must(os.Symlink("loop1", filepath.Join(root, "loop2")))

	cases := []struct{ in, want string }{
		{"/usr/lib/x/libreal.so.1", "/usr/lib/x/libreal.so.1.2"},
		{"/lib/x/libreal.so.1", "/usr/lib/x/libreal.so.1.2"},
		{"/usr/lib/abs.so", "/usr/lib/x/libreal.so.1.2"},
		{"/usr/lib/x/../x/libreal.so.1.2", "/usr/lib/x/libreal.so.1.2"},
	}
	for _, c := range cases {
		got, err := ResolveIn(root, c.in)
		if err != nil || got != c.want {
			t.Errorf("ResolveIn(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if _, err := ResolveIn(root, "/usr/lib/x/escape.so"); !errors.Is(err, ErrEscape) {
		t.Errorf("escape: err = %v, want ErrEscape", err)
	}
	if _, err := ResolveIn(root, "/missing/lib.so"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing: err = %v, want ErrNotExist", err)
	}
	if _, err := ResolveIn(root, "/loop1"); err == nil {
		t.Error("symlink loop must fail")
	}
	if Host(root, "/usr/lib") != filepath.Join(root, "usr/lib") {
		t.Error("Host join")
	}
}
