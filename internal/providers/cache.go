package providers

import (
	"encoding/json"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
)

// cacheVersion is bumped whenever the on-disk format or the tagging/resolution logic
// changes in a way that would make a stored index stale. A mismatch forces a rebuild.
const cacheVersion = 6

type resolvedImport struct {
	targetFile string
	targetName string
}

type diskImport struct {
	TargetFile string `json:"tf"`
	TargetName string `json:"tn"`
}

// diskRef/diskFile/diskIndex are the persisted form of the per-file tag data. The types
// are unexported (not API surface); encoding/json serializes their exported fields.
type diskRef struct {
	Caller string `json:"c"`
	File   string `json:"f"`
	Callee string `json:"n"`
	Recv   string `json:"recv,omitempty"`
	Bare   bool   `json:"bare,omitempty"`
}

type diskFile struct {
	Hash    string                `json:"h"`
	Lang    string                `json:"l"`
	Defs    []*Symbol             `json:"d"`
	Refs    []diskRef             `json:"r"`
	Imports map[string]diskImport `json:"i,omitempty"`
}

type diskIndex struct {
	Version int                 `json:"v"`
	Files   map[string]diskFile `json:"files"`
}

// fileData is the in-memory per-file tag result (from a fresh tag or the cache).
type fileData struct {
	lang    string
	defs    []*Symbol
	refs    []rawRef
	imports map[string]resolvedImport
}

// hashBytes is a fast non-cryptographic content fingerprint; collisions are
// astronomically unlikely for change detection and the cost is far below parsing.
func hashBytes(b []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return strconv.FormatUint(h.Sum64(), 16)
}

func toDiskRefs(refs []rawRef) []diskRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]diskRef, len(refs))
	for i, r := range refs {
		out[i] = diskRef{
			Caller: r.callerQName,
			File:   r.callerFile,
			Callee: r.calleeName,
			Recv:   r.calleeRecv,
			Bare:   r.allowBare,
		}
	}
	return out
}

func fromDiskRefs(refs []diskRef) []rawRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]rawRef, len(refs))
	for i, r := range refs {
		out[i] = rawRef{
			callerQName: r.Caller,
			callerFile:  r.File,
			calleeName:  r.Callee,
			calleeRecv:  r.Recv,
			allowBare:   r.Bare,
		}
	}
	return out
}

func toDiskImports(imports map[string]resolvedImport) map[string]diskImport {
	if len(imports) == 0 {
		return nil
	}
	out := make(map[string]diskImport, len(imports))
	for k, v := range imports {
		out[k] = diskImport{
			TargetFile: v.targetFile,
			TargetName: v.targetName,
		}
	}
	return out
}

func fromDiskImports(imports map[string]diskImport) map[string]resolvedImport {
	if len(imports) == 0 {
		return nil
	}
	out := make(map[string]resolvedImport, len(imports))
	for k, v := range imports {
		out[k] = resolvedImport{
			targetFile: v.TargetFile,
			targetName: v.TargetName,
		}
	}
	return out
}

// loadDiskIndex reads a persisted index, returning nil on any problem (missing,
// unreadable, malformed, or stale version) so the caller falls back to a full rebuild
// rather than trusting corrupt or outdated data.
func loadDiskIndex(path string) *diskIndex {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var di diskIndex
	if err := json.Unmarshal(data, &di); err != nil {
		return nil
	}
	if di.Version != cacheVersion || di.Files == nil {
		return nil
	}
	return &di
}

// saveDiskIndex writes the index atomically (temp file + rename). Every failure is
// swallowed: the cache is an optimization, never a correctness dependency.
func saveDiskIndex(path string, di *diskIndex) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(di)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
