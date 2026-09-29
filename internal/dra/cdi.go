package dra

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"k8s.io/apimachinery/pkg/types"
)

// CDIKind is the vendor/class of every CDI device this driver writes.
const CDIKind = "modules.isambard.ac.uk/module"

type cdiSpec struct {
	Version string      `json:"cdiVersion"`
	Kind    string      `json:"kind"`
	Devices []cdiDevice `json:"devices"`
}

type cdiDevice struct {
	Name           string          `json:"name"`
	ContainerEdits *ContainerEdits `json:"containerEdits"`
}

// CDIDeviceID names the CDI device for one request of a claim. Claim UIDs
// are never reused, so the CDI cache never serves a stale device.
func CDIDeviceID(claim types.UID, request string) string {
	return CDIKind + "=" + string(claim) + "-" + request
}

// SpecPath is the claim's CDI spec file.
func SpecPath(dir string, claim types.UID) string {
	return filepath.Join(dir, "modules.isambard.ac.uk-"+string(claim)+".json")
}

// WriteSpec atomically writes one CDI device per request with edits. The
// temp file's .tmp extension keeps the CDI cache from reading it.
func WriteSpec(dir string, claim types.UID, edits map[string]*ContainerEdits) error {
	s := cdiSpec{Version: "0.6.0", Kind: CDIKind}
	reqs := make([]string, 0, len(edits))
	for r := range edits {
		reqs = append(reqs, r)
	}
	sort.Strings(reqs)
	for _, r := range reqs {
		if edits[r] != nil {
			s.Devices = append(s.Devices, cdiDevice{Name: string(claim) + "-" + r, ContainerEdits: edits[r]})
		}
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".modules-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), SpecPath(dir, claim))
}

// RemoveSpec deletes the claim's CDI spec; a missing file is not an error.
func RemoveSpec(dir string, claim types.UID) error {
	if err := os.Remove(SpecPath(dir, claim)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
