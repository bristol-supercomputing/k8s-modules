package nri

import (
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
)

// ErrClaimNotMounted reports a pvc module whose claim the DaemonSet does
// not mount.
var ErrClaimNotMounted = errors.New("not mounted by module-nri")

// MountInfo is one /proc/<pid>/mountinfo line.
type MountInfo struct {
	MajMin     string
	Root       string
	MountPoint string
}

// ParseMountInfo parses mountinfo, unescaping octal sequences such as \040.
func ParseMountInfo(data []byte) ([]MountInfo, error) {
	var out []MountInfo
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 5 {
			return nil, fmt.Errorf("malformed mountinfo line %q", line)
		}
		out = append(out, MountInfo{MajMin: f[2], Root: unescape(f[3]), MountPoint: unescape(f[4])})
	}
	return out, nil
}

func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// HostPathFor maps our mount at mountPoint to a path in the host's mount
// namespace: the host mount of the same device whose root is the longest
// prefix of ours, plus the remainder of our root.
func HostPathFor(self, host []MountInfo, mountPoint string) (string, error) {
	var mine *MountInfo
	for i := range self {
		if self[i].MountPoint == mountPoint {
			mine = &self[i] // the last mount at a path is the visible one
		}
	}
	if mine == nil {
		return "", fmt.Errorf("%s is not a mount point", mountPoint)
	}
	var best *MountInfo
	for i := range host {
		h := &host[i]
		if h.MajMin != mine.MajMin || !within(mine.Root, h.Root) {
			continue
		}
		if best == nil || len(h.Root) > len(best.Root) {
			best = h
		}
	}
	if best == nil {
		return "", fmt.Errorf("no host mount of device %s contains %s", mine.MajMin, mine.Root)
	}
	rest := strings.TrimPrefix(mine.Root, best.Root)
	return path.Join(best.MountPoint, rest), nil
}

func within(p, root string) bool {
	return root == "/" || p == root || strings.HasPrefix(p, root+"/")
}

// MountinfoPVCs resolves claims mounted at Dir/<claim> via mountinfo.
type MountinfoPVCs struct {
	Claims        map[string]bool
	Dir           string
	SelfMountinfo string
	HostMountinfo string
}

func (p *MountinfoPVCs) HostPath(claim string) (string, error) {
	if !p.Claims[claim] {
		return "", fmt.Errorf("pvc claim %s is %w; add it to nri.pvcs", claim, ErrClaimNotMounted)
	}
	self, err := readMountInfo(p.SelfMountinfo)
	if err != nil {
		return "", err
	}
	host, err := readMountInfo(p.HostMountinfo)
	if err != nil {
		return "", err
	}
	hp, err := HostPathFor(self, host, path.Join(p.Dir, claim))
	if err != nil {
		return "", fmt.Errorf("cannot locate host path for pvc claim %s: %w", claim, err)
	}
	return hp, nil
}

func readMountInfo(file string) ([]MountInfo, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return ParseMountInfo(data)
}
