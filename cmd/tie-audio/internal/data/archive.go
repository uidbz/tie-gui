package data

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // cover decode
	"image/jpeg"
	_ "image/png" // cover decode
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/uidbz/tie/client"
	"github.com/uidbz/tie/io/archivelib"
	"github.com/uidbz/tie/io/putlib"
	"github.com/uidbz/tie/metadata/tag"
)

// archiveCoverEdge is the pixel width a cover extracted from an archive is
// downscaled to before it is stored as the archive's thumbnail, matching the
// cover wall's decode-side cap so later fetches stay small.
const archiveCoverEdge = 512

// archiveTracks resolves an audio-archive album to its playable tracks.
//
// An audio-archive is a single blob (a zip of a ripped album, say), so its
// tracks have no content address of their own until one is minted here: the
// archive is downloaded, each audio member is extracted, and its content hash
// (the same HighwayHash every tie client computes) is uploaded to the
// filehost when missing. Playback then streams the member like any other tie
// blob (baseURL/hash), which keeps pwplay — wherever it runs — fetching from
// the filehost rather than from this device. The upload happens once ever
// per member; the resolved track list is cached per session (keyed by
// archive hash and host), so re-opening the album costs nothing and even an
// app restart only re-downloads the archive to re-run the existence checks.
//
// The whole archive blob plus one member are held in memory at a time — the
// same order of footprint as tie-view's archive browsing.
func (s *Session) archiveTracks(a Album) ([]Track, error) {
	host := s.Host()
	if host.URL == "" {
		return nil, errors.New("no filehost configured")
	}
	key := a.UID + "@" + host.URL
	s.archivesMu.Lock()
	cached, ok := s.archives[key]
	s.archivesMu.Unlock()
	if ok {
		return cached, nil
	}
	tracks, err := s.resolveArchive(a, host)
	if err != nil {
		return nil, err
	}
	s.archivesMu.Lock()
	if s.archives == nil {
		s.archives = make(map[string][]Track)
	}
	s.archives[key] = tracks
	s.archivesMu.Unlock()
	return tracks, nil
}

// resolveArchive does archiveTracks' work on a cache miss.
func (s *Session) resolveArchive(a Album, host client.FileHost) ([]Track, error) {
	blob, err := s.fetchBlob(a.UID)
	if err != nil {
		return nil, fmt.Errorf("downloading archive: %w", err)
	}
	members, err := archivelib.List(bytes.NewReader(blob))
	if err != nil {
		return nil, fmt.Errorf("listing archive members: %w", err)
	}

	pc := putlib.PutConfig{Client: client.HTTPClientFor(host), Store: host.Store}

	// memberTrack keeps the member's directory alongside its track so the
	// sort below can keep multi-disc archives (one subdirectory per disc)
	// grouped: disc directory first, then track number, then filename.
	type memberTrack struct {
		dir   string
		track Track
	}
	var mts []memberTrack
	var coverName string // best cover-image member so far (see coverScore)
	coverScore := -1
	var coverData, embeddedCover []byte
	for _, m := range members {
		switch m.Kind {
		case archivelib.Audio:
			data, err := readArchiveMember(blob, m.Name)
			if err != nil {
				return nil, fmt.Errorf("extracting %s: %w", m.Name, err)
			}
			hash, err := pc.AddressOf(bytes.NewReader(data))
			if err != nil {
				return nil, fmt.Errorf("hashing %s: %w", m.Name, err)
			}
			t := Track{Hash: hash, Filename: path.Base(m.Name), AlbumUID: a.UID}
			if meta, err := tag.ReadFrom(bytes.NewReader(data)); err == nil {
				t.Title = meta.Title()
				t.Artist = meta.Artist()
				t.Album = meta.Album()
				if y := meta.Year(); y > 0 {
					t.Year = strconv.Itoa(y)
				}
				if n, _ := meta.Track(); n > 0 {
					t.TrackNo = n
				}
				t.Duration = meta.Duration().Seconds()
				// First embedded picture wins when the archive carries no
				// cover image file.
				if embeddedCover == nil {
					if pic := meta.Picture(); pic != nil {
						embeddedCover = pic.Data
					}
				}
			}
			if err := ensureBlob(pc, host, hash, data, m.Name); err != nil {
				return nil, fmt.Errorf("making %s playable: %w", m.Name, err)
			}
			mts = append(mts, memberTrack{dir: path.Dir(m.Name), track: t})
		case archivelib.Image:
			if score := coverScoreOf(m.Name); score > coverScore {
				coverScore = score
				coverName = m.Name
			}
		}
	}
	if len(mts) == 0 {
		return nil, errors.New("archive contains no audio tracks")
	}
	sort.Slice(mts, func(i, j int) bool {
		if mts[i].dir != mts[j].dir {
			return mts[i].dir < mts[j].dir
		}
		if mts[i].track.TrackNo != mts[j].track.TrackNo {
			return mts[i].track.TrackNo < mts[j].track.TrackNo
		}
		return mts[i].track.Filename < mts[j].track.Filename
	})
	tracks := make([]Track, 0, len(mts))
	for _, mt := range mts {
		tracks = append(tracks, mt.track)
	}

	// Cover, best effort: the archive's own thumbnail relation is what the
	// cover wall and CoverBytesForUID read, so storing it once gives every
	// machine (and tie-view) the artwork. Only runs when the archive has no
	// thumbnail yet.
	if a.ThumbHash == "" {
		if coverName != "" {
			coverData, err = readArchiveMember(blob, coverName)
			if err != nil {
				coverData = nil
			}
		}
		if coverData == nil {
			coverData = embeddedCover
		}
		if coverData != nil {
			s.storeArchiveCover(a.UID, pc, host, coverData)
		}
	}
	return tracks, nil
}

// readArchiveMember extracts one member's bytes from an in-memory archive.
func readArchiveMember(blob []byte, name string) ([]byte, error) {
	rc, err := archivelib.Open(bytes.NewReader(blob), name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// coverScoreOf ranks an image member as an album cover: a file named
// cover/folder/front beats any other image, mirroring dirCoverHash. -1 leaves
// a previously scored candidate in place.
func coverScoreOf(name string) int {
	switch base := strings.ToLower(path.Base(name)); {
	case strings.HasPrefix(base, "cover."),
		strings.HasPrefix(base, "folder."),
		strings.HasPrefix(base, "front."):
		return 1
	default:
		return 0
	}
}

// storeArchiveCover scales img, uploads it as the archive's cover thumbnail
// and records the (archiveUID, "thumbnail", thumbHash) relation. Every step
// is best effort: failures are logged and the album simply stays coverless.
func (s *Session) storeArchiveCover(archiveUID string, pc putlib.PutConfig, host client.FileHost, img []byte) {
	decoded, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return
	}
	if w := decoded.Bounds().Dx(); w > archiveCoverEdge {
		decoded = imaging.Resize(decoded, archiveCoverEdge, 0, imaging.Lanczos)
	}
	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, decoded, &jpeg.Options{Quality: 90}); err != nil {
		return
	}
	thumbHash, err := pc.AddressOf(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return
	}
	if err := ensureBlob(pc, host, thumbHash, buf.Bytes(), archiveUID[:8]+"-cover.jpg"); err != nil {
		fmt.Println("Error uploading archive cover:", err)
		return
	}
	if err := s.Tie.Set(archiveUID, "thumbnail", []string{thumbHash}); err != nil {
		fmt.Println("Error recording archive cover:", err)
	}
}

// ensureBlob makes hash fetchable from the filehost, uploading data when the
// blob is absent. The existence check is a HEAD request, so a member that any
// client has already extracted costs one round-trip and no bytes.
func ensureBlob(pc putlib.PutConfig, host client.FileHost, hash string, data []byte, name string) error {
	ok, err := blobExists(pc.Client, host.URL, hash)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	item := pc.UploadMultipart(host.URL+"/upload/"+hash, bytes.NewReader(data), len(data), name)
	if item.ErrorMsg != "" {
		return errors.New(item.ErrorMsg)
	}
	if item.Hash != hash {
		return fmt.Errorf("checksum mismatch uploading %s", name)
	}
	return nil
}

// blobExists reports whether the filehost holds hash, via a HEAD request
// (200 present, 404 absent; anything else is an error, not an absence).
func blobExists(hc *http.Client, baseURL, hash string) (bool, error) {
	req, err := http.NewRequest(http.MethodHead, baseURL+"/"+hash, nil)
	if err != nil {
		return false, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("filehost stat %s: unexpected status %s", hash, resp.Status)
	}
}
