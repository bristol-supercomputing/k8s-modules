package dra

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWriteSpec(t *testing.T) {
	dir := t.TempDir()
	edits := map[string]*ContainerEdits{
		"container-0-request-0": {Env: []string{"BRICS_MODULE_X=x-1;rank=10;src=image;mount=/opt/modules/x/1"}},
		"container-0-request-1": nil,
	}
	if err := WriteSpec(dir, "cuid", edits); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(SpecPath(dir, "cuid"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["cdiVersion"] != "0.6.0" || got["kind"] != CDIKind {
		t.Errorf("header = %v", got)
	}
	devs := got["devices"].([]any)
	if len(devs) != 1 || devs[0].(map[string]any)["name"] != "cuid-container-0-request-0" {
		t.Errorf("devices = %v", devs)
	}
	if CDIDeviceID("cuid", "container-0-request-0") != "modules.isambard.ac.uk/module=cuid-container-0-request-0" {
		t.Error("CDIDeviceID")
	}
	// Only the spec itself is left behind: no temp files for the CDI cache to read.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != filepath.Base(SpecPath(dir, "cuid")) {
		t.Errorf("dir = %v", entries)
	}
}

func TestWriteSpecIdempotent(t *testing.T) {
	dir := t.TempDir()
	edits := map[string]*ContainerEdits{"r": {Env: []string{"A=1"}}}
	_ = WriteSpec(dir, "u", edits)
	a, _ := os.ReadFile(SpecPath(dir, "u"))
	_ = WriteSpec(dir, "u", edits)
	b, _ := os.ReadFile(SpecPath(dir, "u"))
	if !reflect.DeepEqual(a, b) {
		t.Error("rewriting must produce the same file")
	}
}

func TestRemoveSpec(t *testing.T) {
	dir := t.TempDir()
	_ = WriteSpec(dir, "u", map[string]*ContainerEdits{"r": {Env: []string{"A=1"}}})
	if err := RemoveSpec(dir, "u"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSpec(dir, "u"); err != nil {
		t.Errorf("second remove must be a no-op: %v", err)
	}
}
