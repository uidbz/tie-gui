package data

import (
	"math"
	"strings"
	"testing"

	"github.com/uidbz/tie/client"
)

func TestParseCueSingleImage(t *testing.T) {
	sheet, err := ParseCue(strings.NewReader(`
REM GENRE "Rock"
REM DATE 1997-03-04
REM ALBUM ARTIST "Various Artists"
PERFORMER "Album Artist"
TITLE "The Album"
FILE "C:\Music\album.flac" WAVE
  TRACK 01 AUDIO
    TITLE "First"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Second"
    PERFORMER "Guest"
    INDEX 00 00:30:00
    INDEX 01 00:31:15
  TRACK 03 AUDIO
    TITLE "Third"
    INDEX 01 01:00:00
`))
	if err != nil {
		t.Fatal(err)
	}
	if sheet.Title != "The Album" || sheet.Performer != "Album Artist" {
		t.Errorf("sheet metadata = %+v", sheet)
	}
	if sheet.Genre != "Rock" || sheet.Date != "1997-03-04" || sheet.AlbumArtist != "Various Artists" {
		t.Errorf("REM metadata = %+v", sheet)
	}
	if len(sheet.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(sheet.Files))
	}
	f := sheet.Files[0]
	if f.Name != `C:\Music\album.flac` || f.Type != "WAVE" {
		t.Errorf("file = %+v", f)
	}
	if len(f.Tracks) != 3 {
		t.Fatalf("tracks = %d, want 3", len(f.Tracks))
	}
	if f.Tracks[0].Title != "First" || f.Tracks[0].Start != 0 || f.Tracks[0].End != 31.2 {
		t.Errorf("track 1 = %+v", f.Tracks[0])
	}
	if f.Tracks[1].Performer != "Guest" || f.Tracks[1].Start != 31.2 || f.Tracks[1].End != 60 {
		t.Errorf("track 2 = %+v", f.Tracks[1])
	}
	if f.Tracks[2].Start != 60 || f.Tracks[2].End != 0 {
		t.Errorf("track 3 = %+v", f.Tracks[2])
	}
	if got := cueYear(sheet.Date); got != "1997" {
		t.Errorf("cueYear = %q, want 1997", got)
	}
}

func TestParseCueMultiFile(t *testing.T) {
	sheet, err := ParseCue(strings.NewReader(`
FILE "01 - One.flac" WAVE
  TRACK 01 AUDIO
    TITLE "One"
    INDEX 01 00:00:00
FILE "02 - Two.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Two"
    INDEX 01 00:00:00
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(sheet.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(sheet.Files))
	}
	for i, f := range sheet.Files {
		if len(f.Tracks) != 1 || f.Tracks[0].Start != 0 || f.Tracks[0].End != 0 {
			t.Errorf("file %d tracks = %+v", i, f.Tracks)
		}
	}
}

func TestParseCueIgnoresMalformedLines(t *testing.T) {
	sheet, err := ParseCue(strings.NewReader(`
TITLE "Album"
FILE "album.flac" WAVE
  TRACK 01 AUDIO
    INDEX nope 00:00:00
  TRACK 02 AUDIO
    INDEX 01 not-a-time
  TRACK 03 AUDIO
    TITLE "Playable"
    INDEX 01 00:10:00
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(sheet.Files) != 1 || len(sheet.Files[0].Tracks) != 3 {
		t.Fatalf("sheet = %+v", sheet)
	}
	if sheet.Files[0].Tracks[0].Start != -1 || sheet.Files[0].Tracks[1].Start != -1 {
		t.Errorf("malformed indexes were accepted: %+v", sheet.Files[0].Tracks)
	}
	if sheet.Files[0].Tracks[2].Start != 10 {
		t.Errorf("track 3 start = %v, want 10", sheet.Files[0].Tracks[2].Start)
	}
}

func TestParseCueTime(t *testing.T) {
	tests := map[string]float64{
		"00:00:00":  0,
		"01:02:03":  62.04,
		"100:00:00": 6000,
		"03:04":     184,
	}
	for in, want := range tests {
		got, err := parseCueTime(in)
		if err != nil || math.Abs(got-want) > 1e-9 {
			t.Errorf("parseCueTime(%q) = (%v, %v), want %v", in, got, err, want)
		}
	}
	if _, err := parseCueTime("bad"); err == nil {
		t.Error("parseCueTime(bad) succeeded")
	}
}

func TestTrackKeyRoundTrip(t *testing.T) {
	hash := strings.Repeat("a", 64)
	plain := Track{Hash: hash}
	if plain.Key() != hash {
		t.Errorf("plain Key = %q", plain.Key())
	}
	seg := &CueSegment{Start: 12.345, End: 67.891}
	track := Track{Hash: hash, Cue: seg}
	key := track.Key()
	if want := hash + "#cue=12345-67891"; key != want {
		t.Errorf("cue Key = %q, want %q", key, want)
	}
	gotHash, gotSeg := SplitTrackKey(key)
	if gotHash != hash || !sameCue(gotSeg, seg) {
		t.Errorf("SplitTrackKey = (%q, %+v)", gotHash, gotSeg)
	}
	if got := TrackKeyFromURL("http://fh:1162/" + key + "?x=1"); got != key {
		t.Errorf("TrackKeyFromURL = %q, want %q", got, key)
	}
	if got := TrackKeyFromURL("http://fh:1162/" + hash); got != hash {
		t.Errorf("TrackKeyFromURL plain = %q", got)
	}
}

func TestMergeCueTracksSingleImage(t *testing.T) {
	sheet, err := ParseCue(strings.NewReader(`
REM DATE 1997
PERFORMER "Album Artist"
TITLE "The Album"
FILE "album.flac" WAVE
  TRACK 01 AUDIO
    TITLE "First"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Second"
    INDEX 01 00:30:00
  TRACK 03 DATA
    INDEX 01 00:40:00
  TRACK 04 AUDIO
    TITLE "Fourth"
    INDEX 01 00:50:00
`))
	if err != nil {
		t.Fatal(err)
	}
	base := []Track{
		{Hash: "img", Filename: "album.flac", Title: "Image", Artist: "Tag Artist", Album: "Tag Album", Duration: 100, AlbumUID: "album"},
		{Hash: "bonus", Filename: "bonus.flac", Title: "Bonus", Duration: 20, AlbumUID: "album"},
	}
	d := &cueAlbumData{
		sheets: []cueSource{{hash: "cuehash", sheet: sheet}},
		files: []albumTreeFile{
			{file: client.File{Uid: "img", Filename: "album.flac", TieType: client.TieAudioFile}, rel: "album.flac"},
			{file: client.File{Uid: "bonus", Filename: "bonus.flac", TieType: client.TieAudioFile}, rel: "bonus.flac"},
		},
	}
	got := mergeCueTracks(base, d)
	if len(got) != 4 {
		t.Fatalf("tracks = %d, want 3 cue tracks + bonus: %+v", len(got), got)
	}
	if got[0].Hash != "img" || got[0].Cue == nil || got[0].Cue.Start != 0 || got[0].Cue.End != 30 {
		t.Errorf("track 1 = %+v", got[0])
	}
	if got[0].Title != "First" || got[0].Artist != "Album Artist" || got[0].Album != "The Album" || got[0].Year != "1997" {
		t.Errorf("track 1 metadata = %+v", got[0])
	}
	if got[0].Duration != 30 || got[0].TrackNo != 1 {
		t.Errorf("track 1 duration/track = %+v", got[0])
	}
	// The DATA track bounds the preceding audio track and is not playable.
	if got[1].Cue.Start != 30 || got[1].Cue.End != 40 || got[1].Duration != 10 {
		t.Errorf("track 2 = %+v", got[1])
	}
	if got[2].Cue.Start != 50 || got[2].Cue.End != 100 || got[2].Duration != 50 {
		t.Errorf("last track = %+v", got[2])
	}
	if got[3].Hash != "bonus" || got[3].Cue != nil {
		t.Errorf("uncovered track = %+v", got[3])
	}
}

// A cue that still names the pre-rename file must still drive a lone image.
func TestMergeCueTracksRenamedImage(t *testing.T) {
	sheet, err := ParseCue(strings.NewReader(`
FILE "old-name.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Only"
    INDEX 01 00:00:00
`))
	if err != nil {
		t.Fatal(err)
	}
	base := []Track{{Hash: "img", Filename: "new-name.flac", Duration: 60, AlbumUID: "album"}}
	d := &cueAlbumData{
		sheets: []cueSource{{hash: "cue", sheet: sheet}},
		files: []albumTreeFile{{
			file: client.File{Uid: "img", Filename: "new-name.flac", TieType: client.TieAudioFile},
			rel:  "new-name.flac",
		}},
	}
	got := mergeCueTracks(base, d)
	if len(got) != 1 || got[0].Cue == nil || got[0].Title != "Only" {
		t.Fatalf("merged = %+v", got)
	}
}

func TestArchiveCueTracks(t *testing.T) {
	fh := newFakeFilehost(t)
	cue := []byte(`TITLE "Archive Album"
PERFORMER "Archive Artist"
FILE "album.mp3" MP3
  TRACK 01 AUDIO
    TITLE "One"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Two"
    INDEX 01 00:30:00
`)
	audio := append(append([]byte{}, mp3ish...), []byte("archive image body")...)
	zipBlob := zipOf(t, map[string][]byte{
		"album.cue": cue,
		"album.mp3": audio,
	})
	zipHash := fh.store(t, zipBlob)
	fh.zipHash = zipHash

	s := archiveSession(fh.URL)
	tracks, err := s.AlbumTracks(Album{UID: zipHash, Kind: AlbumArchive, Title: "Archive Album"})
	if err != nil {
		t.Fatalf("AlbumTracks: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("tracks = %d, want 2: %+v", len(tracks), tracks)
	}
	if tracks[0].Title != "One" || tracks[1].Title != "Two" {
		t.Errorf("titles = %q, %q", tracks[0].Title, tracks[1].Title)
	}
	if tracks[0].Hash != tracks[1].Hash || tracks[0].Cue == nil || tracks[1].Cue == nil {
		t.Fatalf("cue tracks do not share the image hash: %+v", tracks)
	}
	if tracks[0].Cue.Start != 0 || tracks[0].Cue.End != 30 || tracks[1].Cue.Start != 30 {
		t.Errorf("segments = %+v, %+v", tracks[0].Cue, tracks[1].Cue)
	}
	if tracks[0].AlbumUID != zipHash {
		t.Errorf("AlbumUID = %q, want %q", tracks[0].AlbumUID, zipHash)
	}
}

func TestMergeCueTracksDiscDirectory(t *testing.T) {
	sheet, err := ParseCue(strings.NewReader(`
FILE "album.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Disc Two Track"
    INDEX 01 00:00:00
`))
	if err != nil {
		t.Fatal(err)
	}
	base := []Track{{Hash: "img", Filename: "album.flac", Duration: 60, AlbumUID: "album"}}
	d := &cueAlbumData{
		sheets: []cueSource{{hash: "cue", dir: "cd2", sheet: sheet}},
		files: []albumTreeFile{{
			file: client.File{Uid: "img", Filename: "album.flac", TieType: client.TieAudioFile},
			rel:  "cd2/album.flac", dir: "cd2", disc: 2,
		}},
	}
	got := mergeCueTracks(base, d)
	if len(got) != 1 || got[0].Disc != 2 || got[0].Title != "Disc Two Track" {
		t.Fatalf("merged = %+v", got)
	}
}
