package nri

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bristol-supercomputing/k8s-modules/internal/fsutil"
)

const selfInfo = `22 1 0:21 / / rw - overlay overlay rw
40 22 254:1 /var/lib/brics-test/pvc /pvcs/spack-apps ro,relatime - ext4 /dev/vda1 rw
41 22 0:55 /vol/pv-nfs /pvcs/nfs\040claim ro - nfs4 srv:/export rw
`

const hostInfo = `1 0 254:1 / / rw - ext4 /dev/vda1 rw
30 1 254:1 /var/lib /var/lib rw - ext4 /dev/vda1 rw
50 1 0:55 / /var/lib/kubelet/pods/other/volumes/kubernetes.io~nfs/pv-nfs rw - nfs4 srv:/export rw
51 1 0:55 / /var/lib/kubelet/pods/me/volumes/kubernetes.io~nfs/pv-nfs rw - nfs4 srv:/export rw
`

func parse(t *testing.T, s string) []MountInfo {
	t.Helper()
	m, err := ParseMountInfo([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestHostPathFor(t *testing.T) {
	self, host := parse(t, selfInfo), parse(t, hostInfo)
	cases := []struct{ mp, prefer, want string }{
		{"/pvcs/spack-apps", "", "/var/lib/brics-test/pvc"}, // longest matching root (/var/lib) wins
		{"/pvcs/nfs claim", "/var/lib/kubelet/pods/me/", "/var/lib/kubelet/pods/me/volumes/kubernetes.io~nfs/pv-nfs/vol/pv-nfs"},
		// Without a preference the first equally good match wins; that can be
		// another pod's mount, which may be torn down at any time.
		{"/pvcs/nfs claim", "", "/var/lib/kubelet/pods/other/volumes/kubernetes.io~nfs/pv-nfs/vol/pv-nfs"},
	}
	for _, c := range cases {
		if got, err := HostPathFor(self, host, c.mp, c.prefer); err != nil || got != c.want {
			t.Errorf("HostPathFor(%q, prefer %q) = %q, %v; want %q", c.mp, c.prefer, got, err, c.want)
		}
	}
	if _, err := HostPathFor(self, host, "/pvcs/absent", ""); err == nil {
		t.Error("absent mount must fail")
	}
	if _, err := HostPathFor(self, nil, "/pvcs/spack-apps", ""); err == nil {
		t.Error("no host match must fail")
	}
}

func TestMountinfoPVCs(t *testing.T) {
	dir := t.TempDir()
	pv := filepath.Join(dir, "spack-apps")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(pv, "nccl/1"), 0o755))
	must(os.WriteFile(filepath.Join(pv, "afile"), nil, 0o644))
	must(os.Symlink("../../../../../..", filepath.Join(pv, "escape")))
	must(os.Symlink("/", filepath.Join(pv, "abs")))
	s, h := filepath.Join(dir, "self"), filepath.Join(dir, "host")
	must(os.WriteFile(s, []byte(strings.ReplaceAll(selfInfo, "/pvcs/spack-apps", pv)), 0o644))
	must(os.WriteFile(h, []byte(hostInfo), 0o644))
	p := &MountinfoPVCs{Claims: map[string]bool{"spack-apps": true}, Dir: dir, SelfMountinfo: s, HostMountinfo: h, PodUID: "me"}

	for sub, want := range map[string]string{
		"nccl/1": "/var/lib/brics-test/pvc/nccl/1",
		".":      "/var/lib/brics-test/pvc",
		"abs":    "/var/lib/brics-test/pvc", // absolute links resolve inside the PV, never on the host
	} {
		if got, err := p.HostPath("spack-apps", sub); err != nil || got != want {
			t.Errorf("HostPath(%q) = %q, %v; want %q", sub, got, err, want)
		}
	}
	if _, err := p.HostPath("spack-apps", "escape"); !errors.Is(err, fsutil.ErrEscape) {
		t.Errorf("symlink climbing out of the PV: err = %v, want ErrEscape", err)
	}
	if _, err := p.HostPath("spack-apps", "afile"); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("file subPath: err = %v", err)
	}
	if _, err := p.HostPath("other", "x"); !errors.Is(err, ErrClaimNotMounted) {
		t.Errorf("unconfigured claim: %v", err)
	}
}
