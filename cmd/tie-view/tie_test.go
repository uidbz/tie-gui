package main

import (
	"testing"

	"git.sr.ht/~uid/tie/client"

	"github.com/uidbz/tie-gui/gallery"
)

func tieRow(types []string, mediaType string) client.Row {
	attrs := map[string][]string{}
	if types != nil {
		attrs["tie-type"] = types
	}
	if mediaType != "" {
		attrs["media-type"] = []string{mediaType}
	}
	return client.Row{Attributes: attrs}
}

// TestClassifyTieRow: the gallery shows image files and browsable image
// directories only. Multi-valued tie-types and the media-type fallback must
// be handled (files imported by older tie versions carry tie-type
// unknown-file with a reliable media-type).
func TestClassifyTieRow(t *testing.T) {
	cases := []struct {
		name string
		row  client.Row
		want tieRowKind
	}{
		{"tagged image dir", tieRow([]string{"directory", "image-dir"}, ""), tieRowDir},
		{"image file", tieRow([]string{"file", "image-file"}, "image/jpeg"), tieRowFile},
		{"image by media type", tieRow([]string{"unknown-file"}, "image/jpeg"), tieRowFile},
		{"image by media type, no tie-type", tieRow(nil, "image/png"), tieRowFile},
		{"audio file", tieRow([]string{"file", "audio-file"}, "audio/x-flac"), tieRowSkip},
		{"audio by media type", tieRow(nil, "audio/x-flac"), tieRowSkip},
		{"plain directory", tieRow([]string{"directory"}, ""), tieRowSkip},
		{"audio dir", tieRow([]string{"directory", "audio-dir"}, ""), tieRowSkip},
		{"empty", tieRow(nil, ""), tieRowSkip},
	}
	for _, c := range cases {
		if got := classifyTieRow(c.row); got != c.want {
			t.Errorf("%s: classifyTieRow = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestFolderIcon: the icon must be a decodable PNG of the requested size.
func TestFolderIcon(t *testing.T) {
	img, format, err := gallery.Decode(folderIcon(128))
	if err != nil {
		t.Fatal("folder icon does not decode:", err)
	}
	if format != "png" {
		t.Errorf("format = %q, want png", format)
	}
	if b := img.Bounds(); b.Max.X != 128 || b.Max.Y != 128 {
		t.Errorf("bounds = %v, want 128x128", b)
	}
}
