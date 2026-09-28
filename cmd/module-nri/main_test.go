package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallHookCopiesHookAndTrustedLdconfig(t *testing.T) {
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "bin")
	for _, f := range []string{"module-hook", "ldconfig"} {
		if err := os.WriteFile(filepath.Join(from, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := installHook([]string{"--from-dir=" + from, "--to=" + to}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"module-hook", "ldconfig"} {
		fi, err := os.Stat(filepath.Join(to, f))
		if err != nil || fi.Mode().Perm() != 0o755 {
			t.Errorf("%s: %v %v", f, fi, err)
		}
	}
}
