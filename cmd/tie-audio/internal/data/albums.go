package data

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/uidbz/tie/client"
	"github.com/uidbz/tie/io/getlib"
	"github.com/uidbz/tie/metadata/tag"
)

// PlaylistTag is the tie tag every saved playlist carries, so playlists are a
// browsable tag alongside the library's own tags. Playlists themselves are
// tie's first-class playlist subjects (client/playlist.go): an ordered
// playlist-track list plus member-of edges, no fake parent edges.
const PlaylistTag = client.PlaylistTag

// AlbumKind distinguishes the tie shapes a browsable audio entry can take.
type AlbumKind int

const (
	AlbumDir      AlbumKind = iota // audio-dir: a directory of track blobs
	AlbumArchive                   // audio-archive: a single archive blob of tracks
	AlbumTrack                     // a single audio-file
	AlbumPlaylist                  // a saved playlist (tie playlist subject)
)

// Album is a browsable entry on the cover wall.
type Album struct {
	UID       string // dir UID, or file/archive content hash
	Kind      AlbumKind
	Title     string
	Artist    string
	Year      string
	ThumbHash string // content hash of the cached cover thumbnail, "" if none

	// Fields of tie's collection contract (client/media.go), filled from
	// query-expanded rows at no extra round trip; zero when unknown.
	Genres     []string
	Tags       []string
	AddedAt    time.Time // first import (added-at), else the last (tag-date)
	TrackCount int
	Duration   float64 // total seconds
	Stats      client.PlayStats
}

// Display returns the album's label, falling back to the last path component.
func (a Album) Display() string {
	if a.Title != "" {
		return a.Title
	}
	return path.Base(a.UID)
}

// Track is one playable audio file within an album.
type Track struct {
	Hash     string
	Title    string
	Artist   string
	Album    string
	Year     string
	TrackNo  int
	Disc     int // disc number, 0 when unknown
	Filename string
	Duration float64 // playing time in seconds, 0 when unknown
	// AlbumUID identifies the album this track belongs to: the owning audio-dir
	// UID, or the track's own hash for a standalone track. It is what album
	// artwork and playlist grouping key off, so a queue assembled from several
	// albums can show each album's cover. Empty when tie knows no parent.
	AlbumUID string
}

// Display returns the track's label, falling back to its filename.
func (t Track) Display() string {
	if t.Title != "" {
		return t.Title
	}
	return t.Filename
}

// Host resolves the filehost used to fetch content: the configured FileHost
// name when set, then "fast", then the tie config's default hosts.
func (s *Session) Host() client.FileHost {
	cfg := s.Tie.Config
	if s.Cfg.FileHost != "" {
		if h, ok := cfg.FileHosts[s.Cfg.FileHost]; ok {
			return h
		}
	}
	if h, ok := cfg.FileHosts["fast"]; ok {
		return h
	}
	for _, name := range cfg.DefaultFileHosts {
		if h, ok := cfg.FileHosts[name]; ok {
			return h
		}
	}
	return client.FileHost{}
}

// StreamURL is the direct filehost URL for a content hash (baseURL/hash), the
// value handed to pwplay's AddTracks. Returns "" when no host is configured.
func (s *Session) StreamURL(hash string) string {
	h := s.Host()
	if h.URL == "" {
		return ""
	}
	return h.URL + "/" + hash
}

func (s *Session) fetchBlob(hash string) ([]byte, error) {
	h := s.Host()
	r, err := getlib.ReadFile(client.HTTPClientFor(h), h.URL, hash)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// coverHeadBytes is the initial tag probe: enough for the fixed-size headers
// of an ID3v2 or FLAC tag block, plus the tags themselves in the common case.
const coverHeadBytes = 64 << 10

// coverProbeBytes is the wider fallback probe for containers whose metadata
// extent cannot be computed up front (Ogg comments, MP4 atoms, DSF pointers).
// Audio tags sit at the start of the file for the common formats, so a few
// megabytes of head hold the cover while the audio frames behind it — tens of
// megabytes for a FLAC track — are never transferred.
const coverProbeBytes = 4 << 20

// coverTagCap bounds a computed tag block before it is fetched; an embedded
// picture larger than this is rare enough to be worth a full read instead.
const coverTagCap = 16 << 20

// fetchBlobPrefix reads at most n bytes from the start of a blob with an HTTP
// Range request. The filehost serves blobs with http.ServeFile, which honors
// Range; a server that ignores it simply yields the first n bytes of a 200
// response (the body is capped locally). The bytes are not hash-verified: a
// prefix cannot be, and callers use them only to probe tags.
func (s *Session) fetchBlobPrefix(hash string, n int64) ([]byte, error) {
	h := s.Host()
	if h.URL == "" {
		return nil, errors.New("no filehost configured")
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(h.URL, "/")+"/"+hash, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", n-1))
	resp, err := client.HTTPClientFor(h).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPartialContent, http.StatusOK:
		return io.ReadAll(io.LimitReader(resp.Body, n))
	default:
		return nil, fmt.Errorf("filehost get %s: unexpected status %s", hash, resp.Status)
	}
}

// tagBlockSize returns the byte extent of a file's leading metadata block when
// the container declares it: an ID3v2 tag's synchsafe size, or a FLAC metadata
// block chain's last-block offset. ok is false for every other container (or
// when the head is too short/truncated to tell), in which case the caller uses
// the wider fixed probe.
func tagBlockSize(head []byte) (n int64, ok bool) {
	if len(head) >= 10 && string(head[:3]) == "ID3" {
		size := int64(head[6]&0x7f)<<21 | int64(head[7]&0x7f)<<14 |
			int64(head[8]&0x7f)<<7 | int64(head[9]&0x7f)
		if size <= 0 || size > coverTagCap {
			return 0, false
		}
		total := 10 + size
		if head[5]&0x10 != 0 { // v2.4 footer
			total += 10
		}
		return total, true
	}
	if len(head) < 8 || string(head[:4]) != "fLaC" {
		return 0, false
	}
	// Walk the metadata block headers. The block carrying the last-block flag
	// ends the metadata section; audio frames follow it. A block body that
	// runs past the head (a large embedded picture, say) means the extent
	// cannot be computed yet, so the caller falls back to the wide probe.
	pos := int64(4)
	for {
		if pos+4 > int64(len(head)) {
			return 0, false
		}
		h := head[pos]
		length := int64(head[pos+1])<<16 | int64(head[pos+2])<<8 | int64(head[pos+3])
		end := pos + 4 + length
		if end > coverTagCap {
			return 0, false
		}
		if h&0x80 != 0 {
			return end, true
		}
		pos = end
	}
}

// ErrNoCover reports that an album carries no cover image at all, as opposed to
// a cover that exists but could not be fetched. Callers cache this outcome so a
// coverless album is not re-probed on every tile or queue row.
var ErrNoCover = errors.New("no cover")

// CoverReader returns the album's cover-image bytes, or an error when the
// album has no cover (the tile then shows a placeholder). See coverBytes for
// the resolution order.
func (s *Session) CoverReader(a Album) (io.ReadSeeker, error) {
	b, err := s.coverBytes(a.UID, a.ThumbHash)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(b), nil
}

// CoverBytesForUID returns the cover-image bytes for an album identified only
// by its UID (a Track's AlbumUID, which the queue has but no Album struct for).
// ErrNoCover means the album has none; any other error is a fetch failure
// worth retrying later.
//
// The UID may be a directory UID or a content hash (a standalone track); a
// directory read on a non-directory simply yields no candidate, so the two
// shapes need not be distinguished by the caller.
func (s *Session) CoverBytesForUID(uid string) ([]byte, error) {
	hint, _ := s.thumbHints.Load(uid)
	h, _ := hint.(string)
	return s.coverBytes(uid, h)
}

// rememberThumbs records the thumbnail hashes a listing returned, so the
// cover wall's per-tile resolution (CoverBytesForUID) fetches the thumbnail
// blob directly instead of re-reading each album's row first.
func (s *Session) rememberThumbs(albums []Album) {
	for _, a := range albums {
		if a.ThumbHash != "" {
			s.thumbHints.Store(a.UID, a.ThumbHash)
		}
	}
}

// coverBytes resolves an album's cover-image bytes, trying in order:
//  1. thumbHash, when already known (a query-expanded album row)
//  2. the subject's own thumbnail relation (archive covers land there)
//  3. an external cover image among a directory's children (cover.jpg etc.)
//  4. the first track's embedded picture — the first audio file of a
//     directory album, or the track itself for a standalone track
//
// ErrNoCover means the album has no cover anywhere; any other error is a
// fetch failure worth retrying later.
func (s *Session) coverBytes(uid, thumbHash string) ([]byte, error) {
	if uid == "" {
		return nil, ErrNoCover
	}
	// A known thumbnail hash (from a query-expanded listing) is fetched
	// straight away, without reading the album's row. A missing thumbnail blob
	// is not fatal: fall through to the source and re-derive it, which also
	// restores the blob for the next launch.
	var hintErr error
	if thumbHash != "" {
		b, err := s.fetchBlob(thumbHash)
		if err == nil {
			return b, nil
		}
		hintErr = err
	}
	row, _ := s.Tie.Get(uid) // an absent subject yields a zero Row
	if th := client.RowFirst(row, "thumbnail"); th != "" && th != thumbHash {
		if b, err := s.fetchBlob(th); err == nil { // the relation was created by another view/client
			return b, nil
		}
	}
	// ReadTieDir on a non-directory subject yields an empty listing rather
	// than an error, so a standalone track falls through to probing itself.
	if dir, err := client.ReadTieDir(s.Tie, client.DirUID(uid)); err == nil {
		if hash := dirCoverHash(dir); hash != "" {
			return s.coverFromBlob(uid, hash)
		}
		if hash := firstTrackHash(dir); hash != "" {
			pic, err := s.trackEmbeddedCover(hash)
			if err != nil {
				return nil, err
			}
			if pic == nil {
				return nil, noCoverOrErr(hintErr)
			}
			return s.coverFromImage(uid, pic)
		}
		// No files at the root — a multi-disc album files everything below
		// cd<N>/ (and artwork/): look through the collection's members.
		if len(dir.SubDirs) > 0 {
			if hash := s.memberCoverHash(uid); hash != "" {
				return s.coverFromBlob(uid, hash)
			}
			if rows, err := s.collectionAudioRows(uid); err == nil && len(rows) > 0 {
				tracks := make([]Track, 0, len(rows))
				for _, r := range rows {
					t := trackFromRow(r.row, "")
					if t.Disc == 0 {
						t.Disc = r.disc
					}
					tracks = append(tracks, t)
				}
				sortTracks(tracks)
				pic, err := s.trackEmbeddedCover(tracks[0].Hash)
				if err != nil {
					return nil, err
				}
				if pic == nil {
					return nil, noCoverOrErr(hintErr)
				}
				return s.coverFromImage(uid, pic)
			}
		}
	}
	if isAudioRow(row) {
		pic, err := s.trackEmbeddedCover(uid)
		if err != nil {
			return nil, err
		}
		if pic != nil {
			return s.coverFromImage(uid, pic)
		}
	}
	return nil, noCoverOrErr(hintErr)
}

// noCoverOrErr reports the outcome of source resolution when it found no
// cover. If a recorded thumbnail could not be fetched, that failure — not
// "this album has no cover" — is what the caller should see: the blob may
// merely be missing, and the next ask retries it.
func noCoverOrErr(hintErr error) error {
	if hintErr != nil {
		return hintErr
	}
	return ErrNoCover
}

// coverFromBlob fetches the cover image stored at hash and turns it into uid's
// cached thumbnail.
func (s *Session) coverFromBlob(uid, hash string) ([]byte, error) {
	raw, err := s.fetchBlob(hash)
	if err != nil {
		return nil, err
	}
	return s.coverFromImage(uid, raw)
}

// coverFromImage caches raw as uid's cover thumbnail and returns the small
// JPEG. Undecodable bytes are as good as no cover: retrying cannot help.
func (s *Session) coverFromImage(uid string, raw []byte) ([]byte, error) {
	if thumb := s.coverThumbnail(uid, raw); thumb != nil {
		return thumb, nil
	}
	return nil, ErrNoCover
}

// memberCoverHash finds a cover image among a collection's member-of
// members (cover/folder/front preferred, else the first image by name).
func (s *Session) memberCoverHash(uid string) string {
	rows, err := s.Tie.CollectionMembers(uid)
	if err != nil {
		return ""
	}
	var files []client.File
	for _, r := range rows {
		files = append(files, client.File{
			Uid:       r.Key,
			Filename:  client.RowFirst(r, client.RelFilename),
			MediaType: client.RowFirst(r, client.RelMediaType),
			TieType:   client.StringToTieType(firstImageType(r)),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Filename < files[j].Filename })
	return dirCoverHash(client.Directory{Files: files})
}

func firstImageType(r client.Row) string {
	if slices.Contains(client.RowValues(r, client.RelType), client.TieImageFile.String()) {
		return client.TieImageFile.String()
	}
	return ""
}

// dirCoverHash finds a cover image among an album dir's children, preferring a
// file named cover/folder/front, else the first image. Returns "" if none.
func dirCoverHash(dir client.Directory) string {
	var first string
	for _, f := range dir.Files {
		if !isImageFile(f) {
			continue
		}
		if first == "" {
			first = f.Uid
		}
		switch name := strings.ToLower(f.Filename); {
		case strings.HasPrefix(name, "cover."),
			strings.HasPrefix(name, "folder."),
			strings.HasPrefix(name, "front."):
			return f.Uid
		}
	}
	return first
}

// firstTrackHash returns the content hash of the album dir's first audio file
// in filename order — rips are almost always track-number-prefixed, so this
// is the album's first track, the one probed for an embedded cover. "" when
// the dir holds no audio files.
func firstTrackHash(dir client.Directory) string {
	hash, name := "", ""
	for _, f := range dir.Files {
		if !isAudioFile(f) {
			continue
		}
		if hash == "" || f.Filename < name {
			hash, name = f.Uid, f.Filename
		}
	}
	return hash
}

// isAudioRow reports whether a subject's triples mark it an audio file, using
// the raw multi-valued tie-type (a collapsed single TieType is unreliable).
func isAudioRow(row client.Row) bool {
	if slices.Contains(client.RowValues(row, client.RelType), client.TieAudioFile.String()) {
		return true
	}
	return strings.HasPrefix(client.RowFirst(row, client.RelMediaType), "audio/")
}

// trackEmbeddedCover fetches a track's first embedded picture's bytes, or
// (nil, nil) when it has none — including when its tags are unreadable: the
// blob downloaded fine, so a retry would change nothing. A download error is
// returned so the caller retries later.
//
// Only the leading tag block is read, never the audio frames. ID3v2 and FLAC
// declare that block's extent in its header, so the exact bytes are fetched;
// other containers get a fixed-width head probe. When the tags are not in that
// probe (an MP4 with its moov atom at the end) the blob is fetched in full, as
// before. Without this, a library of FLAC rips downloads a whole track —
// tens of megabytes — per album just to look for artwork.
func (s *Session) trackEmbeddedCover(hash string) ([]byte, error) {
	head, err := s.fetchBlobPrefix(hash, coverHeadBytes)
	if err == nil {
		n, exact := tagBlockSize(head)
		if exact && n <= int64(len(head)) {
			if pic, parsed := embeddedCoverIn(head[:n]); parsed {
				return pic, nil
			}
		} else if exact {
			if data, err := s.fetchBlobPrefix(hash, n); err == nil && int64(len(data)) >= n {
				if pic, parsed := embeddedCoverIn(data[:n]); parsed {
					return pic, nil
				}
			}
		} else if wide, err := s.fetchBlobPrefix(hash, coverProbeBytes); err == nil {
			pic, parsed := embeddedCoverIn(wide)
			// MP4's atom walker reports a clean EOF when its prefix runs out,
			// making a truncated moov indistinguishable from a tagless file;
			// only a complete read of a small file settles absence.
			if parsed && (pic != nil || !isMP4Head(wide) || len(wide) < coverProbeBytes) {
				return pic, nil
			}
		}
	}
	data, err := s.fetchBlob(hash)
	if err != nil {
		return nil, err
	}
	meta, err := tag.ReadFrom(bytes.NewReader(data))
	if err != nil {
		return nil, nil
	}
	if pic := meta.Picture(); pic != nil {
		return pic.Data, nil
	}
	return nil, nil
}

// isMP4Head reports whether prefix starts with an ISO base-media file type
// atom, the container whose metadata may sit at the opposite end of the file.
func isMP4Head(prefix []byte) bool {
	return len(prefix) >= 8 && string(prefix[4:8]) == "ftyp"
}

// embeddedCoverIn extracts the embedded picture from a file's leading tag
// bytes. parsed is false when the prefix cannot answer the question —
// unparseable/truncated tags in a container known to hold them — and the
// caller must retry with more of the blob. A container the tag parser does not
// understand cannot carry a picture at all, so that is a settled "none".
func embeddedCoverIn(prefix []byte) (pic []byte, parsed bool) {
	if len(prefix) < 8 {
		return nil, false
	}
	switch {
	case isMP4Head(prefix), string(prefix[:4]) == "fLaC",
		string(prefix[:4]) == "OggS", string(prefix[:4]) == "DSD ",
		string(prefix[:3]) == "ID3":
	default:
		// WAV, AIFF, bare MP3 frames, ID3v1-only, ...: no picture is possible
		// through this parser, so do not download the rest of the file.
		return nil, true
	}
	meta, err := tag.ReadFrom(bytes.NewReader(prefix))
	if err != nil {
		return nil, false
	}
	if pic := meta.Picture(); pic != nil {
		return pic.Data, true
	}
	return nil, true
}

func isImageFile(f client.File) bool {
	return f.TieType == client.TieImageFile || strings.HasPrefix(f.MediaType, "image/")
}

// TagSets returns the sidebar tag lists: every tag registered in the
// ("tags","all",<tag>) registry plus the curated ("tags","favorite",<tag>)
// set. One Get fetches both relations (matching tie-view). A store with no
// tags yields empty lists, not an error.
func (s *Session) TagSets() (all, favorites []string, err error) {
	row, err := s.Tie.Get(string(client.RelTags))
	if errors.Is(err, client.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	all = client.RowValues(row, client.RelAll)
	favorites = client.RowValues(row, client.RelFavorite)
	sort.Strings(favorites)
	return all, favorites, nil
}

// StarTag registers tag in tie's ("tags","favorite") registry (the sidebar's
// ☆/★ toggle); UnstarTag removes it. The favorite set is shared by every
// client on the collection.
func (s *Session) StarTag(tag string) error   { return s.Tie.RegisterFavorite(tag) }
func (s *Session) UnstarTag(tag string) error { return s.Tie.UnregisterFavorite(tag) }

// CoTags returns tags co-occurring with the current selection, for refinement.
func (s *Session) CoTags(include, exclude []string) ([]string, error) {
	return s.Tie.CoTagsForQueryExcludingInput(include, exclude, "")
}

// QueryAlbums returns audio albums and single tracks carrying all include tags
// and none of the exclude tags. An empty include set returns no results.
func (s *Session) QueryAlbums(include, exclude []string) ([]Album, error) {
	if len(include) == 0 {
		return nil, nil
	}
	rows, _, err := s.Tie.Query(client.QuerySpec{
		Terms:   include,
		Exclude: exclude,
		Reverse: true,
		Expand:  true,
		Limit:   -1,
	})
	if err != nil {
		return nil, err
	}
	return s.albumsFromRows(rows), nil
}

// albumsFromRows classifies expanded rows into the wall's album model. It is
// shared by tag queries and free-text search: tie import tags both the album
// directory and each of its tracks, so a multi-row result would otherwise show
// an album tile plus one tile per track.
func (s *Session) albumsFromRows(rows []client.Row) []Album {
	// Collect the dir UIDs first, then drop any single track that belongs to
	// one of those dirs so each album shows as one tile. Standalone tracks (no
	// shown parent dir) still appear.
	type classified struct {
		album    Album
		parents  []string
		ownAlbum string // the dir's own "album" title, "" when it inherits one
	}
	items := make([]classified, 0, len(rows))
	dirUIDs := make(map[string]bool)
	// An album dir row carries no album/artist/year of its own (those live on
	// the track rows), but the query returns the tracks too, so harvest a
	// child track's album title, artist, and year keyed by parent — no extra
	// queries. The dir's folder name often embeds "Artist - Album", so the
	// track's clean album tag makes a better title.
	childAlbum := make(map[string]string)
	childArtist := make(map[string]string)
	childYear := make(map[string]string)
	// innerParents maps a plain (untyped) directory to its parents, so the
	// tracks of a multi-disc album (filed under <album>/cd<N>) are folded
	// into the album tile even before the collection carries member-of.
	innerParents := make(map[string][]string)
	for _, row := range rows {
		if isPlainDirRow(row) {
			innerParents[row.Key] = client.RowValues(row, client.RelParent)
		}
	}
	for _, row := range rows {
		a, ok := classifyAlbum(row)
		if !ok {
			continue
		}
		parents := client.RowValues(row, client.RelParent)
		if a.Kind == AlbumTrack {
			// member-of names the owning collection directly; disc folders
			// resolve one level up.
			parents = append(parents, client.RowValues(row, client.RelMemberOf)...)
			for _, p := range client.RowValues(row, client.RelParent) {
				parents = append(parents, innerParents[p]...)
			}
		}
		var ownAlbum string
		switch a.Kind {
		case AlbumDir, AlbumPlaylist:
			dirUIDs[a.UID] = true
			ownAlbum = client.RowFirst(row, client.RelAlbum)
		case AlbumTrack:
			trackAlbum := client.RowFirst(row, client.RelAlbum)
			for _, p := range parents {
				if trackAlbum != "" && childAlbum[p] == "" {
					childAlbum[p] = trackAlbum
				}
				if a.Artist != "" && childArtist[p] == "" {
					childArtist[p] = a.Artist
				}
				if a.Year != "" && childYear[p] == "" {
					childYear[p] = a.Year
				}
			}
		}
		items = append(items, classified{a, parents, ownAlbum})
	}
	albums := make([]Album, 0, len(items))
	for _, it := range items {
		if it.album.Kind == AlbumTrack && slices.ContainsFunc(it.parents, func(p string) bool { return dirUIDs[p] }) {
			continue
		}
		if it.album.Kind == AlbumDir {
			if it.ownAlbum == "" && childAlbum[it.album.UID] != "" {
				it.album.Title = childAlbum[it.album.UID]
			}
			if it.album.Artist == "" {
				it.album.Artist = childArtist[it.album.UID]
			}
			if it.album.Year == "" {
				it.album.Year = childYear[it.album.UID]
			}
		}
		albums = append(albums, it.album)
	}
	s.rememberThumbs(albums)
	return albums
}

// LatestAlbums returns every album in the collection (audio dirs and audio
// archives), most recently added first — the startup "latest" page. The
// two kinds are queried separately (a query's terms are AND-ed) and merged;
// ordering is by each subject's added-at (first import), falling back to
// tag-date (last import) for data that predates the collection contract.
// Untagged albums are included: the match runs on tie-type, not on tags.
// Playlists are not albums and are left out.
func (s *Session) LatestAlbums() ([]Album, error) {
	return s.LatestAlbumsExcluding(nil)
}

// LatestAlbumsExcluding is LatestAlbums without the albums carrying any of the
// exclude tags — the wall for a tag selection that only excludes (there is
// no include term to query by, so the full listing is filtered client-side
// from the expanded tag attributes).
func (s *Session) LatestAlbumsExcluding(exclude []string) ([]Album, error) {
	var rows []client.Row
	for _, tieType := range []string{client.TieAudioDir.String(), client.TieAudioArchive.String()} {
		r, _, err := s.Tie.Query(client.QuerySpec{
			Terms:   []string{tieType},
			Filter:  string(client.RelType),
			Reverse: true,
			Expand:  true,
			Limit:   -1,
		})
		if err != nil {
			return nil, err
		}
		rows = append(rows, r...)
	}
	albums := make([]Album, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if seen[row.Key] || (len(exclude) > 0 && hasAnyTag(row, exclude)) {
			continue
		}
		seen[row.Key] = true
		if a, ok := classifyAlbum(row); ok && a.Kind != AlbumPlaylist {
			albums = append(albums, a)
		}
	}
	// Albums without any date sort last.
	sort.SliceStable(albums, func(i, j int) bool { return albums[i].AddedAt.After(albums[j].AddedAt) })
	s.rememberThumbs(albums)
	return albums, nil
}

// isPlainDirRow reports whether row is a directory node without a
// collection label (a disc folder, artwork/, …).
func isPlainDirRow(row client.Row) bool {
	types := client.RowValues(row, client.RelType)
	if !slices.Contains(types, client.TieDirectory.String()) {
		return false
	}
	for _, t := range types {
		if client.IsCollectionLabel(t) {
			return false
		}
	}
	return true
}

// hasAnyTag reports whether the row carries one of the given tags.
func hasAnyTag(row client.Row, tags []string) bool {
	for _, t := range client.RowValues(row, "tag") {
		if slices.Contains(tags, t) {
			return true
		}
	}
	return false
}

func classifyAlbum(row client.Row) (Album, bool) {
	types := client.RowValues(row, client.RelType)
	a := Album{
		UID: row.Key,
		Title: firstNonEmpty(
			client.RowFirst(row, client.RelAlbum),
			client.RowFirst(row, client.RelName),
			client.RowFirst(row, client.RelFilename),
		),
		Artist: firstNonEmpty(
			client.RowFirst(row, client.RelAlbumArtist),
			client.RowFirst(row, client.RelArtist),
		),
		Year:      client.RowFirst(row, client.RelYear),
		ThumbHash: client.RowFirst(row, "thumbnail"),
		Genres:    client.RowValues(row, client.RelGenre),
		Tags:      client.RowValues(row, client.RelTag),
		Stats:     client.StatsFromRow(row),
	}
	a.AddedAt, _ = client.ParseMediaTime(client.RowFirst(row, client.RelAddedAt))
	if a.AddedAt.IsZero() {
		a.AddedAt, _ = client.ParseMediaTime(client.RowFirst(row, client.RelTagDate))
	}
	a.TrackCount, _ = strconv.Atoi(client.RowFirst(row, client.RelTrackCount))
	a.Duration, _ = strconv.ParseFloat(client.RowFirst(row, client.RelDuration), 64)
	switch {
	case client.IsPlaylistRow(row):
		a.Kind = AlbumPlaylist
		a.TrackCount = len(client.PlaylistTracks(row))
		return a, true
	case slices.Contains(types, client.TieAudioDir.String()):
		a.Kind = AlbumDir
		return a, true
	case slices.Contains(types, client.TieAudioArchive.String()):
		a.Kind = AlbumArchive
		return a, true
	case slices.Contains(types, client.TieAudioFile.String()):
		a.Kind = AlbumTrack
		if a.Title == "" {
			a.Title = client.RowFirst(row, client.RelTitle)
		}
		return a, true
	}
	if strings.HasPrefix(client.RowFirst(row, client.RelMediaType), "audio/") {
		a.Kind = AlbumTrack
		return a, true
	}
	return Album{}, false
}

// AlbumTracks lists the tracks of an album, ordered by disc, track number,
// then filename (archive albums: by member directory, then track number).
// An archive album's members are extracted and uploaded to the filehost on
// first resolution so they stream like any other blob — see archiveTracks.
func (s *Session) AlbumTracks(a Album) ([]Track, error) {
	switch a.Kind {
	case AlbumTrack:
		t := s.trackFromHash(a.UID)
		// A standalone track is its own album for artwork purposes.
		if t.AlbumUID == "" {
			t.AlbumUID = a.UID
		}
		return []Track{t}, nil
	case AlbumPlaylist:
		return s.playlistTracks(a.UID)
	case AlbumDir:
		// A legacy playlist (pre-v0.6: an audio-dir with an ordered track
		// list) still opens as a playlist until it is migrated.
		row, err := s.Tie.Get(a.UID)
		if err != nil && !errors.Is(err, client.ErrNotFound) {
			return nil, err
		}
		if client.IsPlaylistRow(row) {
			return s.playlistTracks(a.UID)
		}
		rows, err := s.collectionAudioRows(a.UID)
		if err != nil {
			return nil, err
		}
		tracks := make([]Track, 0, len(rows))
		for _, r := range rows {
			t := trackFromRow(r.row, "")
			if t.Disc == 0 {
				t.Disc = r.disc
			}
			// The listing is authoritative about ownership: a track reachable
			// from several collections (an album plus a playlist) belongs to
			// this one here, so artwork and grouping follow the album played.
			t.AlbumUID = a.UID
			tracks = append(tracks, t)
		}
		sortTracks(tracks)
		return tracks, nil
	case AlbumArchive:
		return s.archiveTracks(a)
	default:
		return nil, errors.New("unknown album kind")
	}
}

// sortTracks orders tracks by disc, track number, then filename.
func sortTracks(tracks []Track) {
	sort.SliceStable(tracks, func(i, j int) bool {
		if tracks[i].Disc != tracks[j].Disc {
			return tracks[i].Disc < tracks[j].Disc
		}
		if tracks[i].TrackNo != tracks[j].TrackNo {
			return tracks[i].TrackNo < tracks[j].TrackNo
		}
		return tracks[i].Filename < tracks[j].Filename
	})
}

// memberRow is one audio member of a collection directory, with the disc
// number of the disc folder it sits in (0 = none / unknown).
type memberRow struct {
	row  client.Row
	disc int
}

// collectionAudioRows lists the audio members of an album directory. It asks
// tie for the collection's member-of edges first — one query, whatever the
// folder depth (multi-disc albums file their tracks under cd<N>/) — and falls
// back to walking the directory and its plain subdirectories for data that
// predates the collection contract (or a triplestore without the member-of
// reverse index). Both paths return expanded rows: no per-track round trips.
func (s *Session) collectionAudioRows(uid string) ([]memberRow, error) {
	members, err := s.Tie.CollectionMembers(uid)
	if err == nil {
		var out []memberRow
		for _, r := range members {
			if isAudioRow(r) {
				out = append(out, memberRow{row: r})
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	dir, err := client.ReadTieDir(s.Tie, client.DirUID(uid))
	if err != nil {
		return nil, err
	}
	discOf := map[string]int{}
	var hashes []string
	addFiles := func(d client.Directory, disc int) {
		for _, f := range d.Files {
			if isAudioFile(f) {
				hashes = append(hashes, f.Uid)
				discOf[f.Uid] = disc
			}
		}
	}
	addFiles(dir, 0)
	for _, sd := range dir.SubDirs {
		name := ""
		if len(sd.Paths) > 0 {
			name = path.Base(sd.Paths[0])
		}
		if isCollectionDir(sd.DirTypes) || strings.HasSuffix(name, "_prev") {
			continue // a nested album keeps its own tracks; version history is not content
		}
		sub, err := client.ReadTieDir(s.Tie, client.DirUID(sd.Uid))
		if err != nil {
			continue
		}
		addFiles(sub, discNumber(name))
	}
	if len(hashes) == 0 {
		return nil, nil
	}
	rows, err := s.Tie.Expand(hashes)
	if err != nil {
		return nil, err
	}
	out := make([]memberRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, memberRow{row: r, disc: discOf[r.Key]})
	}
	return out, nil
}

// isCollectionDir reports whether a subdirectory carries its own collection
// label (a nested album), so its files are not this album's tracks.
func isCollectionDir(types []client.TieType) bool {
	for _, t := range types {
		if client.IsCollectionLabel(t.String()) {
			return true
		}
	}
	return false
}

// discNumber parses a disc-like folder name ("cd2", "Disc 3"); 0 otherwise.
func discNumber(name string) int {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, p := range []string{"cd", "disc", "disk"} {
		if rest, ok := strings.CutPrefix(n, p); ok {
			rest = strings.TrimLeft(rest, " -_.")
			if v, err := strconv.Atoi(rest); err == nil && v > 0 && v < 100 {
				return v
			}
		}
	}
	return 0
}

func isAudioFile(f client.File) bool {
	return f.TieType == client.TieAudioFile || strings.HasPrefix(f.MediaType, "audio/")
}

func (s *Session) trackFromHash(hash string) Track {
	t, _ := s.TrackForHash(hash)
	return t
}

// TrackForHash resolves a content hash to its tie metadata. ok is false when tie
// has no record of the hash (e.g. a placeholder or a queue entry that isn't a
// tie blob), in which case the returned Track carries only the hash. Used to
// re-label a queue whose URLs the in-memory registry no longer knows (app
// restart, while pwplay still holds the queue).
func (s *Session) TrackForHash(hash string) (Track, bool) {
	row, err := s.Tie.Get(hash)
	if err != nil {
		return Track{Hash: hash}, false
	}
	return trackFromRow(row, ""), true
}

// trackFromRow maps an expanded track row onto a Track. excludeParent is a
// parent to disregard when picking the owning album (a legacy playlist is a
// parent of every track it lists, but not their album).
func trackFromRow(row client.Row, excludeParent string) Track {
	t := Track{Hash: row.Key}
	t.Title = client.RowFirst(row, client.RelTitle)
	t.Artist = client.RowFirst(row, client.RelArtist)
	t.Album = client.RowFirst(row, client.RelAlbum)
	t.Year = client.RowFirst(row, client.RelYear)
	t.Filename = client.RowFirst(row, client.RelFilename)
	t.AlbumUID = trackAlbumUID(client.RowValues(row, client.RelParent), excludeParent)
	if n, err := strconv.Atoi(client.RowFirst(row, client.RelTrack)); err == nil {
		t.TrackNo = n
	}
	if n, err := strconv.Atoi(client.RowFirst(row, client.RelDisc)); err == nil {
		t.Disc = n
	}
	if d, err := strconv.ParseFloat(client.RowFirst(row, client.RelDuration), 64); err == nil {
		t.Duration = d
	}
	return t
}

// trackAlbumUID picks the album a track belongs to from its parent edges,
// skipping `exclude` (the playlist that merely lists it). Returns "" when the
// track has no usable parent, which leaves it ungrouped and coverless rather
// than mis-attributed.
func trackAlbumUID(parents []string, exclude string) string {
	for _, p := range parents {
		if p == "" || p == exclude {
			continue
		}
		return p
	}
	return ""
}

// playlistTracks returns the ordered tracks of a saved playlist (current or
// legacy) in two round trips: the playlist row and one Expand of its tracks.
func (s *Session) playlistTracks(uid string) ([]Track, error) {
	p, err := s.Tie.ReadPlaylist(uid)
	if err != nil {
		return nil, err
	}
	rows, err := s.Tie.Expand(p.Tracks)
	if err != nil {
		return nil, err
	}
	byHash := make(map[string]client.Row, len(rows))
	for _, r := range rows {
		byHash[r.Key] = r
	}
	tracks := make([]Track, 0, len(p.Tracks))
	for _, h := range p.Tracks {
		r, ok := byHash[h]
		if !ok {
			tracks = append(tracks, Track{Hash: h})
			continue
		}
		// Each track keeps its own album (and cover) in the queue.
		t := trackFromRow(r, uid)
		if t.AlbumUID != "" {
			// A track below a disc folder: the album is one level up.
			if up := s.discFolderAlbum(t.AlbumUID); up != "" {
				t.AlbumUID = up
			}
		}
		tracks = append(tracks, t)
	}
	return tracks, nil
}

// discFolderAlbum returns the album above a disc folder ("" when uid is not
// a disc folder). Cached per session: playlists repeat albums.
func (s *Session) discFolderAlbum(uid string) string {
	s.discMu.Lock()
	if v, ok := s.discUp[uid]; ok {
		s.discMu.Unlock()
		return v
	}
	s.discMu.Unlock()
	up := ""
	if row, err := s.Tie.Get(uid); err == nil && client.RowHas(row, client.RelType, client.TypeDisc) {
		up = client.RowFirst(row, client.RelParent)
	}
	s.discMu.Lock()
	if s.discUp == nil {
		s.discUp = map[string]string{}
	}
	s.discUp[uid] = up
	s.discMu.Unlock()
	return up
}

// SaveQueueAsPlaylist persists an ordered list of track hashes as a new tie
// playlist (tagged PlaylistTag, so it is browsable alongside the library's
// tags). It returns the created playlist as an Album.
func (s *Session) SaveQueueAsPlaylist(name string, trackHashes []string) (Album, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Album{}, errors.New("playlist name is required")
	}
	if len(trackHashes) == 0 {
		return Album{}, errors.New("the queue is empty")
	}
	uid, err := s.Tie.CreatePlaylist(name, trackHashes, nil)
	if err != nil {
		return Album{}, err
	}
	return Album{UID: uid, Kind: AlbumPlaylist, Title: name, TrackCount: len(trackHashes), AddedAt: time.Now()}, nil
}

// MigratePlaylists converts legacy playlists (audio-dirs with fake per-track
// parent edges) to tie playlist subjects. Cheap when there are none (one tag
// query); run once per session in the background.
func (s *Session) MigratePlaylists() (int, error) {
	return s.Tie.MigrateLegacyPlaylists()
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// TrackAlbum returns the album a track belongs to, for attributing a play
// whose queue entry the app no longer knows: the track's parent directory,
// or the album above it when that is a disc folder. "" when unknown.
func (s *Session) TrackAlbum(hash string) string {
	t, ok := s.TrackForHash(hash)
	if !ok || t.AlbumUID == "" {
		return ""
	}
	if up := s.discFolderAlbum(t.AlbumUID); up != "" {
		return up
	}
	return t.AlbumUID
}

// RecentPlays returns the collection's play events since `since`, newest
// first (one round trip).
func (s *Session) RecentPlays(since time.Time, limit int) ([]client.PlayEvent, error) {
	return s.Tie.RecentPlays(client.PlayQuery{Since: since, Limit: limit})
}
