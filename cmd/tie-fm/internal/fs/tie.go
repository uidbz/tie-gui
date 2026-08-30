package fs

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/uidbz/tie/client"
)

// TieFS serves the tie tagging filesystem over a tie client. It implements both
// FileSystem and TagStore.
type TieFS struct {
	tc     *client.TieClient
	tmpDir string // lazily created cache for materialized downloads
}

func NewTieFS(tc *client.TieClient) *TieFS { return &TieFS{tc: tc} }

func (t *TieFS) Scheme() string { return "tie" }

// tiePath returns the path portion of a "tie:" URI, always starting with "/".
func tiePath(uri string) string {
	p := strings.TrimPrefix(uri, tieScheme)
	if p == "" {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// tieURI builds a tie-fm "tie:" URI from a path that may carry the tie client's
// internal URI scheme (client.FileURIScheme) or be a bare path.
func tieURI(p string) string {
	p = strings.TrimPrefix(p, client.FileURIScheme)
	if p == "" {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return tieScheme + p
}

func (t *TieFS) List(uri string) ([]Entry, error) {
	uid, err := t.tc.DirUIDFromPath(tiePath(uri))
	if err != nil {
		return nil, err
	}
	if uid == "" {
		// The path is not tied to any directory UID. tie's data model is
		// tag-based: the root (and most paths) have no directory tree, so
		// this is the normal landing state, not an error. Return an empty
		// listing and let the tag-filter panel drive discovery.
		return nil, nil
	}
	dir, err := client.ReadTieDir(t.tc, uid)
	if err != nil {
		return nil, err
	}

	base := tiePath(uri)
	entries := make([]Entry, 0, len(dir.SubDirs)+len(dir.Files)+len(dir.Archives))

	for _, sd := range dir.SubDirs {
		childPath := base
		if len(sd.Paths) > 0 {
			childPath = strings.TrimPrefix(sd.Paths[0], client.FileURIScheme)
		}
		entries = append(entries, Entry{
			Name:  path.Base(childPath),
			Path:  tieURI(childPath),
			IsDir: true,
			// A directory's tag key is its DirUID (not a content hash); tie's
			// GetTags/SetTags accept any subject key, so this makes dirs taggable.
			Hash: string(sd.Uid),
		})
	}
	for _, f := range dir.Files {
		entries = append(entries, Entry{
			Name:    f.Filename,
			Path:    tieURI(path.Join(base, f.Filename)),
			IsDir:   false,
			Size:    int64(f.Size),
			ModTime: f.TagDate,
			Hash:    f.Uid,
		})
	}
	for _, a := range dir.Archives {
		entries = append(entries, Entry{
			Name:    a.Filename,
			Path:    tieURI(path.Join(base, a.Filename)),
			IsDir:   false,
			Size:    int64(a.Size),
			ModTime: a.TagDate,
			Hash:    a.Hash,
		})
	}
	return entries, nil
}

func (t *TieFS) Materialize(e Entry) (string, error) {
	if e.Hash == "" {
		return "", errors.New("tie: entry has no content hash: " + e.Name)
	}
	if t.tmpDir == "" {
		d, err := os.MkdirTemp("", "tie-fm-")
		if err != nil {
			return "", err
		}
		t.tmpDir = d
	}
	dest := filepath.Join(t.tmpDir, e.Hash+"-"+e.Name)
	if _, err := os.Stat(dest); err == nil {
		return dest, nil // already downloaded
	}
	if err := t.tc.Download("", e.Hash, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// Import copies a local file into the tie tree under destDir, creating the
// directory (and any missing ancestors) if needed. The bytes are uploaded and
// the file's triples written via client.WriteFile, which versions any existing
// same-named file into a <name>_prev history. Implements Importer.
func (t *TieFS) Import(destDir, srcPath, name string) error {
	return t.ImportWithProgress(destDir, srcPath, name, nil)
}

// ImportWithProgress is Import that, when progress is non-nil, reports uploaded
// bytes to it for a live progress bar. Implements ProgressImporter.
func (t *TieFS) ImportWithProgress(destDir, srcPath, name string, progress io.Writer) error {
	dirPath := tiePath(destDir)
	uid, err := t.tc.DirUIDFromPath(dirPath)
	if err != nil {
		return err
	}
	if uid == "" {
		if uid, err = t.tc.MkTieDirAll(client.FileURIScheme + dirPath); err != nil {
			return err
		}
	}
	host, err := t.tc.ResolveHost("")
	if err != nil {
		return err
	}
	_, err = t.tc.WriteFileWithProgress(host, "", uid, name, srcPath, nil, progress)
	return err
}

// StreamURL returns the filehost HTTP URL that serves e's raw bytes, so a media
// player can stream it directly instead of downloading first. Implements
// Streamer.
func (t *TieFS) StreamURL(e Entry) (string, error) {
	if e.Hash == "" {
		return "", errors.New("tie: entry has no content hash: " + e.Name)
	}
	host, err := t.tc.ResolveHost("")
	if err != nil {
		return "", err
	}
	return strings.TrimRight(host.URL, "/") + "/" + e.Hash, nil
}

// Mkdir creates a directory (and any missing ancestors) at parent/name in the
// tie tree, establishing its path triples via MkTieDirAll. Implements DirMaker.
func (t *TieFS) Mkdir(parent, name string) error {
	dirPath := path.Join(tiePath(parent), name)
	_, err := t.tc.MkTieDirAll(client.FileURIScheme + dirPath)
	return err
}

// --- TagStore ---

func (t *TieFS) GetTags(e Entry) ([]string, error) {
	if e.Hash == "" {
		return nil, nil
	}
	return client.GetTags(t.tc, e.Hash)
}

func (t *TieFS) SetTags(e Entry, tags []string) error {
	if e.Hash == "" {
		return errors.New("tie: cannot tag an entry without a content hash")
	}
	return client.SetTags(t.tc, e.Hash, tags)
}

func (t *TieFS) ListAllTags() ([]string, error) {
	tags, _, err := t.tc.ListTags(0, -1)
	return tags, err
}

func (t *TieFS) FilesWithTags(include, exclude []string, offset, limit int) ([]Entry, int, error) {
	files, total, err := t.tc.FilesWithTags("", include, exclude, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	entries := make([]Entry, 0, len(files))
	for _, f := range files {
		// A tag query only carries a basename, so the naive "/<name>" path is
		// wrong for anything not at the tie root. For a directory the entry's
		// Hash is its DirUID, so resolve its real stored path — otherwise
		// entering it navigates to a bogus tie:/<name> with no contents. Files
		// open via their content hash, so their display path can stay naive.
		p := tieURI("/" + f.Filename)
		if f.IsDir {
			if dp := t.dirPath(f.Hash); dp != "" {
				p = tieURI(dp)
			}
		}
		entries = append(entries, Entry{
			Name:  f.Filename,
			Path:  p,
			IsDir: f.IsDir,
			Size:  int64(f.Size),
			Hash:  f.Hash,
		})
	}
	return entries, total, nil
}

// dirPath returns the stored (uid,"path") value of a directory DirUID, or ""
// when the directory has no path triple.
func (t *TieFS) dirPath(uid string) string {
	row, err := t.tc.Get(uid)
	if err != nil {
		return ""
	}
	return client.RowFirst(row, client.TiePath.String())
}

func (t *TieFS) CoTags(include, exclude []string) ([]string, error) {
	return t.tc.CoTagsForQuery(include, exclude, "")
}

// Stat implements Stater: it summarizes a tie entry for the Properties dialog.
// The entry's Hash is the client subject key (content hash for files/archives,
// DirUID for directories), so the client's Stat can classify it directly. It
// stays offline (recorded sizes + version history), skipping the filehost HEAD.
func (t *TieFS) Stat(e Entry) (StatInfo, error) {
	if e.Hash == "" {
		return StatInfo{}, errors.New("tie: cannot stat an entry without a key")
	}
	info, err := t.tc.Stat(e.Hash, client.StatOptions{Recursive: true, Versions: true})
	if err != nil {
		return StatInfo{}, err
	}
	return StatInfo{
		Kind:         string(info.Kind),
		TieType:      info.TieType.String(),
		Filename:     info.Filename,
		Name:         info.Name,
		MediaType:    info.MediaType,
		Size:         info.Size,
		Tags:         info.Tags,
		TagDate:      info.TagDate,
		Meta:         info.Meta,
		Paths:        info.Paths,
		SubDirCount:  info.SubDirCount,
		FileCount:    info.FileCount,
		ArchiveCount: info.ArchiveCount,
		TotalSize:    info.TotalSize,
		VersionCount: len(info.Versions),
	}, nil
}

// Favorites delegate to the tie client's favorites registry, the canonical
// definition of the ("tags","favorite",<tag>) convention shared across every
// client on the collection.

func (t *TieFS) ListFavoriteTags() ([]string, error) {
	return t.tc.ListFavorites()
}

func (t *TieFS) AddFavoriteTag(tag string) error {
	return t.tc.RegisterFavorite(tag)
}

func (t *TieFS) RemoveFavoriteTag(tag string) error {
	return t.tc.UnregisterFavorite(tag)
}
