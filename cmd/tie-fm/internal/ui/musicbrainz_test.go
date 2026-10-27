package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/musicbrainz"
)

func TestContextMenuMusicBrainzItem(t *testing.T) {
	reg := fs.NewRegistry(fs.NewLocalFS(), fs.NewTieFS(nil))
	fm := &FileManager{ops: fs.NewOperations(reg), registry: reg}

	has := func(items []*fyne.MenuItem, label string) bool {
		for _, item := range items {
			if item.Label == label {
				return true
			}
		}
		return false
	}
	cases := []struct {
		name  string
		entry fs.Entry
		want  bool
	}{
		{"tie dir", fs.Entry{Name: "album", Path: "tie:/music/album", IsDir: true, Hash: "uid"}, true},
		{"tie file", fs.Entry{Name: "song.flac", Path: "tie:/music/album/song.flac", Hash: "hash"}, true},
		{"local dir", fs.Entry{Name: "music", Path: "/tmp/music", IsDir: true}, false},
		{"tie dir without hash", fs.Entry{Name: "music", Path: "tie:/music", IsDir: true}, false},
	}
	for _, c := range cases {
		items := fm.contextMenuItems(c.entry, 0)
		if got := has(items, "MusicBrainz…"); got != c.want {
			t.Errorf("%s: MusicBrainz… present = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRenderAlbumDest(t *testing.T) {
	got, ok := renderAlbumDest("/{albumartist}/{year} - {album}", "Miles Davis", "Kind of Blue", 1959)
	if !ok || got != "/Miles Davis/1959 - Kind of Blue" {
		t.Errorf("renderAlbumDest = %q, %v", got, ok)
	}
	// Slashes cannot inject path levels.
	got, ok = renderAlbumDest("/{artist}/{album}", "AC/DC", "Back in Black", 1980)
	if !ok || got != "/AC DC/Back in Black" {
		t.Errorf("sanitized render = %q, %v", got, ok)
	}
	// Missing year with a template that needs it: keep the planner destination.
	if _, ok := renderAlbumDest("/{year}/{album}", "a", "b", 0); ok {
		t.Error("expected rendering to fail without a year")
	}
	if _, ok := renderAlbumDest("", "a", "b", 2000); ok {
		t.Error("empty template must not render")
	}
}

func TestMatchAlbumTracks(t *testing.T) {
	rel := &musicbrainz.Release{
		DiscCount: 1,
		Mediums: []musicbrainz.Medium{{
			Position: 1, TrackCount: 2,
			Tracks: []musicbrainz.Track{
				{Position: 1, Disc: 1, Title: "First"},
				{Position: 2, Disc: 1, Title: "Second"},
			},
		}},
	}
	files := []albumFile{
		{hash: "h2", name: "02 second.flac", track: 2, title: "Second"},
		{hash: "h1", name: "01 first.flac", track: 1, title: "First"},
	}
	got := matchAlbumTracks(files, rel)
	if len(got) != 2 || got[0].file.hash != "h1" || got[1].file.hash != "h2" {
		t.Fatalf("track match = %+v", got)
	}

	// A file whose disc tag is missing still matches a single-disc release.
	files = []albumFile{{hash: "h1", track: 1}}
	got = matchAlbumTracks(files, rel)
	if len(got) != 1 || got[0].file.hash != "h1" {
		t.Fatalf("disc-less match = %+v", got)
	}
}

func TestTieParentPath(t *testing.T) {
	cases := map[string]string{
		"tie:/music/album/song.flac": "tie:/music/album",
		"tie:/song.flac":             "tie:/",
		"tie:/music/album/":          "tie:/music",
	}
	for in, want := range cases {
		if got := tieParentPath(in); got != want {
			t.Errorf("tieParentPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestApplyReleaseToGroupTagsOnly(t *testing.T) {
	g := client.AlbumGroup{
		Artist: "Local Artist", Album: "Local Album", Year: 2001,
		Dest: "/local/2001 - Local Album",
	}
	rel := &musicbrainz.Release{
		Title:  "MB Album",
		Artist: musicbrainz.Artist{Name: "MB Artist"},
		Year:   1999,
	}

	// Tags-only: the match is recorded but never rewrites identity/placement.
	applyReleaseToGroup(&g, rel, "/{albumartist}/{year} - {album}", true)
	if g.Artist != "Local Artist" || g.Album != "Local Album" ||
		g.Year != 2001 || g.Dest != "/local/2001 - Local Album" {
		t.Errorf("tags-only rewrote the group: %+v", g)
	}

	// A full match rewrites both the identity and the rendered destination.
	applyReleaseToGroup(&g, rel, "/{albumartist}/{year} - {album}", false)
	if g.Artist != "MB Artist" || g.Album != "MB Album" ||
		g.Year != 1999 || g.Dest != "/MB Artist/1999 - MB Album" {
		t.Errorf("full match = %+v", g)
	}
}

func TestAlbumPlanSummaryTagsOnly(t *testing.T) {
	plan := []client.AlbumGroup{{SourceDir: "/a", Size: 1024}}
	matches := map[string]*musicbrainz.Release{"/a": {Title: "x"}}

	full := albumPlanSummary(plan, []bool{true}, matches, false)
	if strings.Contains(full, "tags only") {
		t.Errorf("full-match summary mentions tags only: %q", full)
	}
	tagsOnly := albumPlanSummary(plan, []bool{true}, matches, true)
	if !strings.Contains(tagsOnly, "(tags only)") {
		t.Errorf("tags-only summary = %q", tagsOnly)
	}
}

func TestMusicBrainzTagUnion(t *testing.T) {
	merged, added := musicBrainzTagUnion(
		[]string{"KeepMe", "rock"},
		[]string{"Rock", "pop"},
		[]string{"pop", "shoegaze", " "},
	)
	wantMerged := []string{"KeepMe", "rock", "pop", "shoegaze"}
	wantAdded := []string{"pop", "shoegaze"}
	if strings.Join(merged, ",") != strings.Join(wantMerged, ",") {
		t.Errorf("merged = %v, want %v", merged, wantMerged)
	}
	if strings.Join(added, ",") != strings.Join(wantAdded, ",") {
		t.Errorf("added = %v, want %v", added, wantAdded)
	}
	if m, a := musicBrainzTagUnion([]string{"x"}, nil, nil); len(a) != 0 || len(m) != 1 {
		t.Errorf("empty update = %v, %v", m, a)
	}
}
