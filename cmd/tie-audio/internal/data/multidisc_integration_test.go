package data

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/uidbz/tie/client"
)

func randKey() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// fakeTrack writes the triples an audio import writes, without a blob.
func fakeTrack(t *testing.T, tc *client.TieClient, parent client.DirUID, n int, tag string) string {
	t.Helper()
	h := randKey()
	b := tc.NewBatch()
	for rel, v := range map[string]string{
		"tie-type": "audio-file", "media-type": "audio/flac", "filename": fmt.Sprintf("%02d.flac", n),
		"title": fmt.Sprintf("T%d", n), "artist": "A", "album": "Multi", "track": strconv.Itoa(n),
		"duration": "60.000", "tag": tag, "parent": string(parent),
	} {
		b.Add(h, rel, v)
	}
	if _, err := tc.Batch(b); err != nil {
		t.Fatal(err)
	}
	return h
}

// A multi-disc album files its tracks below cd<N>/: it must open with every
// track in disc order — before indexing (directory walk) and after (one
// member-of query) — and show as one tile in a tag query.
func TestMultiDiscAlbumIntegration(t *testing.T) {
	requireTestEnv(t)
	s := &Session{Tie: client.NewTieClient(testEnvConfig())}
	tc := s.Tie
	root := fmt.Sprintf("tie:/tie-audio-test/multi-%d", time.Now().UnixNano())
	tag := "multidisc-" + randKey()[:8]
	album, err := tc.MkTieDirAll(root)
	if err != nil {
		t.Fatal(err)
	}
	cd1, _ := tc.MkTieDirAll(root + "/cd1")
	cd2, _ := tc.MkTieDirAll(root + "/CD 2")
	if err := tc.SetDirType(album, "audio-dir"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []client.DirUID{album, cd1, cd2} {
		if _, err := tc.Add(string(d), "tag", tag); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		fakeTrack(t, tc, cd1, 1, tag), fakeTrack(t, tc, cd1, 2, tag), fakeTrack(t, tc, cd2, 1, tag),
	}
	a := Album{UID: string(album), Kind: AlbumDir}

	check := func(stage string) {
		tracks, err := s.AlbumTracks(a)
		if err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		var got []string
		for _, tr := range tracks {
			got = append(got, tr.Hash)
			if tr.AlbumUID != a.UID {
				t.Errorf("%s: track album %s", stage, tr.AlbumUID)
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: tracks %v, want %v", stage, got, want)
		}
	}
	check("unindexed")
	albums, err := s.QueryAlbums([]string{tag}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 || albums[0].UID != a.UID {
		t.Errorf("tag query shows %d tiles: %+v", len(albums), albums)
	}

	if _, err := tc.IndexCollection(album, client.IndexOptions{}); err != nil {
		t.Fatal(err)
	}
	check("indexed")
	latest, err := s.LatestAlbums()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(latest, func(x Album) bool { return x.UID == a.UID })
	if i < 0 || latest[i].TrackCount != 3 || latest[i].AddedAt.IsZero() || latest[i].Duration != 180 {
		t.Errorf("latest listing entry: %+v", latest)
	}

	// A saved playlist of two of them opens in order, each track keeping its
	// album (not a disc folder).
	pl, err := s.SaveQueueAsPlaylist("p", []string{want[2], want[0]})
	if err != nil {
		t.Fatal(err)
	}
	tracks, err := s.AlbumTracks(pl)
	if err != nil || len(tracks) != 2 || tracks[0].Hash != want[2] || tracks[0].AlbumUID != a.UID {
		t.Fatalf("playlist tracks %+v %v", tracks, err)
	}
	if err := tc.DeletePlaylist(pl.UID); err != nil {
		t.Fatal(err)
	}
}
