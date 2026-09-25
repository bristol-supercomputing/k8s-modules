package discovery

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var hostroot = filepath.Join("testdata", "hostroot")

func fixtureArch(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(hostroot, "ARCH"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func otherArch(a string) string {
	if a == "arm64" {
		return "amd64"
	}
	return "arm64"
}

func TestParseCacheFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(hostroot, "etc", "ld.so.cache"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseCache(data, fixtureArch(t))
	if err != nil {
		t.Fatal(err)
	}
	for so, want := range map[string]string{
		"libfakefabric.so.1": "/usr/lib/brics-test/libfakefabric.so.1",
		"libfakedep.so.1":    "/usr/lib/brics-test/libfakedep.so.1",
	} {
		if c[so] != want {
			t.Errorf("cache[%s] = %q, want %q", so, c[so], want)
		}
	}
	if _, ok := c["libsubdep.so.1"]; ok {
		t.Error("libsubdep lives only in sub/ and must not be cached")
	}
	other, err := ParseCache(data, otherArch(fixtureArch(t)))
	if err != nil || len(other) != 0 {
		t.Errorf("other arch must yield no entries: %v %v", other, err)
	}
}

// synthetic builds a new-format cache, optionally behind an old-format prefix.
func synthetic(withOld bool, entries ...[3]any) []byte {
	var strs bytes.Buffer
	type ent struct {
		flags      int32
		key, value uint32
	}
	headerLen := 48 + 24*len(entries)
	var es []ent
	for _, e := range entries {
		k := uint32(headerLen + strs.Len())
		strs.WriteString(e[0].(string) + "\x00")
		v := uint32(headerLen + strs.Len())
		strs.WriteString(e[1].(string) + "\x00")
		es = append(es, ent{e[2].(int32), k, v})
	}
	var b bytes.Buffer
	b.WriteString(newMagic)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(entries)))
	_ = binary.Write(&b, binary.LittleEndian, uint32(strs.Len()))
	b.Write(make([]byte, 20)) // flags, padding, extension offset, unused
	for _, e := range es {
		_ = binary.Write(&b, binary.LittleEndian, e.flags)
		_ = binary.Write(&b, binary.LittleEndian, e.key)
		_ = binary.Write(&b, binary.LittleEndian, e.value)
		b.Write(make([]byte, 12)) // osversion, hwcap
	}
	b.Write(strs.Bytes())
	if !withOld {
		return b.Bytes()
	}
	var o bytes.Buffer
	o.WriteString(oldMagic + "\x00")
	_ = binary.Write(&o, binary.LittleEndian, uint32(0))
	for o.Len()%8 != 0 {
		o.WriteByte(0)
	}
	return append(o.Bytes(), b.Bytes()...)
}

func TestParseCacheFirstEntryWinsAndOldPrefix(t *testing.T) {
	aarch := int32(flagELFLibc6 | flagAArch64Lib64)
	for _, withOld := range []bool{false, true} {
		data := synthetic(withOld,
			[3]any{"libx.so.1", "/first/libx.so.1", aarch},
			[3]any{"libx.so.1", "/second/libx.so.1", aarch},
			[3]any{"liby.so.1", "/x86/liby.so.1", int32(flagELFLibc6 | flagX8664Lib64)},
		)
		c, err := ParseCache(data, "arm64")
		if err != nil {
			t.Fatalf("withOld=%v: %v", withOld, err)
		}
		if c["libx.so.1"] != "/first/libx.so.1" || len(c) != 1 {
			t.Errorf("withOld=%v: cache = %v", withOld, c)
		}
	}
}

func TestParseCacheRejectsGarbage(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("not a cache"), []byte(newMagic)} {
		if _, err := ParseCache(data, "arm64"); err == nil {
			t.Errorf("ParseCache(%q) must fail", data)
		}
	}
	if _, err := ParseCache(synthetic(false), "riscv64"); err == nil {
		t.Error("unsupported arch must fail")
	}
}
