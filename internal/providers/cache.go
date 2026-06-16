package providers

import (
	"encoding/json"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
)

// CacheVersion is bumped whenever the on-disk format or the tagging/resolution logic
// changes in a way that would make a stored index stale. A mismatch forces a rebuild.
const CacheVersion = 6

const cacheVersion = CacheVersion

// ResolvedImport maps an import alias to a target file and exported name.
type ResolvedImport struct {
	TargetFile string
	TargetName string
}

// DiskImport is the persisted form of a resolved import alias.
type DiskImport struct {
	TargetFile string `json:"tf"`
	TargetName string `json:"tn"`
}

// DiskRef is the persisted form of a raw call reference.
type DiskRef struct {
	Caller string `json:"c"`
	File   string `json:"f"`
	Callee string `json:"n"`
	Recv   string `json:"recv,omitempty"`
	Bare   bool   `json:"bare,omitempty"`
}

// DiskFile is the persisted per-file tag payload.
type DiskFile struct {
	Hash    string                `json:"h"`
	Lang    string                `json:"l"`
	Defs    []*Symbol             `json:"d"`
	Refs    []DiskRef             `json:"r"`
	Imports map[string]DiskImport `json:"i,omitempty"`
}

// DiskIndex is the on-disk incremental index for a repository.
type DiskIndex struct {
	Version int                 `json:"v"`
	Files   map[string]DiskFile `json:"files"`
}

// FileData is the in-memory per-file tag result (from a fresh tag or the cache).
type FileData struct {
	Lang    string
	Defs    []*Symbol
	Refs    []RawRef
	Imports map[string]ResolvedImport
}

// HashBytes is a fast non-cryptographic content fingerprint.
func HashBytes(b []byte) string {
	return hashBytes(b)
}

func hashBytes(b []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return strconv.FormatUint(h.Sum64(), 16)
}

// ToDiskRefs converts in-memory refs to the disk representation.
func ToDiskRefs(refs []RawRef) []DiskRef {
	return toDiskRefs(refs)
}

func toDiskRefs(refs []RawRef) []DiskRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]DiskRef, len(refs))
	for i, r := range refs {
		out[i] = DiskRef{
			Caller: r.CallerQName,
			File:   r.CallerFile,
			Callee: r.CalleeName,
			Recv:   r.CalleeRecv,
			Bare:   r.AllowBare,
		}
	}
	return out
}

// FromDiskRefs converts disk refs to in-memory refs.
func FromDiskRefs(refs []DiskRef) []RawRef {
	return fromDiskRefs(refs)
}

func fromDiskRefs(refs []DiskRef) []RawRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]RawRef, len(refs))
	for i, r := range refs {
		out[i] = RawRef{
			CallerQName: r.Caller,
			CallerFile:  r.File,
			CalleeName:  r.Callee,
			CalleeRecv:  r.Recv,
			AllowBare:   r.Bare,
		}
	}
	return out
}

// ToDiskImports converts in-memory imports to the disk representation.
func ToDiskImports(imports map[string]ResolvedImport) map[string]DiskImport {
	return toDiskImports(imports)
}

func toDiskImports(imports map[string]ResolvedImport) map[string]DiskImport {
	if len(imports) == 0 {
		return nil
	}
	out := make(map[string]DiskImport, len(imports))
	for k, v := range imports {
		out[k] = DiskImport{TargetFile: v.TargetFile, TargetName: v.TargetName} //nolint:staticcheck // disk JSON tags differ
	}
	return out
}

// FromDiskImports converts disk imports to in-memory imports.
func FromDiskImports(imports map[string]DiskImport) map[string]ResolvedImport {
	return fromDiskImports(imports)
}

func fromDiskImports(imports map[string]DiskImport) map[string]ResolvedImport {
	if len(imports) == 0 {
		return nil
	}
	out := make(map[string]ResolvedImport, len(imports))
	for k, v := range imports {
		out[k] = ResolvedImport{TargetFile: v.TargetFile, TargetName: v.TargetName} //nolint:staticcheck // disk JSON tags differ
	}
	return out
}

// LoadDiskIndex reads a persisted index, returning nil on any problem.
func LoadDiskIndex(path string) *DiskIndex {
	return loadDiskIndex(path)
}

func loadDiskIndex(path string) *DiskIndex {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var di DiskIndex
	if err := json.Unmarshal(data, &di); err != nil {
		return nil
	}
	if di.Version != cacheVersion || di.Files == nil {
		return nil
	}
	return &di
}

// SaveDiskIndex writes the index atomically (temp file + rename).
func SaveDiskIndex(path string, di *DiskIndex) {
	saveDiskIndex(path, di)
}

func saveDiskIndex(path string, di *DiskIndex) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(di)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}
