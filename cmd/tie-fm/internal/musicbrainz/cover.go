package musicbrainz

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxCoverArtBytes caps a downloaded cover image (500px front covers are a
// few hundred KiB; 20 MiB leaves room for unusual scans).
const maxCoverArtBytes = 20 << 20

// FetchCoverArt downloads an image (typically Release.CoverArtURL) with the
// configured User-Agent. The returned bytes are the original image.
func FetchCoverArt(ctx context.Context, u string, s Settings) ([]byte, error) {
	if strings.TrimSpace(u) == "" {
		return nil, fmt.Errorf("musicbrainz: empty cover art URL")
	}
	s = s.WithDefaults()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.UserAgent)
	client := &http.Client{Timeout: s.Timeout}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("musicbrainz: cover art: %s", resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "image/") {
		return nil, fmt.Errorf("musicbrainz: cover art: unexpected content type %q", ct)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverArtBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCoverArtBytes {
		return nil, fmt.Errorf("musicbrainz: cover art larger than %d bytes", maxCoverArtBytes)
	}
	return data, nil
}

// CoverArtExtension guesses a file extension from the image magic bytes.
func CoverArtExtension(data []byte) string {
	switch {
	case len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return ".jpg"
	case len(data) > 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return ".png"
	case len(data) > 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return ".webp"
	case len(data) > 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return ".gif"
	}
	return ".jpg"
}
