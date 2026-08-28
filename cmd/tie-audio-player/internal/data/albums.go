package data

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
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

	"git.sr.ht/~uid/tie/client"
	"git.sr.ht/~uid/tie/io/getlib"
)

// PlaylistTag is the tie tag every saved playlist carries, so playlists are a
// browsable tag alongside the library's own tags. playlistTrackProp holds a
// playlist dir's ordered track list: one value per track, each "<index>:<hash>"
// with a zero-padded index so the order survives tie's unordered value sets
// (filenames and track numbers can't carry the order — they live on the shared
// content hash, not on this dir).
const (
	PlaylistTag       = "playlist"
	playlistTrackProp = "playlist-track"
)

// AlbumKind distinguishes the tie shapes a browsable audio entry can take.
type AlbumKind int

const (
	AlbumDir     AlbumKind = iota // audio-dir: a directory of track blobs
	AlbumArchive                  // audio-archive: a zip of tracks (playback deferred)
	AlbumTrack                    // a single audio-file
)

// Album is a browsable entry on the cover wall.
type Album struct {
	UID       string // dir UID, or file/archive content hash
	Kind      AlbumKind
	Title     string
	Artist    string
	Year      string
	ThumbHash string // content hash of the cached cover thumbnail, "" if none
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
	Filename string
	Duration float64 // playing time in seconds, 0 when unknown
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

func httpClientForHost(host client.FileHost) *http.Client {
	if !host.Insecure {
		return nil
	}
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
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
	r, err := getlib.ReadFile(httpClientForHost(h), h.URL, hash)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// CoverReader returns the album's cover-image bytes, or an error when the
// album has no cover (the tile then shows a placeholder). tie's audio-dir
// import stores no thumbnail triple, so for a dir the cover falls back to a
// cover image file among its children (e.g. cover.jpg).
func (s *Session) CoverReader(a Album) (io.ReadSeeker, error) {
	hash := a.ThumbHash
	if hash == "" && a.Kind == AlbumDir {
		hash = s.dirCoverHash(a.UID)
	}
	if hash == "" {
		return nil, errors.New("no cover")
	}
	b, err := s.fetchBlob(hash)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(b), nil
}

// dirCoverHash finds a cover image among an album dir's children, preferring a
// file named cover/folder/front, else the first image. Returns "" if none.
func (s *Session) dirCoverHash(uid string) string {
	dir, err := client.ReadTieDir(s.Tie, client.DirUID(uid))
	if err != nil {
		return ""
	}
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

func isImageFile(f client.File) bool {
	return f.TieType == client.TieImageFile || strings.HasPrefix(f.MediaType, "image/")
}

// AllTags returns every tag known to the tie store, for the sidebar.
func (s *Session) AllTags() ([]string, error) {
	tags, _, err := s.Tie.ListTags(0, -1)
	return tags, err
}

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
	// tie import tags both the audio-dir and each of its files, so a query
	// returns the album dir alongside all its tracks. Collect the dir UIDs
	// first, then drop any single track that belongs to one of those dirs so
	// each album shows as one tile. Standalone tracks (no shown parent dir)
	// still appear.
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
	for _, row := range rows {
		a, ok := classifyAlbum(row)
		if !ok {
			continue
		}
		parents := client.RowValues(row, client.TieParent.String())
		var ownAlbum string
		switch a.Kind {
		case AlbumDir:
			dirUIDs[a.UID] = true
			ownAlbum = client.RowFirst(row, client.TieAlbum.String())
		case AlbumTrack:
			trackAlbum := client.RowFirst(row, client.TieAlbum.String())
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
	return albums, nil
}

func classifyAlbum(row client.Row) (Album, bool) {
	types := client.RowValues(row, client.TieTypeProperty.String())
	a := Album{
		UID: row.Key,
		Title: firstNonEmpty(
			client.RowFirst(row, client.TieAlbum.String()),
			client.RowFirst(row, client.TieName.String()),
			client.RowFirst(row, client.TieFilename.String()),
		),
		Artist:    client.RowFirst(row, client.TieArtist.String()),
		Year:      client.RowFirst(row, client.TieYear.String()),
		ThumbHash: client.RowFirst(row, "thumbnail"),
	}
	switch {
	case slices.Contains(types, client.TieAudioDir.String()):
		a.Kind = AlbumDir
		return a, true
	case slices.Contains(types, client.TieAudioArchive.String()):
		a.Kind = AlbumArchive
		return a, true
	case slices.Contains(types, client.TieAudioFile.String()):
		a.Kind = AlbumTrack
		if a.Title == "" {
			a.Title = client.RowFirst(row, client.TieTitle.String())
		}
		return a, true
	}
	if strings.HasPrefix(client.RowFirst(row, client.TieMediaType.String()), "audio/") {
		a.Kind = AlbumTrack
		return a, true
	}
	return Album{}, false
}

// AlbumTracks lists the tracks of an album, ordered by track number then
// filename. Archive albums are not yet supported for track listing.
func (s *Session) AlbumTracks(a Album) ([]Track, error) {
	switch a.Kind {
	case AlbumTrack:
		return []Track{s.trackFromHash(a.UID)}, nil
	case AlbumDir:
		// A saved playlist is an audio-dir with an explicit ordered track list;
		// honor that order instead of the filename/track-number sort below.
		if tracks, ok, err := s.playlistTracks(a.UID); err != nil {
			return nil, err
		} else if ok {
			return tracks, nil
		}
		dir, err := client.ReadTieDir(s.Tie, client.DirUID(a.UID))
		if err != nil {
			return nil, err
		}
		tracks := make([]Track, 0, len(dir.Files))
		for _, f := range dir.Files {
			if !isAudioFile(f) {
				continue
			}
			tracks = append(tracks, s.trackFromHash(f.Uid))
		}
		sort.Slice(tracks, func(i, j int) bool {
			if tracks[i].TrackNo != tracks[j].TrackNo {
				return tracks[i].TrackNo < tracks[j].TrackNo
			}
			return tracks[i].Filename < tracks[j].Filename
		})
		return tracks, nil
	default:
		return nil, errors.New("archive album playback not supported yet")
	}
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
	t := Track{Hash: hash}
	row, err := s.Tie.Get(hash)
	if err != nil {
		return t, false
	}
	t.Title = client.RowFirst(row, client.TieTitle.String())
	t.Artist = client.RowFirst(row, client.TieArtist.String())
	t.Album = client.RowFirst(row, client.TieAlbum.String())
	t.Year = client.RowFirst(row, client.TieYear.String())
	t.Filename = client.RowFirst(row, client.TieFilename.String())
	if n, err := strconv.Atoi(client.RowFirst(row, client.TieTrack.String())); err == nil {
		t.TrackNo = n
	}
	if d, err := strconv.ParseFloat(client.RowFirst(row, "duration"), 64); err == nil {
		t.Duration = d
	}
	return t, true
}

// playlistTracks returns the ordered tracks of a saved playlist dir. ok is
// false for an ordinary album dir (one with no explicit ordered track list),
// so the caller falls back to the directory listing.
func (s *Session) playlistTracks(uid string) ([]Track, bool, error) {
	row, err := s.Tie.Get(uid)
	if err != nil {
		return nil, false, err
	}
	vals := client.RowValues(row, playlistTrackProp)
	if len(vals) == 0 {
		return nil, false, nil
	}
	sort.Strings(vals) // the zero-padded "<index>:<hash>" prefix restores order
	tracks := make([]Track, 0, len(vals))
	for _, v := range vals {
		if i := strings.IndexByte(v, ':'); i >= 0 {
			tracks = append(tracks, s.trackFromHash(v[i+1:]))
		}
	}
	return tracks, true, nil
}

// SaveQueueAsPlaylist persists an ordered list of track hashes as a new
// playlist: a fresh audio-dir (so it shows on the cover wall like an album)
// tagged PlaylistTag, carrying an explicit ordered track list so the queue
// order survives even though each track's filename and number live on its
// shared content hash. It returns the created playlist as an Album.
func (s *Session) SaveQueueAsPlaylist(name string, trackHashes []string) (Album, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Album{}, errors.New("playlist name is required")
	}
	if len(trackHashes) == 0 {
		return Album{}, errors.New("the queue is empty")
	}

	uid := newPlaylistDirUID()
	batch := s.Tie.NewBatch()

	// Directory node: typed audio-dir (plus the structural directory marker) so
	// the browser classifies and lists it like any album.
	batch.Add(uid, client.TieTypeProperty.String(), client.TieAudioDir.String())
	batch.Add(uid, client.TieTypeProperty.String(), client.TieDirectory.String())
	batch.Add(uid, client.TieName.String(), name)
	batch.Add(uid, client.TieFilename.String(), name)
	batch.Add(uid, client.TieAlbum.String(), name)
	batch.Add(uid, client.TieFilesize.String(), strconv.Itoa(len(trackHashes)))
	batch.Set(uid, client.TieTagDate.String(), []string{time.Now().Format(time.DateTime)})

	// Tag it so it is queryable, registering the tag in the (tags,"all",tag) table.
	batch.Add(uid, client.TieTag.String(), PlaylistTag)
	batch.Add(client.TieTags.String(), client.TieAll.String(), PlaylistTag)

	// Ordered track list plus a structural parent edge per track.
	for i, h := range trackHashes {
		batch.Add(uid, playlistTrackProp, fmt.Sprintf("%06d:%s", i, h))
		batch.Add(h, client.TieParent.String(), uid)
	}

	if _, err := s.Tie.Batch(batch); err != nil {
		return Album{}, err
	}
	if err := s.Tie.Sync(); err != nil {
		return Album{}, err
	}
	return Album{UID: uid, Kind: AlbumDir, Title: name}, nil
}

// newPlaylistDirUID mints a DirUID as 64 hex chars of 32 random bytes, matching
// the content-hash shape tie stores dir UIDs as.
func newPlaylistDirUID() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("data: reading random bytes for playlist UID: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
