// Package discovery finds a host module's library closure on a node.
package discovery

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Cache maps sonames to the paths ld.so would load them from, as recorded
// in /etc/ld.so.cache.
type Cache map[string]string

const (
	newMagic = "glibc-ld.so.cache1.1"
	oldMagic = "ld.so-1.7.0"

	headerLen = 48
	entryLen  = 24

	flagELFLibc6     = 0x0003
	flagX8664Lib64   = 0x0300
	flagAArch64Lib64 = 0x0a00
)

var errTruncated = errors.New("ld.so.cache is truncated")

func archFlags(goarch string) (int32, error) {
	switch goarch {
	case "amd64":
		return flagELFLibc6 | flagX8664Lib64, nil
	case "arm64":
		return flagELFLibc6 | flagAArch64Lib64, nil
	}
	return 0, fmt.Errorf("unsupported architecture %q", goarch)
}

// ParseCache reads glibc's "glibc-ld.so.cache1.1" format, alone or after
// the legacy "ld.so-1.7.0" prefix. It keeps entries for goarch without
// hwcap subdirectories. When a soname appears twice, the first entry wins,
// as it does for ld.so.
func ParseCache(data []byte, goarch string) (Cache, error) {
	want, err := archFlags(goarch)
	if err != nil {
		return nil, err
	}
	base := 0
	if bytes.HasPrefix(data, []byte(oldMagic)) {
		if len(data) < 16 {
			return nil, errTruncated
		}
		n := int(binary.LittleEndian.Uint32(data[12:]))
		base = (16 + n*12 + 7) &^ 7
	}
	if len(data) < base+headerLen || string(data[base:base+len(newMagic)]) != newMagic {
		return nil, errors.New("not a glibc-ld.so.cache1.1 file")
	}
	h := data[base:] // string offsets are relative to the new-format header
	n := int(binary.LittleEndian.Uint32(h[20:]))
	if headerLen+n*entryLen > len(h) {
		return nil, errTruncated
	}
	c := Cache{}
	for i := range n {
		e := h[headerLen+i*entryLen:]
		flags := int32(binary.LittleEndian.Uint32(e[0:]))
		if flags != want || binary.LittleEndian.Uint64(e[16:]) != 0 {
			continue
		}
		key, err := cstring(h, binary.LittleEndian.Uint32(e[4:]))
		if err != nil {
			return nil, err
		}
		value, err := cstring(h, binary.LittleEndian.Uint32(e[8:]))
		if err != nil {
			return nil, err
		}
		if _, seen := c[key]; !seen {
			c[key] = value
		}
	}
	return c, nil
}

func cstring(b []byte, off uint32) (string, error) {
	if int(off) >= len(b) {
		return "", errTruncated
	}
	end := bytes.IndexByte(b[off:], 0)
	if end < 0 {
		return "", errTruncated
	}
	return string(b[off : int(off)+end]), nil
}
