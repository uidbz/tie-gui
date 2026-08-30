// Package fs provides the filesystem-provider abstraction behind tie-fm. Each
// URI scheme (local "file:"/bare paths, "tie:", and "mtp:") is served by a
// FileSystem implementation, so the UI navigates and lists uniformly regardless
// of backend.
package fs

import (
	"io"
	"strings"
	"time"
)

// Entry is one row in a directory listing, backend-agnostic.
type Entry struct {
	Name    string
	Path    string // full tie-fm URI used for navigation (e.g. "/home/x" or "tie:/music")
	IsDir   bool
	Size    int64
	ModTime time.Time

	// Tie-only fields; zero for local entries.
	Hash string   // tag key: a file's content hash, or a directory's DirUID
	Tags []string // populated lazily
}

// FileSystem lists and materializes entries for one URI scheme.
type FileSystem interface {
	Scheme() string
	List(path string) ([]Entry, error)
	// Materialize returns a real on-disk path for opening the entry in an
	// external program, downloading remote content to a temp file when needed.
	Materialize(e Entry) (string, error)
}

// TagStore is implemented by backends that support tagging (tie). The UI type-
// asserts a FileSystem to TagStore to decide whether to show the tag panel.
type TagStore interface {
	GetTags(e Entry) ([]string, error)
	SetTags(e Entry, tags []string) error
	ListAllTags() ([]string, error)
	// FilesWithTags returns entries matching the include/exclude tag sets and
	// the total match count (for pagination).
	FilesWithTags(include, exclude []string, offset, limit int) ([]Entry, int, error)
	CoTags(include, exclude []string) ([]string, error)
	// Favorite tags are a user-curated set of pinned tags stored in the tie
	// collection (shared with other tie clients such as imgview).
	ListFavoriteTags() ([]string, error)
	AddFavoriteTag(tag string) error
	RemoveFavoriteTag(tag string) error
}

// StatInfo is a backend-neutral metadata summary for one entry, rendered by the
// UI's Properties dialog. It mirrors the fields the tie backend can supply
// without coupling the fs abstraction to the tie client's own types.
type StatInfo struct {
	Kind         string // "file" | "directory" | "archive"
	TieType      string // full classification, e.g. "audio-file"
	Filename     string
	Name         string
	MediaType    string
	Size         int64
	Tags         []string
	TagDate      time.Time
	Meta         map[string]string // media metadata: title/artist/album/year/track
	Paths        []string
	SubDirCount  int
	FileCount    int
	ArchiveCount int
	TotalSize    int64
	VersionCount int
}

// Stater is implemented by backends that can produce a metadata summary for an
// entry. The UI type-asserts a FileSystem to Stater to decide whether to offer
// the Properties action.
type Stater interface {
	Stat(e Entry) (StatInfo, error)
}

// Importer is implemented by backends that can accept a local file copied in
// from another scheme (tie). destDir is the target directory's URI in this
// backend; srcPath is a real on-disk path; name is the filename to store.
type Importer interface {
	Import(destDir, srcPath, name string) error
}

// ProgressImporter is an Importer that reports upload progress: progress
// receives a Write for each chunk of bytes stored, so the copy engine can drive
// a byte-level progress bar. Backends that cannot report progress implement
// only Importer.
type ProgressImporter interface {
	ImportWithProgress(destDir, srcPath, name string, progress io.Writer) error
}

// Streamer is implemented by backends whose entries can be opened directly over
// the network without a full local download (e.g. tie files served over HTTP),
// letting media players stream large files instead of waiting on a copy.
type Streamer interface {
	StreamURL(e Entry) (string, error)
}

// DirMaker is implemented by backends that can create a directory. parent is the
// containing directory's URI in this backend; name is the new directory's name.
type DirMaker interface {
	Mkdir(parent, name string) error
}

const (
	tieScheme = "tie:"
	mtpScheme = "mtp:"
)

// SchemeOf reports the scheme of a tie-fm path: "tie" for tie: URIs, "mtp" for
// mtp: URIs, otherwise "file" (bare local paths and file: URIs).
func SchemeOf(path string) string {
	switch {
	case strings.HasPrefix(path, tieScheme):
		return "tie"
	case strings.HasPrefix(path, mtpScheme):
		return "mtp"
	default:
		return "file"
	}
}

// IsTie reports whether path addresses the tie filesystem.
func IsTie(path string) bool { return SchemeOf(path) == "tie" }

// IsLocal reports whether path addresses the local disk (bare/file: paths). A
// non-local path is served by a remote backend (tie, mtp) that must materialize
// content before it can be read as a real file.
func IsLocal(path string) bool { return SchemeOf(path) == "file" }

// Registry resolves a path to the FileSystem that serves its scheme.
type Registry struct {
	local FileSystem
	tie   FileSystem
	mtp   FileSystem
}

func NewRegistry(local, tie FileSystem) *Registry {
	return &Registry{local: local, tie: tie}
}

// SetTie replaces the provider serving tie: paths (used when the user selects a
// different tie config at runtime).
func (r *Registry) SetTie(tie FileSystem) { r.tie = tie }

// SetMTP sets the provider serving mtp: paths (connected phones/media devices).
func (r *Registry) SetMTP(mtp FileSystem) { r.mtp = mtp }

// For returns the provider for path's scheme.
func (r *Registry) For(path string) FileSystem {
	switch SchemeOf(path) {
	case "tie":
		return r.tie
	case "mtp":
		return r.mtp
	default:
		return r.local
	}
}
