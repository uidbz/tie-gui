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
	"sync"
	"time"

	"github.com/uidbz/tie/client"
	"github.com/uidbz/tie/metadata"
)

// TieFS serves the tie tagging filesystem over a tie client. It implements both
// FileSystem and TagStore.
type TieFS struct {
	tc *client.TieClient
	// download fetches a blob to a local path. It is a field so tests can
	// inject a downloader that fails or truncates mid-transfer; production uses
	// the tie client.
	download func(hash, dest string) error
	// mu guards tmpDir: Materialize runs on the ops goroutine and the UI
	// goroutine, and both create the cache lazily.
	mu     sync.Mutex
	tmpDir string // lazily created cache for materialized downloads
}

func NewTieFS(tc *client.TieClient) *TieFS {
	return &TieFS{
		tc:       tc,
		download: func(hash, dest string) error { return tc.Download("", hash, dest) },
	}
}

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
		// f.Filename is store-supplied: keep it to one safe path component
		// before it becomes a URI component or a name on screen.
		name := SafeName(f.Filename, f.Uid)
		entries = append(entries, Entry{
			Name:    name,
			Path:    tieURI(path.Join(base, name)),
			IsDir:   false,
			Size:    int64(f.Size),
			ModTime: f.TagDate,
			Hash:    f.Uid,
		})
	}
	for _, a := range dir.Archives {
		name := SafeName(a.Filename, a.Hash)
		entries = append(entries, Entry{
			Name:    name,
			Path:    tieURI(path.Join(base, name)),
			IsDir:   false,
			Size:    int64(a.Size),
			ModTime: a.TagDate,
			Hash:    a.Hash,
		})
	}
	return entries, nil
}

// Materialize returns a local path holding e's bytes, downloading them on
// demand and caching the result for the session.
//
// Three things this has to get right, and previously did not:
//
//   - Atomicity. getlib.DownloadFile writes straight to the destination with
//     os.Create + io.Copy — no temp file, no rename. A cancelled export, a
//     dropped connection or an OOM mid-download used to leave a truncated file
//     at the cache path, and the "already downloaded" Stat hit meant every
//     later Materialize handed out the corrupt file with no way to recover. So
//     download to <hash>.part, check it, then rename into place: a cached file
//     is now always complete.
//   - Untrusted names. The cache path used to be hash + "-" + e.Name, where
//     e.Name is the store's filename triple, copied verbatim out of the
//     database. Nothing constrains it to a basename, so a filename of
//     "../../.bashrc" made filepath.Join escape tmpDir and the download wrote
//     wherever the user's process could write. Keying on the content hash alone
//     removes the untrusted component entirely.
//   - Lifetime. tmpDir was assigned lazily without synchronisation while
//     Materialize runs on both the ops goroutine and the UI goroutine, and it
//     was never removed, so every preview and export left its bytes on disk.
func (t *TieFS) Materialize(e Entry) (string, error) {
	if e.Hash == "" {
		return "", errors.New("tie: entry has no content hash: " + e.Name)
	}
	if !metadata.IsHexHash(e.Hash) {
		return "", errors.New("tie: entry has an unusable content hash: " + e.Name)
	}
	dir, err := t.cacheDir()
	if err != nil {
		return "", err
	}
	dest := filepath.Join(dir, e.Hash)
	if info, err := os.Stat(dest); err == nil && (e.Size <= 0 || info.Size() == e.Size) {
		return dest, nil // already downloaded, and the size still matches
	}

	part := dest + ".part"
	if err := t.download(e.Hash, part); err != nil {
		os.Remove(part)
		return "", err
	}
	info, err := os.Stat(part)
	if err != nil {
		return "", err
	}
	if e.Size > 0 && info.Size() != e.Size {
		os.Remove(part)
		return "", fmt.Errorf("tie: download of %s is %d bytes, expected %d", e.Name, info.Size(), e.Size)
	}
	if err := os.Rename(part, dest); err != nil {
		os.Remove(part)
		return "", err
	}
	return dest, nil
}

// cacheDir returns TieFS's download cache directory, creating it on first use.
// Guarded, because Materialize is called from the ops goroutine and the UI
// goroutine.
func (t *TieFS) cacheDir() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tmpDir == "" {
		d, err := os.MkdirTemp("", "tie-fm-")
		if err != nil {
			return "", err
		}
		t.tmpDir = d
	}
	return t.tmpDir, nil
}

// Close removes the download cache. Materialized files exist only to feed a
// transfer or an external program, so nothing outlives the session that made
// them. Safe to call more than once.
func (t *TieFS) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tmpDir == "" {
		return nil
	}
	err := os.RemoveAll(t.tmpDir)
	t.tmpDir = ""
	return err
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
	hash, err := t.tc.WriteFileWithProgress(host, uid, name, srcPath, tags, progress)
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
		batch.Add(hash, string(client.RelTitle), m.Title)
	}
	if m.Artist != "" {
		batch.Add(hash, string(client.RelArtist), m.Artist)
	}
	if m.AlbumArtist != "" {
		batch.Add(hash, "album-artist", m.AlbumArtist)
	}
	if m.Album != "" {
		batch.Add(hash, string(client.RelAlbum), m.Album)
	}
	if m.Year != 0 {
		batch.Add(hash, string(client.RelYear), strconv.Itoa(m.Year))
	}
	if m.Track != 0 {
		batch.Add(hash, string(client.RelTrack), strconv.Itoa(m.Track))
	}
	if m.Duration > 0 {
		batch.Add(hash, "duration", strconv.FormatFloat(m.Duration, 'f', 3, 64))
	}
	if m.TrackTotal != 0 {
		batch.Set(hash, string(client.RelTrackTotal), []string{strconv.Itoa(m.TrackTotal)})
	}
	if m.Disc != 0 {
		batch.Set(hash, string(client.RelDisc), []string{strconv.Itoa(m.Disc)})
	}
	if m.DiscTotal != 0 {
		batch.Set(hash, string(client.RelDiscTotal), []string{strconv.Itoa(m.DiscTotal)})
	}
	if g := client.SplitGenres(m.Genre); len(g) > 0 {
		batch.Set(hash, string(client.RelGenre), g)
	}
	if m.Composer != "" {
		batch.Set(hash, string(client.RelComposer), []string{m.Composer})
	}
	batch.Set(hash, string(client.RelMetaVersion), []string{client.MetaVersion})
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
		batch.Add(string(uid), string(client.RelFilename), label.Name)
		batch.Add(string(uid), string(client.RelName), label.Name)
	}
	// tag-date is single-valued (the last import/label time); replace the
	// whole relation so re-labels don't accumulate dates.
	batch.Set(string(uid), string(client.RelTagDate), []string{time.Now().Format("2006-01-02 15:04:05.000000")})
	for _, tag := range label.Tags {
		if tag == "" {
			continue
		}
		batch.Add(string(uid), string(client.RelTag), tag)
		batch.Add(string(client.RelTags), string(client.RelAll), tag)
	}
	if label.Artist != "" {
		batch.Set(string(uid), string(client.RelArtist), []string{label.Artist})
	}
	if label.Album != "" {
		batch.Set(string(uid), string(client.RelAlbum), []string{label.Album})
	}
	if label.Year != "" {
		batch.Set(string(uid), string(client.RelYear), []string{label.Year})
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
	return client.RowFirst(row, client.RelPath)
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
