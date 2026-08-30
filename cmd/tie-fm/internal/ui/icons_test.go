package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

func TestIconNamesFor(t *testing.T) {
	cases := map[string]string{
		"report.pdf":  "application-pdf",
		"photo.JPG":   "image-jpeg",
		"clip.mkv":    "video-x-matroska",
		"song.flac":   "audio-x-flac",
		"notes.txt":   "text-x-generic",
		"main.go":     "text-x-go",
		"archive.tar": "application-x-tar",
		"noext":       "application-octet-stream",
	}
	for name, want := range cases {
		got := iconNamesFor(name)
		if len(got) == 0 || got[0] != want {
			t.Errorf("iconNamesFor(%q) first = %v, want %q", name, got, want)
		}
	}
}

// FileIcon must resolve to a real embedded Breeze SVG (non-fallback) for common
// types in both variants.
func TestFileIconResolvesBreeze(t *testing.T) {
	for _, v := range []struct {
		name    string
		variant fyne.ThemeVariant
	}{{"light", theme.VariantLight}, {"dark", theme.VariantDark}} {
		for _, fn := range []string{"a.pdf", "b.png", "c.mp4", "d.txt"} {
			r := icons.FileIcon(fn, v.variant)
			if r == nil {
				t.Errorf("%s: FileIcon(%q) nil", v.name, fn)
				continue
			}
			// Fyne's fallback file icon is named differently than our Breeze
			// resources (which we name "<icon>.svg").
			if r.Name() == theme.FileIcon().Name() {
				t.Errorf("%s: FileIcon(%q) fell back to theme icon", v.name, fn)
			}
		}
	}
}
