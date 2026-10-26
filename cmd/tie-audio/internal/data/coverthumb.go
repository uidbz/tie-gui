package data

import (
	"bytes"
	"fmt"
	_ "image/gif" // cover decode
	"image/jpeg"
	_ "image/png" // cover decode

	"github.com/disintegration/imaging"
	"github.com/uidbz/tie/client"
	"github.com/uidbz/tie/io/putlib"

	"github.com/uidbz/tie-gui/gallery"
)

// coverThumbEdge is the pixel edge album thumbnails are downscaled to before
// being cached on the filehost. It matches the cover wall's decode-side cap
// (coverMaxEdge in the ui package) and tie-view's thumbnail size, so every
// consumer draws the same small blob.
const coverThumbEdge = 512

// coverThumbnail turns a raw cover image into the small JPEG cached under
// (uid, "thumbnail", thumbHash) and returns those bytes, or nil when raw is
// not a decodable image. Publishing is best effort: the returned thumbnail is
// still usable if the upload or the relation write fails.
//
// Cache-first is the whole point: an album's artwork may live inside a
// multi-megabyte track or a full-size scan, and resolving it again on every
// launch turns the cover wall into a bulk download. Once a thumbnail is
// recorded, coverBytes fetches it directly — the same path tie-view uses.
func (s *Session) coverThumbnail(uid string, raw []byte) []byte {
	// gallery.Decode applies EXIF orientation and is the same decoder the
	// cover wall used before thumbnails were cached; re-encoding drops the
	// EXIF block, so the rotation has to happen here, once.
	decoded, _, err := gallery.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	if b := decoded.Bounds(); b.Dx() > coverThumbEdge || b.Dy() > coverThumbEdge {
		decoded = imaging.Fit(decoded, coverThumbEdge, coverThumbEdge, imaging.Lanczos)
	}
	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, decoded, &jpeg.Options{Quality: 90}); err != nil {
		return nil
	}
	thumb := buf.Bytes()
	s.publishCoverThumbnail(uid, thumb)
	return thumb
}

// publishCoverThumbnail uploads jpegBytes to the filehost and records
// (uid, "thumbnail", thumbHash). Failures are logged and ignored: the caller
// still has usable bytes, the album just stays uncached and is re-derived next
// time.
func (s *Session) publishCoverThumbnail(uid string, jpegBytes []byte) {
	host := s.Host()
	if uid == "" || host.URL == "" {
		return
	}
	pc := putlib.PutConfig{Client: client.HTTPClientFor(host), Store: host.Store}
	thumbHash, err := pc.AddressOf(bytes.NewReader(jpegBytes))
	if err != nil {
		return
	}
	item := pc.UploadMultipart(client.FileHostClient(host), thumbHash,
		bytes.NewReader(jpegBytes), len(jpegBytes), thumbName(uid))
	if item.ErrorMsg != "" {
		fmt.Println("Error uploading cover thumbnail:", item.ErrorMsg)
		return
	}
	if item.Hash != thumbHash {
		fmt.Println("Error uploading cover thumbnail: checksum mismatch")
		return
	}
	if err := s.Tie.Set(uid, "thumbnail", []string{thumbHash}); err != nil {
		fmt.Println("Error recording cover thumbnail:", err)
		return
	}
	// Later tiles in this session (and the queue/transport) can fetch the
	// thumbnail directly instead of re-reading the subject's row first.
	s.thumbHints.Store(uid, thumbHash)
}

// thumbName is the upload filename shown by the filehost's transfer log; it
// only needs to be recognisable, not unique.
func thumbName(uid string) string {
	if len(uid) > 8 {
		uid = uid[:8]
	}
	return uid + "-cover.jpg"
}
