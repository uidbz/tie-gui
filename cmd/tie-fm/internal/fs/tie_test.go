package fs

import (
	"testing"

	"github.com/uidbz/tie/client"
)

func TestTiePath(t *testing.T) {
	cases := map[string]string{
		"tie:/":            "/",
		"tie:":             "/",
		"tie:/music":       "/music",
		"tie:/music/album": "/music/album",
		"tie:music":        "/music",
	}
	for in, want := range cases {
		if got := tiePath(in); got != want {
			t.Errorf("tiePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTieURI(t *testing.T) {
	s := client.FileURIScheme // the tie client's internal path scheme
	cases := map[string]string{
		s + "/music/album": "tie:/music/album",
		"/music/album":     "tie:/music/album",
		s + "/":            "tie:/",
		"":                 "tie:/",
	}
	for in, want := range cases {
		if got := tieURI(in); got != want {
			t.Errorf("tieURI(%q) = %q, want %q", in, got, want)
		}
	}
}
