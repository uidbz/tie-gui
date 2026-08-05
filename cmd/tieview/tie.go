package main

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"slices"
	"strings"

	"git.sr.ht/~uid/tie/client"
	"git.sr.ht/~uid/tie/io/getlib"
	"git.sr.ht/~uid/tie/io/putlib"

	"git.sr.ht/~uid/imgview/gallery"
)

// httpClientForHost honors the host's Insecure flag (self-signed
// certificates); a secure host gets nil so getlib/putlib fall back to
// http.DefaultClient.
func httpClientForHost(host client.FileHost) *http.Client {
	if !host.Insecure {
		return nil
	}
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
}

// tieHostName names the filehost to fetch content from, set by the -host
// flag. Empty means the default resolution in tieFileHost.
var tieHostName string

// tieFileHost resolves the filehost used to fetch tie content: the host
// named by -host when given, the "fast" host when configured, otherwise the
// first configured default host.
func tieFileHost(tc *client.TieClient) client.FileHost {
	if tieHostName != "" {
		// Validated against the config at startup, so this always hits.
		return tc.Config.FileHosts[tieHostName]
	}
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

type tieReader struct {
	seeker io.ReadSeeker
	data   []byte
	host   client.FileHost
	client *http.Client
	hash   string
	// thumbHash is the content hash of the filehost-cached thumbnail, learned
	// from the query's expanded attributes or after uploadTieThumbnail.
	thumbHash string
	isVideo   bool
}

// IsVideo implements gallery.VideoFile so ReadCustom can set InputIsVideo.
func (t *tieReader) IsVideo() bool { return t.isVideo }

// StreamURL implements gallery.VideoStreamer. It returns the direct HTTP URL
// for this entry so libmpv can stream without downloading the blob first.
// The tie filehost URL scheme is: baseURL + "/" + contentHash.
func (t *tieReader) StreamURL() string {
	if t.host.URL == "" {
		return ""
	}
	return t.host.URL + "/" + t.hash
}

func (t *tieReader) Path() string {
	return t.hash
}

func (t *tieReader) httpClient() *http.Client {
	if t.client == nil {
		t.client = httpClientForHost(t.host)
	}
	return t.client
}

func (t *tieReader) GetReader() (io.ReadSeeker, error) {
	var err error
	var r io.Reader
	if t.seeker == nil {
		r, err = getlib.ReadFile(t.httpClient(), t.host.URL, t.hash)
		if err == nil {
			t.data, err = io.ReadAll(r)
			if err == nil {
				t.seeker = bytes.NewReader(t.data)
			}
		}
	}
	return t.seeker, err
}

// tieDirReader is the gallery entry for a tagged tie directory (tie-type
// image-dir). It is not image content — GetReader always fails; opening the
// entry browses the directory instead (Openable), and its thumbnail is a
// folder icon.
type tieDirReader struct {
	uid  client.DirUID
	open func()
}

func (t *tieDirReader) Path() string {
	return string(t.uid)
}

func (t *tieDirReader) GetReader() (io.ReadSeeker, error) {
	return nil, errors.New("tie directory is not image content: " + string(t.uid))
}

func (t *tieDirReader) Open() {
	t.open()
}

// tieRowKind is how a tag-query row appears in the gallery.
type tieRowKind int

const (
	tieRowSkip  tieRowKind = iota // hidden (audio, plain dirs, ...)
	tieRowFile                    // a viewable image
	tieRowDir                     // a browsable tagged image directory
	tieRowVideo                   // a playable video file
)

// classifyTieRow reports how a tag-query row should appear. The gallery
// shows only image-file and image-dir entries. The raw expanded attributes
// are checked (not client.File.TieType, which collapses multi-valued
// tie-types to unknown-file); the media type is the fallback discriminator
// for image files without a clean tie-type (see isImageFile).
func classifyTieRow(row client.Row) tieRowKind {
	types := client.RowValues(row, client.TieTypeProperty.String())
	if slices.Contains(types, client.TieImageDir.String()) {
		return tieRowDir
	}
	if slices.Contains(types, client.TieImageFile.String()) {
		return tieRowFile
	}
	mediaType := client.RowFirst(row, client.TieMediaType.String())
	if strings.HasPrefix(mediaType, "image/") {
		return tieRowFile
	}
	if strings.HasPrefix(mediaType, "video/") {
		return tieRowVideo
	}
	return tieRowSkip
}

// readFromTie queries tie for images carrying all of the include tags and
// none of the exclude tags, and replaces the viewer's gallery with the
// results. Tagged image directories in the results become browsable
// entries: opening one calls browseDir with the directory's UID.
func readFromTie(viewer *gallery.Viewer, tc *client.TieClient, include, exclude []string, filter string, browseDir func(client.DirUID)) {
	if len(include) == 0 {
		return
	}
	// Terms is the full AND-list of tags a match must carry; there is no
	// positional seed key. Reverse selects matches by reverse association
	// (the tag-query case). Limit -1 disables pagination. Expand attaches
	// each match's forward attributes, so thumbnail mappings ride along
	// without a per-image lookup.
	spec := client.QuerySpec{
		Terms:   include,
		Exclude: exclude,
		Reverse: true,
		Filter:  filter,
		Expand:  true,
		Limit:   -1,
	}

	viewer.ReadCustomAsync(func() []gallery.CustomReader {
		rows, _, err := tc.Query(spec)
		if err != nil {
			fmt.Println("Error happened querying tie:", err)
			return nil
		}
		host := tieFileHost(tc)
		readers := make([]gallery.CustomReader, 0, len(rows))
		for _, row := range rows {
		switch classifyTieRow(row) {
		case tieRowFile:
			readers = append(readers, &tieReader{host: host, hash: row.Key, thumbHash: client.RowFirst(row, "thumbnail")})
		case tieRowDir:
			uid := client.DirUID(row.Key)
			readers = append(readers, &tieDirReader{uid: uid, open: func() { browseDir(uid) }})
		case tieRowVideo:
			readers = append(readers, &tieReader{host: host, hash: row.Key, isVideo: true})
		}
		}
		return readers
	})
}

// filehostThumbnailer implements gallery.Thumbnailer on top of the tie
// stores: thumbnails are cached on the filehost (mapped by a
// (imageHash, "thumbnail", thumbHash) triple), not in a local directory, so
// the cache is shared by every machine with access to the same tie stores.
// On a cache miss the thumbnail is generated from the full blob and uploaded.
type filehostThumbnailer struct {
	tie       *client.TieClient
	tileWidth int
}

func (t *filehostThumbnailer) GetThumbnail(info *gallery.ImageInfo) (io.ReadSeeker, error) {
	if _, ok := info.CustomReader.(*tieDirReader); ok {
		// Directories have no image content; a folder icon marks the tile
		// clearly as a directory.
		info.ThumbnailIsScaled = true
		return folderIcon(t.tileWidth * 2), nil
	}
	tr, ok := info.CustomReader.(*tieReader)
	if !ok {
		return nil, errors.New("tie image without tie reader: " + info.Path)
	}
	if tr.isVideo {
		// Video thumbnails are handled upstream (InputIsVideo → loading placeholder).
		return nil, errors.New("video thumbnail not available")
	}
	if rs, ok := t.thumbnailReader(tr); ok {
		info.ThumbnailIsScaled = true
		return rs, nil
	}

	reader, err := info.GetReader()
	if err != nil {
		return nil, err
	}
	decoded, _, err := gallery.Decode(reader)
	if err != nil {
		return nil, err
	}
	scaled := gallery.ScaleImage(decoded, t.tileWidth*2)
	decoded = nil
	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, scaled, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	t.upload(tr, buf.Bytes())
	info.ThumbnailIsScaled = true

	return bytes.NewReader(buf.Bytes()), nil
}

// thumbnailReader returns the filehost-cached thumbnail for tr by following
// the (hash, "thumbnail", thumbHash) mapping. The mapping usually arrives
// with the query's expanded attributes (tr.thumbHash); otherwise it is
// looked up with a single Get. ok is false when no mapping exists or the
// blob is unavailable (e.g. reaped from the filehost), in which case the
// caller regenerates and re-uploads.
func (t *filehostThumbnailer) thumbnailReader(tr *tieReader) (rs io.ReadSeeker, ok bool) {
	host := tieFileHost(t.tie)
	if host.URL == "" {
		return nil, false
	}
	thumbHash := tr.thumbHash
	if thumbHash == "" {
		row, err := t.tie.Get(tr.hash)
		if err != nil {
			return nil, false
		}
		thumbHash = client.RowFirst(row, "thumbnail")
		if thumbHash == "" {
			return nil, false
		}
		tr.thumbHash = thumbHash
	}
	r, err := getlib.ReadFile(httpClientForHost(host), host.URL, thumbHash)
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
// (tr.hash, "thumbnail", thumbHash) mapping. Failures are logged; the
// generated thumbnail remains usable, just uncached.
func (t *filehostThumbnailer) upload(tr *tieReader, jpegBytes []byte) {
	host := tieFileHost(t.tie)
	if host.URL == "" {
		return
	}
	pc := putlib.PutConfig{Client: httpClientForHost(host)}
	thumbHash, err := pc.AddressOf(bytes.NewReader(jpegBytes))
	if err != nil {
		fmt.Println("Error hashing thumbnail:", err)
		return
	}
	item := pc.UploadMultipart(host.URL+"/upload/"+thumbHash, bytes.NewReader(jpegBytes), len(jpegBytes), tr.hash+".jpg")
	if item.ErrorMsg != "" {
		fmt.Println("Error uploading thumbnail:", item.ErrorMsg)
		return
	}
	if item.Hash != thumbHash {
		fmt.Println("Error uploading thumbnail: checksum mismatch")
		return
	}
	// Set (not Add) keeps the relation single-valued, so a regenerated
	// thumbnail replaces one whose blob was reaped from the filehost.
	if err := t.tie.Set(tr.hash, "thumbnail", []string{thumbHash}); err != nil {
		fmt.Println("Error saving thumbnail mapping:", err)
		return
	}
	tr.thumbHash = thumbHash
}
