package main

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"git.sr.ht/~uid/tie/client"
	"git.sr.ht/~uid/tie/io/archivelib"
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
	// dimensions is the original image size as "WxH", pre-populated from the
	// query's expanded attributes so placeholder tiles have the correct aspect
	// ratio before the thumbnail blob is fetched.
	dimensions string
	// filename is the original filename from tie metadata, pre-populated from
	// the query's expanded attributes for display in the gallery label.
	filename string
	isVideo  bool
}

// Dimensions implements gallery.DimensionProvider. It parses the "WxH" string
// stored in tie metadata and returns the original image pixel dimensions.
// Returns (0, 0) when no dimensions have been stored yet.
func (t *tieReader) Dimensions() (int, int) {
	w, h, ok := parseDimensions(t.dimensions)
	if !ok {
		return 0, 0
	}
	return w, h
}

// parseDimensions parses a "WxH" string into width and height integers.
func parseDimensions(s string) (w, h int, ok bool) {
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

func (t *tieReader) DisplayName() string {
	if t.filename != "" {
		return t.filename
	}
	// Fallback: show first 8 chars of hash (content-addressed identifier)
	if len(t.hash) >= 8 {
		return t.hash[:8] + "..."
	}
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
// entry browses the directory instead (Openable). Its thumbnail is the first
// image inside (badged with a folder icon by the gallery), and swiping the
// tile cycles through the directory's images (PreviewProvider).
type tieDirReader struct {
	uid  client.DirUID
	tc   *client.TieClient
	host client.FileHost
	open func()
}

func (t *tieDirReader) Path() string {
	return string(t.uid)
}

func (t *tieDirReader) DisplayName() string {
	// Directory UID is like "dir-name" or "parent/child", extract last component
	return filepath.Base(string(t.uid))
}

func (t *tieDirReader) GetReader() (io.ReadSeeker, error) {
	return nil, errors.New("tie directory is not image content: " + string(t.uid))
}

func (t *tieDirReader) Open() {
	t.open()
}

// Previews implements gallery.PreviewProvider: the directory's image files
// as tieReaders. Called lazily from a gallery loader goroutine.
func (t *tieDirReader) Previews() ([]gallery.CustomReader, error) {
	dir, err := client.ReadTieDir(t.tc, t.uid)
	if err != nil {
		return nil, err
	}
	readers := make([]gallery.CustomReader, 0, len(dir.Files))
	for _, f := range dir.Files {
		if !isImageFile(f) {
			continue
		}
		readers = append(readers, &tieReader{host: t.host, hash: f.Uid, filename: f.Filename})
	}
	return readers, nil
}

// tieArchiveReader is the gallery entry for an archive blob (tie-type
// image-archive / audio-archive / ...). Like a directory it is not image
// content itself: opening it browses the archive's image members (Openable).
// Its tile thumbnail is the server-cached cover when available
// (CoverProvider, tie relation (hash, "thumbnail", thumbHash)); otherwise
// the first image member is extracted — which downloads the blob once — and
// stored as the cover for next time. Swiping the tile cycles through the
// members (PreviewProvider).
type tieArchiveReader struct {
	hash      string
	filename  string
	host      client.FileHost
	tc        *client.TieClient
	thumbHash string // cover thumbnail hash, from expanded query attrs or after StoreCoverThumbnail
	open      func()
}

func (t *tieArchiveReader) Path() string { return t.hash }

func (t *tieArchiveReader) DisplayName() string {
	if t.filename != "" {
		return t.filename
	}
	if len(t.hash) >= 8 {
		return t.hash[:8] + "..."
	}
	return t.hash
}

func (t *tieArchiveReader) GetReader() (io.ReadSeeker, error) {
	return nil, errors.New("tie archive is not image content: " + t.hash)
}

func (t *tieArchiveReader) Open() { t.open() }

// archiveFetchSem bounds concurrent full-archive downloads: each download
// holds the whole blob in memory, and a page of archives without cached
// covers would otherwise fetch them all at once (one per loader worker).
var archiveFetchSem = make(chan struct{}, 2)

// Previews implements gallery.PreviewProvider: the archive blob is fetched
// once and its image members become preview readers sharing the blob.
// Called lazily from a gallery loader goroutine.
func (t *tieArchiveReader) Previews() ([]gallery.CustomReader, error) {
	archiveFetchSem <- struct{}{}
	defer func() { <-archiveFetchSem }()

	data, err := fetchBlob(t.host, t.hash)
	if err != nil {
		return nil, err
	}
	members, err := archivelib.List(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	readers := make([]gallery.CustomReader, 0, len(members))
	for _, m := range members {
		if m.Kind != archivelib.Image {
			continue
		}
		readers = append(readers, &archiveMemberReader{archiveHash: t.hash, data: data, member: m})
	}
	return readers, nil
}

// CoverThumbnail implements gallery.CoverProvider: the archive's
// server-cached cover thumbnail, resolved via the (hash, "thumbnail",
// thumbHash) relation. Avoids downloading the archive blob. Returns an
// error when no cover exists yet (the caller then generates and stores one
// via the PreviewProvider path).
func (t *tieArchiveReader) CoverThumbnail() (io.ReadSeeker, error) {
	thumbHash := t.thumbHash
	if thumbHash == "" {
		row, err := t.tc.Get(t.hash)
		if err != nil {
			return nil, err
		}
		thumbHash = client.RowFirst(row, "thumbnail")
		if thumbHash == "" {
			return nil, errors.New("no cover thumbnail for archive " + t.hash)
		}
		t.thumbHash = thumbHash
	}
	data, err := fetchBlob(t.host, thumbHash)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// StoreCoverThumbnail implements gallery.CoverProvider: uploads the
// generated cover thumbnail and records the (hash, "thumbnail", thumbHash)
// mapping. Failures are logged; the generated thumbnail remains usable,
// just uncached.
func (t *tieArchiveReader) StoreCoverThumbnail(jpegBytes []byte) {
	thumbHash, err := uploadThumbnail(t.tc, t.host, t.hash, jpegBytes)
	if err != nil {
		fmt.Println("Error storing archive cover thumbnail:", err)
		return
	}
	t.thumbHash = thumbHash
}

// archiveMemberReader serves one image member's bytes out of an archive blob
// that has already been fetched into memory. The whole archive is shared across
// its members (viewing an archive means holding it), and each member's
// decompressed bytes are extracted lazily on first read.
type archiveMemberReader struct {
	archiveHash string
	data        []byte // the whole archive blob, shared between members
	member      archivelib.Member
	seeker      io.ReadSeeker
}

func (a *archiveMemberReader) Path() string { return a.archiveHash + "/" + a.member.Name }

func (a *archiveMemberReader) DisplayName() string { return filepath.Base(a.member.Name) }

func (a *archiveMemberReader) GetReader() (io.ReadSeeker, error) {
	if a.seeker == nil {
		rc, err := archivelib.Open(bytes.NewReader(a.data), a.member.Name)
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			return nil, err
		}
		a.seeker = bytes.NewReader(b)
	}
	return a.seeker, nil
}

// browseTieArchive fetches an archive blob and replaces the gallery with its
// image members, mirroring how a directory listing is shown. The blob is read
// once and shared across the member readers.
func browseTieArchive(viewer *gallery.Gallery, host client.FileHost, hash string) {
	viewer.ReadCustomAsync(func() []gallery.CustomReader {
		r, err := getlib.ReadFile(httpClientForHost(host), host.URL, hash)
		if err != nil {
			fmt.Println("Error fetching archive", hash, ":", err)
			return nil
		}
		data, err := io.ReadAll(r)
		if err != nil {
			fmt.Println("Error reading archive", hash, ":", err)
			return nil
		}
		members, err := archivelib.List(bytes.NewReader(data))
		if err != nil {
			fmt.Println("Error listing archive", hash, ":", err)
			return nil
		}
		readers := make([]gallery.CustomReader, 0, len(members))
		for _, m := range members {
			if m.Kind != archivelib.Image {
				continue
			}
			readers = append(readers, &archiveMemberReader{archiveHash: hash, data: data, member: m})
		}
		return readers
	})
	viewer.ChangeGallery()
}

// tieRowKind is how a tag-query row appears in the gallery.
type tieRowKind int

const (
	tieRowSkip    tieRowKind = iota // hidden (audio, plain dirs, ...)
	tieRowFile                      // a viewable image
	tieRowDir                       // a browsable tagged image directory
	tieRowVideo                     // a playable video file
	tieRowArchive                   // a browsable archive blob (zip of images)
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
	for _, tp := range types {
		if client.IsArchiveType(client.StringToTieType(tp)) {
			return tieRowArchive
		}
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
func readFromTie(viewer *gallery.Gallery, tc *client.TieClient, include, exclude []string, filter string, browseDir func(client.DirUID)) {
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
		return buildReaders(viewer, tc, rows, browseDir)
	})
}

// buildReaders turns tag/rating query rows into gallery readers, classifying
// each row into a viewable image, a browsable directory/archive, or a video.
// Rows that classify as tieRowSkip are dropped.
func buildReaders(viewer *gallery.Gallery, tc *client.TieClient, rows []client.Row, browseDir func(client.DirUID)) []gallery.CustomReader {
	host := tieFileHost(tc)
	readers := make([]gallery.CustomReader, 0, len(rows))
	for _, row := range rows {
		switch classifyTieRow(row) {
		case tieRowFile:
			readers = append(readers, &tieReader{
				host:       host,
				hash:       row.Key,
				thumbHash:  client.RowFirst(row, "thumbnail"),
				dimensions: client.RowFirst(row, "dimensions"),
				filename:   client.RowFirst(row, "filename"),
			})
		case tieRowDir:
			uid := client.DirUID(row.Key)
			readers = append(readers, &tieDirReader{uid: uid, tc: tc, host: host, open: func() { browseDir(uid) }})
		case tieRowArchive:
			hash := row.Key
			readers = append(readers, &tieArchiveReader{
				hash:      hash,
				filename:  client.RowFirst(row, "filename"),
				host:      host,
				tc:        tc,
				thumbHash: client.RowFirst(row, "thumbnail"),
				open:      func() { browseTieArchive(viewer, host, hash) },
			})
		case tieRowVideo:
			readers = append(readers, &tieReader{
				host:     host,
				hash:     row.Key,
				filename: client.RowFirst(row, "filename"),
				isVideo:  true,
			})
		}
	}
	return readers
}

// ratingMode selects how the rating filter constrains the gallery.
type ratingMode int

const (
	ratingAny     ratingMode = iota // no rating constraint
	ratingExact                     // exactly n stars
	ratingMin                       // at least n stars
	ratingUnrated                   // no rating triple at all
)

// ratingFilter is a gallery rating constraint. n is the star count for the
// exact/min modes and ignored otherwise.
type ratingFilter struct {
	mode ratingMode
	n    int
}

// matches reports whether a query row satisfies the rating filter, read from
// the row's expanded "rating" attribute.
func (rf ratingFilter) matches(row client.Row) bool {
	switch rf.mode {
	case ratingUnrated:
		return len(client.RowValues(row, "rating")) == 0
	case ratingExact:
		r, _ := strconv.Atoi(client.RowFirst(row, "rating"))
		return r == rf.n
	case ratingMin:
		r, _ := strconv.Atoi(client.RowFirst(row, "rating"))
		return r >= rf.n
	default:
		return true
	}
}

// sortMode selects how gallery rows are ordered client-side.
type sortMode int

const (
	sortDefault    sortMode = iota // engine order (unspecified)
	sortNameAsc                    // filename A→Z
	sortNameDesc                   // filename Z→A
	sortRatingDesc                 // highest rating first
	sortRatingAsc                  // lowest rating first
	sortNewest                     // most recent tag-date first
	sortOldest                     // oldest tag-date first
)

// sortRows orders rows in place per mode. Rating is numeric; name and date are
// string comparisons (tag-date's fixed layout sorts chronologically as text).
func sortRows(rows []client.Row, mode sortMode) {
	rating := func(r client.Row) int { n, _ := strconv.Atoi(client.RowFirst(r, "rating")); return n }
	name := func(r client.Row) string { return client.RowFirst(r, "filename") }
	date := func(r client.Row) string { return client.RowFirst(r, "tag-date") }
	switch mode {
	case sortNameAsc:
		sort.SliceStable(rows, func(i, j int) bool { return name(rows[i]) < name(rows[j]) })
	case sortNameDesc:
		sort.SliceStable(rows, func(i, j int) bool { return name(rows[i]) > name(rows[j]) })
	case sortRatingDesc:
		sort.SliceStable(rows, func(i, j int) bool { return rating(rows[i]) > rating(rows[j]) })
	case sortRatingAsc:
		sort.SliceStable(rows, func(i, j int) bool { return rating(rows[i]) < rating(rows[j]) })
	case sortNewest:
		sort.SliceStable(rows, func(i, j int) bool { return date(rows[i]) > date(rows[j]) })
	case sortOldest:
		sort.SliceStable(rows, func(i, j int) bool { return date(rows[i]) < date(rows[j]) })
	}
}

// galleryFilter is the full set of sidebar constraints driving the gallery.
type galleryFilter struct {
	include, exclude []string     // tag AND / NOT lists
	rating           ratingFilter // star constraint
	untaggedOnly     bool         // show only items with no tag at all
	sort             sortMode     // client-side ordering
}

// queryGallery populates the viewer from tie according to gf. The server seed is
// chosen by precedence — untagged, then tags, then a rating-only browse — and
// uses the MissingRelation predicate for the "untagged" and "unrated" cases so
// only qualifying rows cross the wire. The rating filter and sort are applied
// client-side over the expanded rows. It calls ReadCustomAsync; the caller is
// responsible for viewer.ChangeGallery().
func queryGallery(viewer *gallery.Gallery, tc *client.TieClient, gf galleryFilter, browseDir func(client.DirUID)) {
	spec := client.QuerySpec{Reverse: true, Expand: true, Limit: -1}
	imageScope := client.TieImageFile.String()
	tieType := client.TieTypeProperty.String()
	switch {
	case gf.untaggedOnly:
		spec.Terms = []string{imageScope}
		spec.Filter = tieType
		spec.MissingRelation = "tag"
	case len(gf.include) > 0:
		spec.Terms = gf.include
		spec.Exclude = gf.exclude
		spec.Filter = "tag"
	case gf.rating.mode == ratingUnrated:
		spec.Terms = []string{imageScope}
		spec.Filter = tieType
		spec.MissingRelation = "rating"
	case gf.rating.mode == ratingExact:
		spec.Terms = []string{strconv.Itoa(gf.rating.n)}
		spec.Filter = "rating"
	case gf.rating.mode == ratingMin:
		spec.Terms = []string{imageScope}
		spec.Filter = tieType
	default:
		// Nothing selected (no tags, no rating): leave the gallery unchanged,
		// matching the prior readFromTie behavior for an empty selection.
		return
	}

	rf := gf.rating
	sm := gf.sort
	viewer.ReadCustomAsync(func() []gallery.CustomReader {
		rows, _, err := tc.Query(spec)
		if err != nil {
			fmt.Println("Error happened querying tie:", err)
			return nil
		}
		filtered := rows[:0]
		for _, row := range rows {
			if rf.matches(row) {
				filtered = append(filtered, row)
			}
		}
		sortRows(filtered, sm)
		return buildReaders(viewer, tc, filtered, browseDir)
	})
}

// uploadThumbnail stores jpegBytes on the filehost and records the
// (ownerHash, "thumbnail", thumbHash) mapping, returning the thumbHash.
// Set (not Add) keeps the relation single-valued, so regenerated values
// replace ones whose blobs were reaped from the filehost.
func uploadThumbnail(tc *client.TieClient, host client.FileHost, ownerHash string, jpegBytes []byte) (string, error) {
	if host.URL == "" {
		return "", errors.New("no filehost URL configured")
	}
	pc := putlib.PutConfig{Client: httpClientForHost(host)}
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

// fetchBlob downloads a blob from the filehost.
func fetchBlob(host client.FileHost, hash string) ([]byte, error) {
	r, err := getlib.ReadFile(httpClientForHost(host), host.URL, hash)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
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
	switch info.CustomReader.(type) {
	case *tieDirReader, *tieArchiveReader:
		// Fallback for collections without usable previews (empty directory,
		// fetch failure, ...): a plain folder icon marks the tile as
		// browsable. When the entry has preview images, the gallery badges a
		// content thumbnail via PreviewProvider and never reaches this case.
		info.ThumbnailIsScaled = true
		return folderIcon(t.tileWidth * 2), nil
	}
	// tr is nil for readers that carry image bytes but have no filehost thumbnail
	// cache (e.g. archive members): they fall through to on-the-fly generation
	// without the cache lookup/upload.
	tr, _ := info.CustomReader.(*tieReader)
	if tr != nil {
		if tr.isVideo {
			// Video thumbnails are handled upstream (InputIsVideo → loading placeholder).
			return nil, errors.New("video thumbnail not available")
		}
		if rs, ok := t.thumbnailReader(tr); ok {
			info.ThumbnailIsScaled = true
			return rs, nil
		}
	}

	reader, err := info.GetReader()
	if err != nil {
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
	if tr != nil {
		t.upload(tr, buf.Bytes(), origW, origH)
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
// (tr.hash, "thumbnail", thumbHash) and (tr.hash, "dimensions", "WxH")
// mappings. Failures are logged; the generated thumbnail remains usable,
// just uncached.
func (t *filehostThumbnailer) upload(tr *tieReader, jpegBytes []byte, origW, origH int) {
	thumbHash, err := uploadThumbnail(t.tie, tieFileHost(t.tie), tr.hash, jpegBytes)
	if err != nil {
		fmt.Println("Error uploading thumbnail:", err)
		return
	}
	dims := fmt.Sprintf("%dx%d", origW, origH)
	if err := t.tie.Set(tr.hash, "dimensions", []string{dims}); err != nil {
		fmt.Println("Error saving dimensions:", err)
		return
	}
	tr.thumbHash = thumbHash
	tr.dimensions = dims
}
