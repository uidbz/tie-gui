package gallery

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/dsoprea/go-exif/v3"
)

// infoExifTags lists the EXIF tags shown in the info overlay, in display
// order. Anything outside this curated list is skipped to keep the panel
// readable.
var infoExifTags = []string{
	"Make", "Model", "LensModel", "Software",
	"DateTimeOriginal", "DateTime",
	"ExposureTime", "FNumber", "ISOSpeedRatings", "FocalLength",
	"FocalLengthIn35mmFilm", "ExposureProgram", "ExposureBiasValue",
	"MeteringMode", "Flash", "WhiteBalance",
	"Orientation", "XResolution", "YResolution",
	"Artist", "Copyright",
}

// infoRow is a single label/value line in the info overlay.
type infoRow struct {
	label string
	value string
}

// imageMetadata is the collected overlay content: basic file information and
// a curated EXIF section (empty when the image carries no EXIF block).
type imageMetadata struct {
	file []infoRow
	exif []infoRow
}

// infoOverlay is a modal metadata panel laid over the single-image view: a
// dimmed scrim over the image with a centered, scrollable panel showing the
// filename, path, dimensions, byte size, and EXIF camera data. Toggled with
// the I hotkey or the ☰ menu's "Image info" item; tapping the scrim closes it.
type infoOverlay struct {
	widget.BaseWidget
	viewer  *Gallery
	scrim   *canvas.Rectangle
	panelBg *canvas.Rectangle
	title   *widget.Label
	text    *widget.RichText
	panel   *fyne.Container
	info    *ImageInfo
	// panelPos/panelSize track the laid-out panel bounds so Tapped can tell
	// scrim taps (close) from panel taps (ignored).
	panelPos  fyne.Position
	panelSize fyne.Size
}

// scrimColor is the dimming tint behind the panel (theme-independent).
var scrimColor = color.NRGBA{R: 0, G: 0, B: 0, A: 140}

func newInfoOverlay(viewer *Gallery) *infoOverlay {
	o := &infoOverlay{viewer: viewer}
	o.scrim = canvas.NewRectangle(scrimColor)
	o.panelBg = canvas.NewRectangle(theme.Color(theme.ColorNameOverlayBackground))
	o.panelBg.CornerRadius = 8
	o.title = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	o.title.Truncation = fyne.TextTruncateEllipsis
	o.text = widget.NewRichText()
	o.text.Wrapping = fyne.TextWrapWord
	closeBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		o.viewer.hideInfoOverlay()
	})
	header := container.NewBorder(nil, nil, nil, closeBtn, o.title)
	o.panel = container.NewBorder(header, nil, nil, nil, container.NewScroll(o.text))
	o.ExtendBaseWidget(o)
	return o
}

// setInfo points the overlay at a new image: the immediately known rows are
// rendered at once, then the byte-level metadata (size, format, EXIF) is
// loaded off the UI goroutine and swapped in when ready.
func (o *infoOverlay) setInfo(info *ImageInfo) {
	o.info = info
	name := info.DisplayName
	if name == "" {
		name = filepath.Base(info.Path)
	}
	o.title.SetText(name)
	o.text.Segments = infoSegments(basicMetadata(info))
	o.text.Refresh()
	if info.InputIsVideo || info.InputIsDir {
		return // no byte-level metadata for videos or collections
	}
	load := o.viewer.infoMetadataFn
	if load == nil {
		load = asyncInfoMetadata
	}
	load(info, func(md imageMetadata) {
		// The image may have changed (or the overlay closed) while loading.
		if o.info != info || !o.Visible() {
			return
		}
		o.text.Segments = infoSegments(md)
		o.text.Refresh()
	})
}

// asyncInfoMetadata is the production infoMetadataFn: the metadata load runs
// on a background goroutine (file read + EXIF parse) and the result is
// applied on the UI goroutine.
func asyncInfoMetadata(info *ImageInfo, apply func(imageMetadata)) {
	go func() {
		md := collectImageMetadata(info)
		fyne.Do(func() { apply(md) })
	}()
}

// Tapped closes the overlay when the tap lands on the scrim outside the
// panel; taps inside the panel are ignored (text selection, scrolling).
func (o *infoOverlay) Tapped(ev *fyne.PointEvent) {
	p := ev.Position
	if p.X < o.panelPos.X || p.Y < o.panelPos.Y ||
		p.X > o.panelPos.X+o.panelSize.Width || p.Y > o.panelPos.Y+o.panelSize.Height {
		o.viewer.hideInfoOverlay()
	}
}

// basicMetadata returns the rows known without reading the image bytes.
func basicMetadata(info *ImageInfo) imageMetadata {
	name := info.DisplayName
	if name == "" {
		name = filepath.Base(info.Path)
	}
	path := info.Path
	if info.FullPath != "" {
		path = info.FullPath
	}
	kind := "Image"
	switch {
	case info.InputIsVideo:
		kind = "Video"
	case info.InputIsDir && !info.InputIsArchive:
		kind = "Directory"
	case info.InputIsArchive:
		kind = "Archive"
	}
	rows := []infoRow{
		{"File", name},
		{"Path", path},
		{"Type", kind},
	}
	if info.InputIsArchive && info.archiveName != "" {
		rows = append(rows, infoRow{"Archive", info.archiveName})
	}
	if info.Width > 0 && info.Height > 0 {
		rows = append(rows, infoRow{"Dimensions",
			fmt.Sprintf("%d × %d px", info.Width, info.Height)})
	}
	return imageMetadata{file: rows}
}

// collectImageMetadata reads the image content off the UI goroutine and
// returns the full overlay metadata: byte size, format, decoded dimensions,
// and the curated EXIF section. Must not touch widgets.
func collectImageMetadata(info *ImageInfo) imageMetadata {
	md := basicMetadata(info)
	r, err := info.GetReader()
	if err != nil {
		return md
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return md
	}
	md.file = append(md.file, infoRow{"Size", humanBytes(int64(len(data)))})
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err == nil {
		if format != "" {
			md.file = append(md.file, infoRow{"Format", format})
		}
		if cfg.Width > 0 && cfg.Height > 0 && (info.Width == 0 || info.Height == 0) {
			md.file = append(md.file, infoRow{"Dimensions",
				fmt.Sprintf("%d × %d px", cfg.Width, cfg.Height)})
		}
	}
	md.exif = extractExifRows(data)
	return md
}

// extractExifRows parses the EXIF block (JPEG/TIFF) and returns the curated
// whitelist tags in display order. Returns nil when no EXIF data is present.
func extractExifRows(data []byte) []infoRow {
	rawExif, err := exif.SearchAndExtractExif(data)
	if err != nil {
		return nil
	}
	entries, _, err := exif.GetFlatExifData(rawExif, nil)
	if err != nil {
		return nil
	}
	byName := make(map[string]string, len(entries))
	for _, e := range entries {
		if _, ok := byName[e.TagName]; !ok && e.FormattedFirst != "" {
			byName[e.TagName] = e.FormattedFirst
		}
	}
	var rows []infoRow
	for _, tag := range infoExifTags {
		if v, ok := byName[tag]; ok {
			rows = append(rows, infoRow{tag, v})
		}
	}
	return rows
}

// infoSegments renders the metadata rows as rich text: bold labels followed
// by their values, with the EXIF rows under a sub-heading.
func infoSegments(md imageMetadata) []widget.RichTextSegment {
	var segs []widget.RichTextSegment
	appendRows := func(rows []infoRow) {
		for _, r := range rows {
			segs = append(segs,
				&widget.TextSegment{Text: r.label + ": ", Style: widget.RichTextStyleStrong},
				&widget.TextSegment{Text: r.value + "\n", Style: widget.RichTextStyleInline},
			)
		}
	}
	appendRows(md.file)
	if len(md.exif) > 0 {
		segs = append(segs, &widget.TextSegment{Text: "\nEXIF\n", Style: widget.RichTextStyleSubHeading})
		appendRows(md.exif)
	}
	return segs
}

// humanBytes formats a byte count as B/KB/MB/GB.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// ═══════════════════════════════════════════════════════════════════════════
// Gallery integration
// ═══════════════════════════════════════════════════════════════════════════

// imageViewActive reports whether the single-image view is currently on
// screen (as opposed to the gallery grid or a video).
func (viewer *Gallery) imageViewActive() bool {
	return len(viewer.Content.Objects) > 0 && viewer.Content.Objects[0] == viewer.CurrentImage
}

// ImageViewActive reports whether the single-image view is currently on
// screen. Apps use it to decide whether an image-view overlay they manage
// (e.g. tie-view's quick tag bar) should be added to Content right away or
// only on the next OnImageChange.
func (viewer *Gallery) ImageViewActive() bool { return viewer.imageViewActive() }

// ToggleInfoOverlay shows the metadata overlay for the currently displayed
// image, or hides it when already open. Bound to the I key by default and
// reachable from the ☰ menu's "Image info" item.
func (viewer *Gallery) ToggleInfoOverlay() {
	if viewer.infoOverlay != nil && viewer.infoOverlay.Visible() {
		viewer.hideInfoOverlay()
		return
	}
	viewer.showInfoOverlay()
}

// showInfoOverlay opens (or retargets) the metadata overlay. No-op unless the
// single-image view is on screen.
func (viewer *Gallery) showInfoOverlay() {
	if !viewer.imageViewActive() || viewer.CurrentImageView == nil || viewer.CurrentImageView.info == nil {
		return
	}
	if viewer.infoOverlay == nil {
		viewer.infoOverlay = newInfoOverlay(viewer)
	}
	viewer.infoOverlay.setInfo(viewer.CurrentImageView.info)
	present := false
	for _, obj := range viewer.Content.Objects {
		if obj == viewer.infoOverlay {
			present = true
			break
		}
	}
	if !present {
		viewer.Content.Objects = append(viewer.Content.Objects, viewer.infoOverlay)
	}
	viewer.infoOverlay.Show()
	viewer.Content.Refresh()
}

// hideInfoOverlay closes the metadata overlay and returns keyboard focus to
// the image view on desktop so hotkeys keep working.
func (viewer *Gallery) hideInfoOverlay() {
	if viewer.infoOverlay == nil {
		return
	}
	viewer.infoOverlay.Hide()
	viewer.Content.Refresh()
	if viewer.platform.ShouldFocusImageView() && viewer.imageViewActive() && viewer.CurrentImageView != nil {
		viewer.window.Canvas().Focus(viewer.CurrentImageView)
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Renderer
// ═══════════════════════════════════════════════════════════════════════════

type infoOverlayRenderer struct {
	overlay *infoOverlay
	objects []fyne.CanvasObject
}

func (o *infoOverlay) CreateRenderer() fyne.WidgetRenderer {
	return &infoOverlayRenderer{
		overlay: o,
		objects: []fyne.CanvasObject{o.scrim, o.panelBg, o.panel},
	}
}

func (r *infoOverlayRenderer) Layout(size fyne.Size) {
	o := r.overlay
	o.scrim.Resize(size)
	o.scrim.Move(fyne.NewPos(0, 0))

	w := size.Width * 0.85
	if max := float32(640); w > max {
		w = max
	}
	h := size.Height * 0.8
	panelSize := fyne.NewSize(w, h)
	panelPos := fyne.NewPos((size.Width-w)/2, (size.Height-h)/2)
	o.panel.Resize(panelSize)
	o.panel.Move(panelPos)
	o.panelBg.Resize(panelSize)
	o.panelBg.Move(panelPos)
	o.panelPos, o.panelSize = panelPos, panelSize
}

func (r *infoOverlayRenderer) MinSize() fyne.Size {
	return fyne.NewSize(100, 100)
}

func (r *infoOverlayRenderer) Refresh() {
	o := r.overlay
	o.panelBg.FillColor = theme.Color(theme.ColorNameOverlayBackground)
	o.panelBg.Refresh()
	o.panel.Refresh()
}

func (r *infoOverlayRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *infoOverlayRenderer) Destroy() {}
