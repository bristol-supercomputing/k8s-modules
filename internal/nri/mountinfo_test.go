package nri

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const selfInfo = `22 1 0:21 / / rw - overlay overlay rw
40 22 254:1 /var/lib/brics-test/pvc /pvcs/spack-apps ro,relatime - ext4 /dev/vda1 rw
41 22 0:55 /vol/pv-nfs /pvcs/nfs\040claim ro - nfs4 srv:/export rw
`

const hostInfo = `1 0 254:1 / / rw - ext4 /dev/vda1 rw
30 1 254:1 /var/lib /var/lib rw - ext4 /dev/vda1 rw
50 1 0:55 / /var/lib/kubelet/pods/u/volumes/kubernetes.io~nfs/pv-nfs rw - nfs4 srv:/export rw
`

func TestHostPathFor(t *testing.T) {
	self, err := ParseMountInfo([]byte(selfInfo))
	if err != nil {
		t.Fatal(err)
	}
	host, err := ParseMountInfo([]byte(hostInfo))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"/pvcs/spack-apps": "/var/lib/brics-test/pvc", // longest matching root (/var/lib) wins
		"/pvcs/nfs claim":  "/var/lib/kubelet/pods/u/volumes/kubernetes.io~nfs/pv-nfs/vol/pv-nfs",
	}
	for mp, want := range cases {
		if got, err := HostPathFor(self, host, mp); err != nil || got != want {
			t.Errorf("HostPathFor(%q) = %q, %v; want %q", mp, got, err, want)
		}
	}
	if _, err := HostPathFor(self, host, "/pvcs/absent"); err == nil {
		t.Error("absent mount must fail")
	}
	if _, err := HostPathFor(self, nil, "/pvcs/spack-apps"); err == nil {
		t.Error("no host match must fail")
	}
}

func TestMountinfoPVCs(t *testing.T) {
	dir := t.TempDir()
	s, h := filepath.Join(dir, "self"), filepath.Join(dir, "host")
	_ = os.WriteFile(s, []byte(selfInfo), 0o644)
	_ = os.WriteFile(h, []byte(hostInfo), 0o644)
	p := &MountinfoPVCs{Claims: map[string]bool{"spack-apps": true}, Dir: "/pvcs", SelfMountinfo: s, HostMountinfo: h}
	if got, err := p.HostPath("spack-apps"); err != nil || got != "/var/lib/brics-test/pvc" {
		t.Errorf("HostPath = %q, %v", got, err)
	}
	if _, err := p.HostPath("other"); !errors.Is(err, ErrClaimNotMounted) {
		t.Errorf("unconfigured claim: %v", err)
	}
}
