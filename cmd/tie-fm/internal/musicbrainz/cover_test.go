package musicbrainz

import "testing"

func TestCoverArtExtension(t *testing.T) {
	cases := []struct {
		data []byte
		want string
	}{
		{[]byte{0xFF, 0xD8, 0xFF, 0xE0}, ".jpg"},
		{[]byte("\x89PNG\r\n\x1a\nxxxx"), ".png"},
		{[]byte("RIFFxxxxWEBPxxxx"), ".webp"},
		{[]byte("GIF89axxxx"), ".gif"},
		{[]byte("junk"), ".jpg"},
	}
	for _, c := range cases {
		if got := CoverArtExtension(c.data); got != c.want {
			t.Errorf("CoverArtExtension(%q) = %q, want %q", c.data, got, c.want)
		}
	}
}
