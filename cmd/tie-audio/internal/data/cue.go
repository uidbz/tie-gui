package data

// CUE sheet support: a single-image rip (one FLAC/APE/WAV plus a .cue file) is
// stored in tie as one audio file, but a CUE sheet describes the album as many
// logical tracks. tie-audio reads the cue file from the filehost, overlays its
// titles/performers/track boundaries on the image blob, and plays each logical
// track as a seek range of the same stream (the queue seeks to the segment
// start and advances at the segment end; see internal/ui/transport.go).

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"math"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/uidbz/tie/client"
)

// CueSegment identifies one track of a CUE sheet inside an audio blob. Start
// and End are absolute seconds in the blob; End 0 means "unknown / until EOF".
// Sheet is the cue file's content hash (informational; the queue key carries
// only the boundaries, so a cue re-import with new titles keeps the same
// queue identity).
type CueSegment struct {
	Sheet string
	Start float64
	End   float64
}

// Duration returns the segment length, falling back to the containing blob's
// duration when the cue sheet did not provide an end boundary.
func (c *CueSegment) Duration(fileDuration float64) float64 {
	if c == nil {
		return fileDuration
	}
	if c.End > c.Start {
		return c.End - c.Start
	}
	if fileDuration > c.Start {
		return fileDuration - c.Start
	}
	return 0
}

// Key is the queue identity of a track: the content hash for an ordinary
// track, hash#cue=<start>-<end> (milliseconds) for a cue segment. The filehost
// ignores the URL fragment, so several segments share one blob while the queue
// (and the play log) can tell them apart.
func (t Track) Key() string {
	if t.Cue == nil {
		return t.Hash
	}
	return fmt.Sprintf("%s#cue=%d-%d", t.Hash,
		int64(math.Round(t.Cue.Start*1000)), int64(math.Round(t.Cue.End*1000)))
}

// SplitTrackKey splits a queue key into its content hash and cue segment.
// A plain hash yields a nil segment.
func SplitTrackKey(key string) (hash string, seg *CueSegment) {
	base, frag, ok := strings.Cut(key, "#cue=")
	if !ok {
		return key, nil
	}
	startMS, endMS, ok := strings.Cut(frag, "-")
	if !ok {
		return key, nil
	}
	start, err1 := strconv.ParseInt(startMS, 10, 64)
	end, err2 := strconv.ParseInt(endMS, 10, 64)
	if err1 != nil || err2 != nil || start < 0 || end < 0 {
		return key, nil
	}
	return base, &CueSegment{Start: float64(start) / 1000, End: float64(end) / 1000}
}

// TrackKeyFromURL derives a queue key from a stream URL: its last path segment
// plus the cue fragment. Query strings are ignored (the filehost treats them
// as cache-busters).
func TrackKeyFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return path.Base(raw)
	}
	key := path.Base(u.Path)
	if frag := u.Fragment; strings.HasPrefix(frag, "cue=") {
		if i := strings.IndexAny(frag, "&?"); i >= 0 {
			frag = frag[:i]
		}
		key += "#" + frag
	}
	return key
}

// CueSheet is a parsed .cue file.
type CueSheet struct {
	Title       string
	Performer   string
	Songwriter  string
	AlbumArtist string
	Catalog     string
	Genre       string
	Date        string
	Comment     string
	Files       []CueFile
}

// CueFile is one FILE section of a cue sheet. Track indexes are relative to
// this file's start.
type CueFile struct {
	Name   string
	Type   string
	Tracks []CueTrack
}

// CueTrack is one TRACK section. Start/End are absolute seconds within the
// enclosing CueFile, derived from INDEX 01 (or INDEX 00 when 01 is absent).
type CueTrack struct {
	Number     int
	Audio      bool
	Title      string
	Performer  string
	Songwriter string
	ISRC       string
	Start      float64
	End        float64
	Indexes    map[int]float64
}

// ParseCue parses a cue sheet. Malformed individual lines are ignored (real
// sheets are often hand-edited); only a read error is returned.
func ParseCue(r io.Reader) (*CueSheet, error) {
	sheet := &CueSheet{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)

	fileIdx, trackIdx := -1, -1
	currentTrack := func() *CueTrack {
		if fileIdx < 0 || trackIdx < 0 {
			return nil
		}
		return &sheet.Files[fileIdx].Tracks[trackIdx]
	}
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		tok := cueTokens(line)
		if len(tok) == 0 {
			continue
		}
		switch strings.ToUpper(tok[0]) {
		case "REM":
			applyCueRem(sheet, tok[1:])
		case "TITLE":
			if t := currentTrack(); t != nil {
				t.Title = cueValue(tok[1:])
			} else {
				sheet.Title = cueValue(tok[1:])
			}
		case "PERFORMER":
			if t := currentTrack(); t != nil {
				t.Performer = cueValue(tok[1:])
			} else {
				sheet.Performer = cueValue(tok[1:])
			}
		case "SONGWRITER":
			if t := currentTrack(); t != nil {
				t.Songwriter = cueValue(tok[1:])
			} else {
				sheet.Songwriter = cueValue(tok[1:])
			}
		case "CATALOG":
			sheet.Catalog = cueValue(tok[1:])
		case "FILE":
			if len(tok) < 2 {
				continue
			}
			f := CueFile{Name: tok[1]}
			if len(tok) > 2 {
				f.Type = tok[2]
			}
			sheet.Files = append(sheet.Files, f)
			fileIdx = len(sheet.Files) - 1
			trackIdx = -1
		case "TRACK":
			if fileIdx < 0 || len(tok) < 2 {
				continue
			}
			n, _ := strconv.Atoi(tok[1])
			audio := true
			if len(tok) > 2 && !strings.EqualFold(tok[2], "AUDIO") {
				audio = false
			}
			f := &sheet.Files[fileIdx]
			f.Tracks = append(f.Tracks, CueTrack{
				Number:  n,
				Audio:   audio,
				Indexes: map[int]float64{},
			})
			trackIdx = len(f.Tracks) - 1
		case "INDEX":
			t := currentTrack()
			if t == nil || len(tok) < 3 {
				continue
			}
			n, err := strconv.Atoi(tok[1])
			if err != nil {
				continue
			}
			at, err := parseCueTime(tok[2])
			if err != nil {
				continue
			}
			t.Indexes[n] = at
		case "ISRC":
			if t := currentTrack(); t != nil {
				t.ISRC = cueValue(tok[1:])
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sheet.finalize()
	return sheet, nil
}

// finalize resolves each track's start/end from its indexes. A track ends
// where the next one begins; the last track of a file has no cue-provided end
// (the caller fills it from the blob's duration).
func (s *CueSheet) finalize() {
	for fi := range s.Files {
		f := &s.Files[fi]
		// Resolve every start first: an end boundary needs the next track's
		// start, which may not have been seen yet in a single pass.
		for ti := range f.Tracks {
			t := &f.Tracks[ti]
			if v, ok := t.Indexes[1]; ok {
				t.Start = v
			} else if v, ok := t.Indexes[0]; ok {
				t.Start = v
			} else {
				t.Start = -1
			}
		}
		for ti := range f.Tracks {
			t := &f.Tracks[ti]
			t.End = 0
			if t.Start < 0 {
				continue
			}
			for tj := ti + 1; tj < len(f.Tracks); tj++ {
				next := f.Tracks[tj]
				if next.Start > t.Start {
					t.End = next.Start
					break
				}
			}
		}
	}
}

// applyCueRem handles REM extensions. The common ones are promoted to sheet
// fields; everything else is ignored.
func applyCueRem(sheet *CueSheet, tok []string) {
	rest := strings.TrimSpace(strings.Join(tok, " "))
	if rest == "" {
		return
	}
	upper := strings.ToUpper(rest)
	for _, key := range []string{"ALBUMARTIST", "ALBUM ARTIST", "ALBUM_ARTIST"} {
		if strings.HasPrefix(upper, key) {
			sheet.AlbumArtist = strings.TrimSpace(rest[len(key):])
			return
		}
	}
	key, value, _ := strings.Cut(rest, " ")
	value = strings.TrimSpace(value)
	switch strings.ToUpper(key) {
	case "GENRE":
		sheet.Genre = value
	case "DATE", "YEAR":
		sheet.Date = value
	case "COMMENT":
		sheet.Comment = value
	}
}

// cueValue joins the arguments of a TITLE/PERFORMER line. The tokenizer has
// already unquoted them; joining preserves unquoted multi-word values.
func cueValue(tok []string) string {
	return strings.TrimSpace(strings.Join(tok, " "))
}

// cueTokens splits a cue line into tokens, honoring double-quoted strings.
// Backslashes are preserved (Windows paths are common in cue files); only an
// escaped quote inside a quoted string is unescaped.
func cueTokens(line string) []string {
	var out []string
	for i := 0; i < len(line); {
		for i < len(line) && unicode.IsSpace(rune(line[i])) {
			i++
		}
		if i >= len(line) {
			break
		}
		if line[i] == '"' {
			i++
			var b strings.Builder
			for i < len(line) {
				if line[i] == '\\' && i+1 < len(line) && line[i+1] == '"' {
					b.WriteByte('"')
					i += 2
					continue
				}
				if line[i] == '"' {
					i++
					break
				}
				b.WriteByte(line[i])
				i++
			}
			out = append(out, b.String())
			continue
		}
		start := i
		for i < len(line) && !unicode.IsSpace(rune(line[i])) {
			i++
		}
		out = append(out, line[start:i])
	}
	return out
}

// parseCueTime parses "mm:ss:ff" (75 frames per second), accepting "mm:ss"
// too. Minutes may exceed 59.
func parseCueTime(s string) (float64, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("invalid cue time %q", s)
	}
	var vals [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid cue time %q", s)
		}
		vals[i] = n
	}
	sec := float64(vals[0]*60 + vals[1])
	if len(parts) == 3 {
		sec += float64(vals[2]) / 75
	}
	return sec, nil
}

// cueYear extracts a four-digit year from a REM DATE value.
func cueYear(date string) string {
	date = strings.TrimSpace(date)
	for i := 0; i+4 <= len(date); i++ {
		y := date[i : i+4]
		if _, err := strconv.Atoi(y); err == nil && y[0] != '0' {
			return y
		}
	}
	return ""
}

// albumTreeFile is one file found while scanning an album directory.
type albumTreeFile struct {
	file client.File
	rel  string // slash path below the album root
	dir  string // slash path of the containing directory below the root
	disc int
}

// cueSource is one parsed cue file in an album directory.
type cueSource struct {
	hash  string
	dir   string // directory below the album root
	sheet *CueSheet
}

// cueAlbumData is the cue-relevant contents of one album directory: the parsed
// sheets and every audio file they may reference.
type cueAlbumData struct {
	sheets []cueSource
	files  []albumTreeFile
}

// collectAlbumCues scans an album directory for .cue files, parses them (the
// cue blobs are downloaded once per session), and caches the result. It never
// fails the album open: an unavailable cue simply leaves the ordinary track
// list untouched.
func (s *Session) collectAlbumCues(uid string) *cueAlbumData {
	host := s.Host()
	key := uid + "@" + host.URL

	s.cueMu.Lock()
	if d, ok := s.cueData[key]; ok {
		s.cueMu.Unlock()
		return d
	}
	s.cueMu.Unlock()

	d := s.scanAlbumCues(uid)

	s.cueMu.Lock()
	if s.cueData == nil {
		s.cueData = map[string]*cueAlbumData{}
	}
	s.cueData[key] = d
	s.cueMu.Unlock()
	return d
}

// scanAlbumCues walks an album directory (and its plain subdirectories, e.g.
// disc folders) collecting audio files and cue sheets.
func (s *Session) scanAlbumCues(root string) *cueAlbumData {
	d := &cueAlbumData{}
	type item struct {
		uid  string
		dir  string
		disc int
	}
	queue := []item{{uid: root, dir: ""}}
	seen := map[string]bool{root: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		dir, err := client.ReadTieDir(s.Tie, client.DirUID(cur.uid))
		if err != nil {
			continue
		}
		for _, f := range dir.Files {
			rel := path.Join(cur.dir, f.Filename)
			af := albumTreeFile{file: f, rel: rel, dir: cur.dir, disc: cur.disc}
			if isAudioFile(f) {
				d.files = append(d.files, af)
			}
			if strings.EqualFold(path.Ext(f.Filename), ".cue") {
				if sheet := s.cueSheet(f.Uid); sheet != nil {
					d.sheets = append(d.sheets, cueSource{
						hash: f.Uid, dir: cur.dir, sheet: sheet,
					})
				}
			}
		}
		// ReadTieDir does not order subdirectories; sort them so cue
		// discovery (and which of two sheets wins) is deterministic.
		subs := append([]client.SubDirectory(nil), dir.SubDirs...)
		sort.SliceStable(subs, func(i, j int) bool {
			pi, pj := "", ""
			if len(subs[i].Paths) > 0 {
				pi = subs[i].Paths[0]
			}
			if len(subs[j].Paths) > 0 {
				pj = subs[j].Paths[0]
			}
			return pi < pj
		})
		for _, sd := range subs {
			uid := string(sd.Uid)
			if len(sd.Paths) == 0 || seen[uid] {
				continue
			}
			name := path.Base(sd.Paths[0])
			if strings.HasSuffix(name, "_prev") || isCollectionDir(sd.DirTypes) {
				continue // version history and nested albums are not this album
			}
			seen[uid] = true
			disc := cur.disc
			if n := discNumber(name); n > 0 {
				disc = n
			}
			queue = append(queue, item{uid: uid, dir: path.Join(cur.dir, name), disc: disc})
		}
	}
	return d
}

// ClearCueCache drops the session's cue discovery/parse cache, so a reload
// sees cue sheets and audio files imported or edited since launch.
func (s *Session) ClearCueCache() {
	s.cueMu.Lock()
	s.cueData = nil
	s.cueSheets = nil
	s.cueMu.Unlock()
}

// cueSheet downloads and parses a cue blob, caching the parse by content hash.
func (s *Session) cueSheet(hash string) *CueSheet {
	s.cueMu.Lock()
	if sheet, ok := s.cueSheets[hash]; ok {
		s.cueMu.Unlock()
		return sheet
	}
	s.cueMu.Unlock()

	b, err := s.fetchBlob(hash)
	if err != nil {
		log.Printf("tie-audio: cue %s: %v", hash, err)
		return nil
	}
	sheet, err := ParseCue(bytes.NewReader(b))
	if err != nil {
		log.Printf("tie-audio: cue %s: %v", hash, err)
	}
	s.cueMu.Lock()
	if s.cueSheets == nil {
		s.cueSheets = map[string]*CueSheet{}
	}
	s.cueSheets[hash] = sheet // nil caches an unparseable cue too
	s.cueMu.Unlock()
	return sheet
}

// mergeCueTracks replaces the image tracks covered by cue sheets with their
// logical tracks. Audio files no cue references (and files from an unreadable
// cue) keep their ordinary track entries.
func mergeCueTracks(base []Track, d *cueAlbumData) []Track {
	if d == nil || len(d.sheets) == 0 {
		return base
	}
	byHash := make(map[string]Track, len(base))
	for _, t := range base {
		byHash[t.Hash] = t
	}
	byRel := make(map[string]albumTreeFile, len(d.files))
	byDirBase := make(map[string]albumTreeFile, len(d.files))
	byBase := make(map[string][]albumTreeFile, len(d.files))
	byDir := make(map[string][]albumTreeFile, len(d.files))
	for _, f := range d.files {
		rel := strings.ToLower(path.Clean(f.rel))
		base := strings.ToLower(path.Base(f.rel))
		dir := strings.ToLower(path.Clean(f.dir))
		byRel[rel] = f
		byDirBase[dir+"\x00"+base] = f
		byBase[base] = append(byBase[base], f)
		byDir[dir] = append(byDir[dir], f)
	}

	covered := map[string]bool{}
	cueByHash := map[string][]Track{}
	var cueTracks []Track
	for _, cs := range d.sheets {
		// A cue with one FILE is the usual single-image rip: keep the sheet's
		// track numbers. With several FILEs (one per track, or per disc) the
		// numbers often restart at 01, so use a running sequence instead.
		multiFile := len(cs.sheet.Files) > 1
		seq := 0
		for _, cf := range cs.sheet.Files {
			audio, ok := resolveCueAudio(cs.dir, cf.Name, byRel, byDirBase, byBase)
			if !ok && len(cs.sheet.Files) == 1 && !strings.ContainsAny(cf.Name, `/\`) {
				// The image was renamed in tie (the cue still names the old
				// file): a lone bare FILE and a lone audio file can only mean
				// it. A path-bearing FILE that failed to resolve stays
				// unresolved rather than grabbing an unrelated file.
				if fs := byDir[strings.ToLower(path.Clean(cs.dir))]; len(fs) == 1 {
					audio, ok = fs[0], true
				}
			}
			if !ok {
				continue
			}
			if covered[audio.file.Uid] {
				continue // two sheets describing the same image: first wins
			}
			baseTrack, ok := byHash[audio.file.Uid]
			if !ok {
				continue
			}
			fileDuration := baseTrack.Duration
			added := false
			for _, ct := range cf.Tracks {
				if !ct.Audio || ct.Start < 0 {
					continue
				}
				seq++
				if fileDuration > 0 && ct.Start >= fileDuration {
					continue // index past the end of the image
				}
				end := ct.End
				if fileDuration > 0 && (end <= ct.Start || end > fileDuration) {
					end = fileDuration
				}
				if end <= ct.Start {
					end = 0
				}
				t := baseTrack
				t.Cue = &CueSegment{Sheet: cs.hash, Start: ct.Start, End: end}
				t.Title = ct.Title
				if t.Title == "" {
					// A single-image rip's embedded title is the album, not a
					// track; separate files can fall back to their own tags.
					if len(cs.sheet.Files) == 1 {
						t.Title = fmt.Sprintf("Track %02d", ct.Number)
					} else {
						t.Title = firstNonEmpty(baseTrack.Title, fmt.Sprintf("Track %02d", ct.Number))
					}
				}
				t.Artist = firstNonEmpty(ct.Performer, cs.sheet.Performer, cs.sheet.AlbumArtist, baseTrack.Artist)
				t.Album = firstNonEmpty(cs.sheet.Title, baseTrack.Album)
				if y := cueYear(cs.sheet.Date); y != "" {
					t.Year = y
				}
				if ct.Number > 0 {
					t.TrackNo = ct.Number
				} else {
					t.TrackNo = seq
				}
				if multiFile {
					t.TrackNo = seq
				}
				if audio.disc > 0 {
					t.Disc = audio.disc
				}
				t.Duration = 0
				if end > ct.Start {
					t.Duration = end - ct.Start
				}
				cueTracks = append(cueTracks, t)
				cueByHash[audio.file.Uid] = append(cueByHash[audio.file.Uid], t)
				added = true
			}
			if added {
				covered[audio.file.Uid] = true
			}
		}
	}
	if len(cueTracks) == 0 {
		return base
	}
	// Replace each covered image at its position in the base list, so the
	// album order stays stable even before the caller's disc/track sort.
	out := make([]Track, 0, len(base)+len(cueTracks))
	emitted := map[string]bool{}
	for _, t := range base {
		if !covered[t.Hash] {
			out = append(out, t)
			continue
		}
		if emitted[t.Hash] {
			continue
		}
		emitted[t.Hash] = true
		out = append(out, cueByHash[t.Hash]...)
	}
	return out
}

// resolveCueAudio maps a cue FILE name to an audio file below the album root.
// Cue paths are often Windows-style or just a bare filename, so matching
// degrades from exact relative path to same-directory basename to a unique
// basename anywhere in the album.
func resolveCueAudio(cueDir, name string, byRel, byDirBase map[string]albumTreeFile, byBase map[string][]albumTreeFile) (albumTreeFile, bool) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if name == "" {
		return albumTreeFile{}, false
	}
	clean := path.Clean(name)
	if path.IsAbs(clean) || strings.HasPrefix(clean, "../") {
		clean = path.Base(clean)
	}
	if f, ok := byRel[strings.ToLower(clean)]; ok {
		return f, true
	}
	if f, ok := byRel[strings.ToLower(path.Join(cueDir, clean))]; ok {
		return f, true
	}
	base := strings.ToLower(path.Base(clean))
	if f, ok := byDirBase[strings.ToLower(path.Clean(cueDir))+"\x00"+base]; ok {
		return f, true
	}
	if fs := byBase[base]; len(fs) == 1 {
		return fs[0], true
	}
	return albumTreeFile{}, false
}

// sameCue reports whether two queue segments denote the same range.
func sameCue(a, b *CueSegment) bool {
	if a == nil || b == nil {
		return a == b
	}
	if math.Abs(a.Start-b.Start) >= 0.0005 {
		return false
	}
	// End 0 means "unknown / until EOF" (a saved key from before the image's
	// duration was known), so it matches a now-resolved boundary.
	return a.End == 0 || b.End == 0 || math.Abs(a.End-b.End) < 0.0005
}

// cueTrackForKey re-derives a cue track's full metadata from its album, so a
// queue restored after a restart (which only knows URLs) still shows titles
// and segment lengths. It falls back to the base track with just the segment
// boundaries when the album is unavailable.
func (s *Session) cueTrackForKey(key string, base Track) *Track {
	hash, seg := SplitTrackKey(key)
	if seg == nil {
		return &base
	}
	if base.AlbumUID != "" {
		albumUID := base.AlbumUID
		if up := s.discFolderAlbum(albumUID); up != "" {
			albumUID = up // a multi-disc track's parent is its cd<N> folder
		}
		if tracks, err := s.AlbumTracks(Album{UID: albumUID, Kind: AlbumDir}); err == nil {
			for i := range tracks {
				if tracks[i].Hash == hash && sameCue(tracks[i].Cue, seg) {
					return &tracks[i]
				}
			}
		}
	}
	base.Cue = seg
	base.Duration = seg.Duration(base.Duration)
	return &base
}

// TrackForURL resolves a stream URL (queue entry or history record) to its
// track metadata, including cue-segment identity.
func (s *Session) TrackForURL(raw string) (Track, bool) {
	return s.TrackForKey(TrackKeyFromURL(raw))
}

// TrackForKey resolves a queue key (hash or hash#cue=...) to its metadata.
func (s *Session) TrackForKey(key string) (Track, bool) {
	hash, seg := SplitTrackKey(key)
	if hash == "" {
		return Track{}, false
	}
	t, ok := s.TrackForHash(hash)
	if !ok {
		return t, false
	}
	if seg == nil {
		return t, true
	}
	if ct := s.cueTrackForKey(key, t); ct != nil {
		return *ct, true
	}
	return t, true
}

// CueSegmentDuration returns a cue track's logical playback length, falling
// back to the backend-reported duration. Shared with the history recorder so
// complete/skip classification sees the segment, not the whole image.
func CueSegmentDuration(t Track, fallback float64) float64 {
	if t.Cue == nil {
		return fallback
	}
	if d := t.Cue.Duration(fallback); d > 0 {
		return d
	}
	return fallback
}

// CueSegmentPosition converts an absolute blob position into a segment-relative
// one for play history. seg is used when the track metadata was unavailable
// (a queue entry the registry could not resolve).
func CueSegmentPosition(t Track, absolute float64, seg *CueSegment) float64 {
	if t.Cue != nil {
		seg = t.Cue
	}
	if seg == nil {
		return absolute
	}
	pos := absolute - seg.Start
	if pos < 0 {
		return 0
	}
	return pos
}
