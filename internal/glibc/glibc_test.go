package glibc

import "testing"

func TestVersion(t *testing.T) {
	for in, want := range map[string]string{"GLIBC_2.36": "2.36", "GLIBC_2.2.5": "2.2.5"} {
		if got, ok := Version(in); !ok || got != want {
			t.Errorf("Version(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"GLIBC_PRIVATE", "GLIBCXX_3.4", "GLIBC_", "GLIBC_2.x"} {
		if _, ok := Version(in); ok {
			t.Errorf("Version(%q) must be false", in)
		}
	}
}

func TestLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"", "2.17", true}, {"2.17", "", false}, {"2.9", "2.17", true}, {"2.17", "2.9", false},
		{"2.2.5", "2.3", true}, {"2.36", "2.36", false}, {"2.36", "2.36.1", true},
	}
	for _, c := range cases {
		if got := Less(c.a, c.b); got != c.want {
			t.Errorf("Less(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestExcluded(t *testing.T) {
	for _, s := range []string{"libc.so.6", "libm.so.6", "libpthread.so.0", "libdl.so.2", "librt.so.1", "ld-linux-aarch64.so.1", "ld-linux-x86-64.so.2"} {
		if !Excluded(s) {
			t.Errorf("%s must be excluded", s)
		}
	}
	if Excluded("libfabric.so.1") || Excluded("libcrypt.so.1") {
		t.Error("non-glibc libraries must not be excluded")
	}
}

func TestMultiarch(t *testing.T) {
	if Multiarch("arm64") != "aarch64-linux-gnu" || Multiarch("amd64") != "x86_64-linux-gnu" || Multiarch("riscv64") != "" {
		t.Error("multiarch names")
	}
}
