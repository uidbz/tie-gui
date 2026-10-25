package fs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/uidbz/tie/client"
	"github.com/uidbz/tie/metadata"
)

// TieFS serves the tie tagging filesystem over a tie client. It implements both
// FileSystem and TagStore.
type TieFS struct {
	tc     *client.TieClient
	tmpDir string // lazily created cache for materialized downloads
}

func NewTieFS(tc *client.TieClient) *TieFS { return &TieFS{tc: tc} }

func (t *TieFS) Scheme() string { return "tie" }

// Client returns the underlying tie client (e.g. for the preview grid's
// thumbnailer and dimension lookups).
func (t *TieFS) Client() *client.TieClient { return t.tc }

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
	return t.importFile(destDir, srcPath, name, nil, nil)
}

// ImportWithProgress is Import that, when progress is non-nil, reports uploaded
// bytes to it for a live progress bar. Implements ProgressImporter.
func (t *TieFS) ImportWithProgress(destDir, srcPath, name string, progress io.Writer) error {
	return t.importFile(destDir, srcPath, name, nil, progress)
}

// ImportTagged is ImportWithProgress that additionally applies tags to the
// imported file (and registers them), so album imports are queryable in media
// apps like tie-audio. Implements TagImporter.
func (t *TieFS) ImportTagged(destDir, srcPath, name string, tags []string, progress io.Writer) error {
	return t.importFile(destDir, srcPath, name, tags, progress)
}

// importFile uploads srcPath into the tie tree under destDir, writes its file
// triples (versions any existing same-named file), tags it when tags are
// non-empty, and — for audio files — records its media metadata (title,
// artist, album, year, track, duration) on the content hash, mirroring what
// client.ImportFile writes so media apps can title, sort and group the track.
func (t *TieFS) importFile(destDir, srcPath, name string, tags []string, progress io.Writer) error {
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
	hash, err := t.tc.WriteFileWithProgress(host, "", uid, name, srcPath, tags, progress)
	if err != nil {
		return err
	}
	if err := writeAudioMetadata(t.tc, hash, srcPath); err != nil {
		return err
	}
	// added-at: the stable "date added" of the tie media model, written once
	// (tag-date is rewritten by every re-import).
	return t.tc.EnsureAddedAt(hash)
}

// writeAudioMetadata records srcPath's audio metadata (title, artist,
// album-artist, album, year, track, track-total, disc, disc-total, genre,
// composer, duration) on its content hash — the same triples
// client.ImportFile's appendTagOps writes. Non-audio files (and
// untaggable audio) yield no metadata and no write.
func writeAudioMetadata(tc *client.TieClient, hash, srcPath string) error {
	m := client.ExtractMediaMetadata(srcPath)
	if m == (metadata.Media{}) {
		return nil
	}
	batch := tc.NewBatch()
	if m.Title != "" {
		batch.Add(hash, client.TieTitle.String(), m.Title)
	}
	if m.Artist != "" {
		batch.Add(hash, client.TieArtist.String(), m.Artist)
	}
	if m.AlbumArtist != "" {
		batch.Add(hash, "album-artist", m.AlbumArtist)
	}
	if m.Album != "" {
		batch.Add(hash, client.TieAlbum.String(), m.Album)
	}
	if m.Year != 0 {
		batch.Add(hash, client.TieYear.String(), strconv.Itoa(m.Year))
	}
	if m.Track != 0 {
		batch.Add(hash, client.TieTrack.String(), strconv.Itoa(m.Track))
	}
	if m.Duration > 0 {
		batch.Add(hash, "duration", strconv.FormatFloat(m.Duration, 'f', 3, 64))
	}
	if m.TrackTotal != 0 {
		batch.Set(hash, client.RelTrackTotal, []string{strconv.Itoa(m.TrackTotal)})
	}
	if m.Disc != 0 {
		batch.Set(hash, client.RelDisc, []string{strconv.Itoa(m.Disc)})
	}
	if m.DiscTotal != 0 {
		batch.Set(hash, client.RelDiscTotal, []string{strconv.Itoa(m.DiscTotal)})
	}
	if g := client.SplitGenres(m.Genre); len(g) > 0 {
		batch.Set(hash, client.RelGenre, g)
	}
	if m.Composer != "" {
		batch.Set(hash, client.RelComposer, []string{m.Composer})
	}
	batch.Set(hash, client.RelMetaVersion, []string{client.MetaVersion})
	if _, err := tc.Batch(batch); err != nil {
		return err
	}
	return tc.Sync()
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

// Remove deletes a tie entry by unlinking its tree triples. It never touches
// the filehost: a file's blob stays content-addressed, and when the last parent
// edge is gone the unlink drops the per-hash metadata while the blob remains
// governed by the filehost's retention/reaper. Directories are removed
// recursively (children first), because a standard file-manager Delete on a
// non-empty folder is expected to work; an empty-directory delete is the
// client's own model. Implements Remover.
//
// Entries from a tag-query listing carry a synthetic "/<name>" path (a query
// has no containing directory), so the parents recorded on the subject are
// used when the path's parent is not among them. A normal directory listing
// deletes just that one parent edge, preserving the entry in other folders.
func (t *TieFS) Remove(e Entry) error {
	if e.Hash == "" {
		return fmt.Errorf("tie: cannot delete %q without a key", e.Name)
	}
	if !e.IsDir {
		return t.removeFile(e)
	}
	return t.removeDirEntry(e)
}

func (t *TieFS) removeFile(e Entry) error {
	parents, err := t.parentUIDs(e.Hash)
	if errors.Is(err, client.ErrNotFound) {
		return nil // already gone (e.g. deleted with a parent directory)
	}
	if err != nil {
		return err
	}
	if parent, err := t.parentUID(e.Path); err == nil && parent != "" && containsDir(parents, parent) {
		return t.detachChild(e.Hash, parent)
	}
	if len(parents) == 0 {
		return fmt.Errorf("tie: no parent directory recorded for %q", e.Name)
	}
	// A tag-query result: unlink the item from every location it appears in.
	for _, parent := range parents {
		if err := t.detachChild(e.Hash, parent); err != nil {
			return err
		}
	}
	return nil
}

func (t *TieFS) removeDirEntry(e Entry) error {
	uid := client.DirUID(e.Hash)
	parents, err := t.parentUIDs(e.Hash)
	if errors.Is(err, client.ErrNotFound) {
		return nil // already gone (e.g. deleted with a parent directory)
	}
	if err != nil {
		return err
	}
	targets := parents
	if parent, err := t.parentUID(e.Path); err == nil && parent != "" && containsDir(parents, parent) {
		targets = []client.DirUID{parent}
	}
	if len(targets) == 0 {
		return fmt.Errorf("tie: no parent directory recorded for %q", e.Name)
	}
	if err := t.removeDirContents(uid); err != nil {
		return err
	}
	for _, parent := range targets {
		if err := t.deleteDir(uid, parent); err != nil {
			return err
		}
	}
	return nil
}

// removeDirContents recursively unlinks every file, archive and subdirectory
// below uid. Subdirectories are unlinked from uid only, so a child linked under
// another directory survives there.
func (t *TieFS) removeDirContents(uid client.DirUID) error {
	dir, err := client.ReadTieDir(t.tc, uid)
	if err != nil {
		return err
	}
	for _, f := range dir.Files {
		if err := t.detachChild(f.Uid, uid); err != nil {
			return err
		}
	}
	for _, a := range dir.Archives {
		if err := t.detachChild(a.Hash, uid); err != nil {
			return err
		}
	}
	for _, sub := range dir.SubDirs {
		if err := t.removeDirContents(sub.Uid); err != nil {
			return err
		}
		if err := t.deleteDir(sub.Uid, uid); err != nil {
			return err
		}
	}
	return nil
}

// detachChild removes the (key,"parent",parent) edge. When that was the key's
// last parent, its per-hash metadata is dropped as well; the content blob is
// left on the filehost. This mirrors the client's unexported detachChild, which
// the pinned tie v0.6 client does not expose (DeleteFile arrived in v0.7).
func (t *TieFS) detachChild(key string, parent client.DirUID) error {
	b := t.tc.NewBatch()
	b.Delete(key, client.TieParent.String(), string(parent))
	if _, err := t.tc.Batch(b); err != nil {
		return err
	}
	if err := t.tc.Sync(); err != nil {
		return err
	}
	n, err := t.parentCount(key)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil // still reachable from another directory; keep its metadata
	}
	row, err := t.tc.Get(key)
	if errors.Is(err, client.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	meta := t.tc.NewBatch()
	for relation, values := range row.Attributes {
		for _, value := range values {
			meta.Delete(key, relation, value)
		}
	}
	if _, err := t.tc.Batch(meta); err != nil {
		return err
	}
	return t.tc.Sync()
}

// deleteDir removes an empty directory's parent edge and path triples, dropping
// its metadata when no parent remains. It mirrors the client's v0.7 DeleteDir;
// the pinned v0.6 client only exposes the pieces.
func (t *TieFS) deleteDir(uid, parent client.DirUID) error {
	dir, err := client.ReadTieDir(t.tc, uid)
	if err != nil {
		return err
	}
	if len(dir.SubDirs) > 0 || len(dir.Files) > 0 || len(dir.Archives) > 0 {
		return fmt.Errorf("tie: directory %s is not empty", uid)
	}
	b := t.tc.NewBatch()
	b.Delete(string(uid), client.TieParent.String(), string(parent))
	for _, p := range dir.Paths {
		b.Delete(string(uid), client.TiePath.String(), p)
	}
	if _, err := t.tc.Batch(b); err != nil {
		return err
	}
	if err := t.tc.Sync(); err != nil {
		return err
	}
	n, err := t.parentCount(string(uid))
	if err != nil {
		return err
	}
	if n > 0 {
		return nil // still reachable through another parent; keep its metadata
	}
	row, err := t.tc.Get(string(uid))
	if errors.Is(err, client.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	meta := t.tc.NewBatch()
	for relation, values := range row.Attributes {
		for _, value := range values {
			meta.Delete(string(uid), relation, value)
		}
	}
	if _, err := t.tc.Batch(meta); err != nil {
		return err
	}
	return t.tc.Sync()
}

// parentCount returns how many parent edges key currently has.
func (t *TieFS) parentCount(key string) (int, error) {
	row, err := t.tc.Get(key)
	if errors.Is(err, client.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return len(client.RowValues(row, client.TieParent.String())), nil
}

// parentUID resolves the DirUID of uri's containing directory, returning ""
// when that path is not mapped in the tree.
func (t *TieFS) parentUID(uri string) (client.DirUID, error) {
	uid, err := t.tc.DirUIDFromPath(path.Dir(tiePath(uri)))
	if err != nil || uid == "" {
		return "", err
	}
	return client.DirUID(uid), nil
}

// parentUIDs returns the subjects of the entry's parent edges, if any.
func (t *TieFS) parentUIDs(key string) ([]client.DirUID, error) {
	row, err := t.tc.Get(key)
	if err != nil {
		return nil, err
	}
	values := client.RowValues(row, client.TieParent.String())
	out := make([]client.DirUID, 0, len(values))
	for _, v := range values {
		out = append(out, client.DirUID(v))
	}
	return out, nil
}

func containsDir(ids []client.DirUID, id client.DirUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// DirTypes returns the directory's classification labels — its tie-type values
// minus the structural "directory" marker (e.g. ["audio-dir"]). The entry's
// Hash is the directory's DirUID. Implements DirTyper.
func (t *TieFS) DirTypes(e Entry) ([]string, error) {
	if e.Hash == "" {
		return nil, errors.New("tie: cannot read dir types of an entry without a key")
	}
	return client.GetDirType(t.tc, client.DirUID(e.Hash))
}

// SetDirTypes replaces the directory's classification labels; an empty slice
// clears them. The structural "directory" marker is preserved by the client.
// Implements DirTyper.
func (t *TieFS) SetDirTypes(e Entry, labels []string) error {
	if e.Hash == "" {
		return errors.New("tie: cannot set dir types of an entry without a key")
	}
	return client.SetDirTypes(t.tc, client.DirUID(e.Hash), labels)
}

// LabelDir stamps a DirLabel on the directory at dirURI: a dir-type label,
// tags (registered in the ("tags","all",<tag>) table), a display name, and
// album metadata aggregates — mirroring what client.ImportDir's
// appendTagDirOps and albumMeta write, so media apps (tie-audio) can title
// the directory and match it in tag queries. All writes are additive except
// the single-valued tag-date and aggregates, which replace. The directory
// (and any missing ancestors) is created when absent — an empty source tree
// imported by the copy engine has created no directory yet. Implements
// DirLabeler.
func (t *TieFS) LabelDir(dirURI string, label DirLabel) error {
	dirPath := tiePath(dirURI)
	uid, err := t.tc.DirUIDFromPath(dirPath)
	if err != nil {
		return err
	}
	if uid == "" {
		if uid, err = t.tc.MkTieDirAll(client.FileURIScheme + dirPath); err != nil {
			return err
		}
	}
	batch := t.tc.NewBatch()
	if label.Name != "" {
		batch.Add(string(uid), client.TieFilename.String(), label.Name)
		batch.Add(string(uid), client.TieName.String(), label.Name)
	}
	// tag-date is single-valued (the last import/label time); replace the
	// whole relation so re-labels don't accumulate dates.
	batch.Set(string(uid), client.TieTagDate.String(), []string{time.Now().Format("2006-01-02 15:04:05.000000")})
	for _, tag := range label.Tags {
		if tag == "" {
			continue
		}
		batch.Add(string(uid), client.TieTag.String(), tag)
		batch.Add(client.TieTags.String(), client.TieAll.String(), tag)
	}
	if label.Artist != "" {
		batch.Set(string(uid), client.TieArtist.String(), []string{label.Artist})
	}
	if label.Album != "" {
		batch.Set(string(uid), client.TieAlbum.String(), []string{label.Album})
	}
	if label.Year != "" {
		batch.Set(string(uid), client.TieYear.String(), []string{label.Year})
	}
	if _, err := t.tc.Batch(batch); err != nil {
		return err
	}
	if err := t.tc.Sync(); err != nil {
		return err
	}
	if label.Type != "" {
		if err := t.tc.SetDirType(uid, label.Type); err != nil {
			return err
		}
		// Bring the labeled collection up to tie's collection contract
		// (member-of edges, added-at, disc labels, album summary) so media
		// apps list it — including multi-disc layouts — in one query.
		if client.IsCollectionLabel(label.Type) {
			if _, err := t.tc.IndexCollection(uid, client.IndexOptions{}); err != nil {
				return err
			}
		}
	}
	return nil
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
	var dirTypes []string
	if info.Kind == client.StatDirectory {
		// Stat classifies a directory as plain "directory"; its classification
		// labels (audio-dir, image-dir, …) are separate tie-type values.
		dirTypes, _ = client.GetDirType(t.tc, client.DirUID(e.Hash))
	}
	return StatInfo{
		Kind:         string(info.Kind),
		TieType:      info.TieType.String(),
		DirTypes:     dirTypes,
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
