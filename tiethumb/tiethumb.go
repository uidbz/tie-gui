// Package tiethumb implements gallery.Thumbnailer on top of the tie stores:
// thumbnails are cached on the filehost (mapped by a
// (imageHash, "thumbnail", thumbHash) triple), not in a local directory, so
// the cache is shared by every machine with access to the same tie stores.
// On a cache miss the thumbnail is generated from the full blob, uploaded,
// and the blob's pixel dimensions are recorded as (hash, "dimensions", "WxH").
//
// It is shared by tie-view (whose filehostThumbnailer it generalizes) and
// tie-fm's preview grid. A reader opts into the filehost cache lookup by
// implementing ThumbReader (tie-view's *tieReader does); any other
// CustomReader (archive members, tie-fm's entry readers) is thumbnailed
// on the fly from its bytes, and the result is still uploaded and mapped
// when the reader's Path is a 64-char content hash.
package tiethumb

import (
	"bytes"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"strconv"
	"strings"

	"github.com/uidbz/tie/client"
	"github.com/uidbz/tie/io/getlib"
	"github.com/uidbz/tie/io/putlib"

	"github.com/uidbz/tie-gui/gallery"
)

// ThumbReader is the interface between Thumbnailer and a content-addressed
// reader (e.g. tie-view's *tieReader). The thumbnailer reads the cached
// thumbnail hash (pre-populated from a query's expanded attributes) and
// writes back the hash and dimensions after generating a thumbnail, so a
// later query or DimensionProvider sees them without a network round-trip.
type ThumbReader interface {
	Path() string // content hash (64 hex chars)
	ThumbHash() string
	SetThumbCache(thumbHash, dimensions string)
	IsVideo() bool
}

// Thumbnailer implements gallery.Thumbnailer; see the package doc.
type Thumbnailer struct {
	tie  func() *client.TieClient
	host func() client.FileHost
	// tileWidth is the gallery's target row height; thumbnails are generated
	// at 2× that width.
	tileWidth int
	// fallback, when non-nil, supplies the thumbnail for readers that carry
	// no image bytes (e.g. tie-view's directory/archive readers without
	// usable previews). Nil leaves the generic path to error out, which the
	// gallery treats as "keep the placeholder".
	fallback func(info *gallery.ImageInfo) (io.ReadSeeker, error)
}

// New returns a Thumbnailer caching thumbnails on a tie filehost. tie and
// host are resolved per call, so client/collection switches and host-config
// changes are picked up by already-built galleries; tie may return nil (no
// client bound), in which case lookups and uploads are skipped. fallback is
// optional (see the struct doc).
func New(tie func() *client.TieClient, host func() client.FileHost, tileWidth int, fallback func(info *gallery.ImageInfo) (io.ReadSeeker, error)) *Thumbnailer {
	return &Thumbnailer{tie: tie, host: host, tileWidth: tileWidth, fallback: fallback}
}

// FileHost resolves the filehost used to fetch tie content: the "fast" host
// when configured, otherwise the first configured default host. It is the
// flag-free variant of tie-view's tieFileHost, suitable for embedding apps
// that have no -host flag.
func FileHost(tc *client.TieClient) client.FileHost {
	if host, ok := tc.Config.FileHosts["fast"]; ok {
		return host
	}
	for _, name := range tc.Config.DefaultFileHosts {
		if host, ok := tc.Config.FileHosts[name]; ok {
			return host
		}
	}
	return client.FileHost{}
}

func (t *Thumbnailer) GetThumbnail(info *gallery.ImageInfo) (io.ReadSeeker, error) {
	tr, _ := info.CustomReader.(ThumbReader)
	if tr != nil && tr.IsVideo() {
		// Video thumbnails are handled upstream (InputIsVideo → frame extraction).
		return nil, errors.New("tiethumb: video thumbnail not available")
	}
	if tr != nil {
		if rs, ok := t.thumbnailReader(tr); ok {
			info.ThumbnailIsScaled = true
			return rs, nil
		}
	}

	reader, err := info.GetReader()
	if err != nil {
		if t.fallback != nil {
			return t.fallback(info)
		}
		return nil, err
	}
	decoded, _, err := gallery.Decode(reader)
	if err != nil {
		return nil, err
	}
	origW := decoded.Bounds().Max.X
	origH := decoded.Bounds().Max.Y
	scaled := gallery.ScaleImage(decoded, t.tileWidth*2)
	decoded = nil
	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, scaled, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	dims := fmt.Sprintf("%dx%d", origW, origH)
	// Upload and record the mapping when the entry is content-addressed:
	// a ThumbReader, or any reader whose Path is a bare content hash (tie-fm's
	// tie entries). The latter can always re-fetch by hash, which is what the
	// (hash, "thumbnail", thumbHash) relation requires.
	if tc := t.tie(); tc != nil {
		if tr != nil {
			t.upload(tc, tr, buf.Bytes(), dims)
		} else if hash := info.Path; len(hash) == 64 && !strings.Contains(hash, "/") {
			if _, err := UploadThumbnail(tc, t.host(), hash, buf.Bytes()); err == nil {
				tc.Set(hash, "dimensions", []string{dims})
			} else {
				fmt.Println("Error uploading thumbnail:", err)
			}
		}
	}
	// Make dimensions available for the current session without waiting for
	// the next query to return them.
	info.Width = origW
	info.Height = origH
	info.ThumbnailIsScaled = true

	return bytes.NewReader(buf.Bytes()), nil
}

// thumbnailReader returns the filehost-cached thumbnail for tr by following
// the (hash, "thumbnail", thumbHash) mapping. The mapping usually arrives
// with the query's expanded attributes (ThumbHash); otherwise it is looked
// up with a single Get. ok is false when no mapping exists or the blob is
// unavailable (e.g. reaped from the filehost), in which case the caller
// regenerates and re-uploads.
func (t *Thumbnailer) thumbnailReader(tr ThumbReader) (rs io.ReadSeeker, ok bool) {
	tc := t.tie()
	host := t.host()
	if tc == nil || host.URL == "" {
		return nil, false
	}
	thumbHash := tr.ThumbHash()
	if thumbHash == "" {
		row, err := tc.Get(tr.Path())
		if err != nil {
			return nil, false
		}
		thumbHash = client.RowFirst(row, "thumbnail")
		if thumbHash == "" {
			return nil, false
		}
		// The same row may carry the dimensions; pass them along so the
		// reader can serve DimensionProvider without another Get.
		tr.SetThumbCache(thumbHash, client.RowFirst(row, "dimensions"))
	}
	r, err := getlib.ReadFile(client.HTTPClientFor(host), host.URL, thumbHash)
	if err != nil {
		return nil, false
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, false
	}
	return bytes.NewReader(data), true
}

// upload stores jpegBytes on the filehost and records the
// (tr.Path(), "thumbnail", thumbHash) and (tr.Path(), "dimensions", "WxH")
// mappings. Failures are logged; the generated thumbnail remains usable,
// just uncached.
func (t *Thumbnailer) upload(tc *client.TieClient, tr ThumbReader, jpegBytes []byte, dims string) {
	thumbHash, err := UploadThumbnail(tc, t.host(), tr.Path(), jpegBytes)
	if err != nil {
		fmt.Println("Error uploading thumbnail:", err)
		return
	}
	if err := tc.Set(tr.Path(), "dimensions", []string{dims}); err != nil {
		fmt.Println("Error saving dimensions:", err)
		return
	}
	tr.SetThumbCache(thumbHash, dims)
}

// UploadThumbnail stores jpegBytes on the filehost and records the
// (ownerHash, "thumbnail", thumbHash) mapping, returning the thumbHash.
// Set (not Add) keeps the relation single-valued, so regenerated values
// replace ones whose blobs were reaped from the filehost.
func UploadThumbnail(tc *client.TieClient, host client.FileHost, ownerHash string, jpegBytes []byte) (string, error) {
	if host.URL == "" {
		return "", errors.New("no filehost URL configured")
	}
	pc := putlib.PutConfig{Client: client.HTTPClientFor(host), Store: host.Store}
	thumbHash, err := pc.AddressOf(bytes.NewReader(jpegBytes))
	if err != nil {
		return "", err
	}
	item := pc.UploadMultipart(host.URL+"/upload/"+thumbHash, bytes.NewReader(jpegBytes), len(jpegBytes), ownerHash+".jpg")
	if item.ErrorMsg != "" {
		return "", errors.New(item.ErrorMsg)
	}
	if item.Hash != thumbHash {
		return "", errors.New("checksum mismatch uploading thumbnail")
	}
	if err := tc.Set(ownerHash, "thumbnail", []string{thumbHash}); err != nil {
		return "", err
	}
	return thumbHash, nil
}

// FetchBlob downloads a blob from the filehost.
func FetchBlob(host client.FileHost, hash string) ([]byte, error) {
	r, err := getlib.ReadFile(client.HTTPClientFor(host), host.URL, hash)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// ParseDimensions parses a "WxH" tie dimensions string (e.g. "3840x2160")
// into width and height. ok is false for malformed or non-positive values.
func ParseDimensions(s string) (w, h int, ok bool) {
	parts := strings.SplitN(s, "x", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(parts[0])
	h, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}
