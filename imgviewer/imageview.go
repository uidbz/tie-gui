package imgviewer

import (
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"

	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
	"github.com/h2non/filetype"

	"github.com/disintegration/imaging"
)

type ImageView struct {
	widget.BaseWidget

	fyneImage         *canvas.Image
	raster            *canvas.Raster
	format            string
	imgWidth          int
	imgHeight         int
	zoom              float32
	imgScale          float32
	region            *Region
	startPos          fyne.Position
	pos               fyne.Position
	size              fyne.Size
	lastContainerSize fyne.Size
	refreshBilinear   *time.Timer
	newSize           bool
	fillWindow        bool
	info              *ImageInfo
	dragStart         bool
	focus             func(fyne.Focusable)
	hotkeys           []Hotkey
	w                 fyne.Window
	container         *fyne.Container
	changeFn          func()
}

type Hotkey struct {
	Name    fyne.KeyName
	Functon func()
}

type ImageLayout struct{}

func (d *ImageLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(100, 100)
}
func (d *ImageLayout) Layout(objects []fyne.CanvasObject, containerSize fyne.Size) {
	for _, o := range objects {
		iv := o.(*ImageView)
		if iv.fillWindow {
			iv.pos = fyne.NewPos(0, 0)
			iv.size = containerSize
			iv.newSize = true
		} else {
			if iv.lastContainerSize.Height == 0 {
				iv.lastContainerSize = containerSize
			}
			if iv.lastContainerSize.Height != containerSize.Height || iv.lastContainerSize.Width != containerSize.Width {
				xCenter := (containerSize.Width - iv.lastContainerSize.Width) / 2
				yCenter := (containerSize.Height - iv.lastContainerSize.Height) / 2
				newX := iv.pos.X + xCenter
				newY := iv.pos.Y + yCenter
				iv.lastContainerSize = containerSize
				iv.pos = fyne.NewPos(newX, newY)
			}
		}
		if o.(*ImageView).newSize {
			oldSize := o.Size()
			newSize := iv.size
			o.Resize(newSize)
			iv.newSize = false
			if iv.dragStart { // If dragging while zooming
				delta := newSize.Subtract(oldSize)
				iv.startPos = iv.startPos.Add(fyne.NewPos(delta.Width/2, delta.Height/2))
			}
			iv.changeFn()
		}
		o.Move(iv.pos)
	}
}

func (iv *ImageView) GetImageInfo() string {
	return filepath.Base(iv.info.Path) + " (" + strconv.Itoa(iv.imgWidth) + "x" + strconv.Itoa(iv.imgHeight) + ")" + " [" + strconv.Itoa(iv.GetZoomLevel()) + "%]"
}

func (iv *ImageView) GetZoomLevel() int {
	zoomLevel := iv.Size().Width / float32(iv.imgWidth) * 100
	return int(zoomLevel)
}

func (iv *ImageView) RotateLeft() {
	iv.fyneImage.Image = imaging.Rotate90(iv.fyneImage.Image)
	iv.fyneImage.Refresh()
}

func (iv *ImageView) RotateRight() {
	iv.fyneImage.Image = imaging.Rotate270(iv.fyneImage.Image)
	iv.fyneImage.Refresh()
}

func (iv *ImageView) OriginalSize() {
	iv.fillWindow = false
	iv.size = fyne.NewSize(float32(iv.imgWidth), float32(iv.imgHeight))
	w, h := iv.container.Size().Subtract(iv.size).Components()
	iv.pos = fyne.NewPos(w/2, h/2)
	iv.newSize = true
	iv.container.Refresh()
	iv.changeFn()
}

func NewImageView(info *ImageInfo, size fyne.Size, hideRegion bool, w fyne.Window, focusFunc func(fyne.Focusable)) *ImageView {
	iv := &ImageView{
		focus:           focusFunc,
		zoom:            2,
		info:            info,
		w:               w,
		size:            size,
		newSize:         true,
		refreshBilinear: time.NewTimer(100 * time.Millisecond),
	}
	if err := iv.LoadImage(); err != nil {
		fmt.Println("Error:", err)
	}
	iv.refreshBilinear.Stop()
	// go func() {
	// 	for {
	// 		<-iv.refreshBilinear.C
	// 		iv.fyneImage.ScaleMode = canvas.ImageScaleSmooth
	// 		iv.fyneImage.Refresh()
	// 	}
	// }()

	if !hideRegion {
		iv.region = NewRegion(color.Black, iv)
	}
	iv.ExtendBaseWidget(iv)

	return iv
}

func (img *ImageInfo) GetReader() (io.ReadSeeker, error) {
	if img.InputIsReader {
		r, err := img.CustomReader.GetReader()
		if err != nil {
			return nil, err
		}
		if _, err := r.Seek(0, io.SeekStart); err != nil {
			return nil, err
		} else {
			return r, nil
		}
	}
	if img.InputIsArchive {
		if img.archiveFile == nil {
			return nil, errors.New("Could not read zip file")
		}
		imgReader, err := img.archiveFile.Open(img.Path)
		if err != nil {
			return nil, err
		}
		if file, err := io.ReadAll(imgReader); err != nil {
			return nil, err
		} else {
			return bytes.NewReader(file), nil
		}
	} else {
		imgReader, err := os.Open(img.Path)
		if err != nil {
			return nil, err
		}
		return imgReader, nil
	}
}

func (iv *ImageView) LoadImage() error {
	reader, err := iv.info.GetReader()
	if err != nil {
		return err
	}
	img, format, err2 := Decode(reader)
	if err2 != nil {
		return err2
	}
	iv.format = format
	iv.fyneImage = canvas.NewImageFromImage(img)
	iv.fyneImage.ScaleMode = canvas.ImageScaleFastest
	iv.fyneImage.FillMode = canvas.ImageFillContain
	iv.imgWidth = img.Bounds().Max.X
	iv.imgHeight = img.Bounds().Max.Y

	return nil
}

func (iv *ImageView) Resize(size fyne.Size) {
	iv.BaseWidget.Resize(size)
}

func (iv *ImageView) Dragged(drag *fyne.DragEvent) {
	if !iv.info.IsDraggable {
		return
	}
	if !iv.dragStart {
		iv.newSize = false
		iv.startPos = drag.PointEvent.Position
		iv.dragStart = true
		iv.fillWindow = false
	}
	// iv.fyneImage.ScaleMode = canvas.ImageScaleFastest
	iv.pos = drag.AbsolutePosition.Subtract(iv.startPos)

	iv.container.Refresh()
}

func (iv *ImageView) DragEnd() {
	iv.dragStart = false
	// iv.fyneImage.ScaleMode = canvas.ImageScaleSmooth
	// iv.fyneImage.Refresh()
}

func (iv *ImageView) TypedKey(key *fyne.KeyEvent) {
	for _, x := range iv.hotkeys {
		if key.Name == x.Name {
			x.Functon()
		}
	}
}
func (iv *ImageView) Tapped(_ *fyne.PointEvent) {
	if iv.info.OnTapped != nil {
		iv.info.OnTapped()
	}
}

func (iv *ImageView) TappedSecondary(_ *fyne.PointEvent) {
	if iv.info.OnTappedSecondary != nil {
		iv.info.OnTappedSecondary()
	}
}

func (iv *ImageView) DoubleTapped(_ *fyne.PointEvent) {
	if iv.info.OnDoubleTapped != nil {
		iv.info.OnDoubleTapped()
	}
}

func (e *ImageView) RegisterKey(name fyne.KeyName, function func()) {
	e.hotkeys = append(e.hotkeys, Hotkey{name, function})
}

func (iv *ImageView) Scrolled(ev *fyne.ScrollEvent) {
	if !iv.info.IsZoomable {
		fmt.Println("Not zoomable")
		return
	}
	iv.fillWindow = false
	// iv.fyneImage.ScaleMode = canvas.ImageScaleFastest
	var zoom float32
	if ev.Scrolled.DY > 0 {
		zoom = 1 + (iv.zoom / 10)
	} else {
		zoom = 1 - (iv.zoom / 10)
	}

	newWidth := iv.Size().Width * zoom
	newHeight := iv.Size().Height * zoom
	newSize := fyne.NewSize(newWidth, newHeight)

	factorX := (newWidth / iv.Size().Width)
	factorY := (newHeight / iv.Size().Height)
	x := ev.AbsolutePosition.X - (ev.Position.X * factorX)
	y := ev.AbsolutePosition.Y - (ev.Position.Y * factorY)
	// fmt.Println("delta", ev.Position.X, ev.Position.Y, x, y)
	newPos := fyne.NewPos(x, y)
	iv.pos = newPos
	iv.size = newSize
	iv.newSize = true

	iv.changeFn()
	iv.container.Refresh()
	// iv.refreshBilinear.Reset(100 * time.Millisecond)
}

func IsImage(file io.Reader) bool {
	head := make([]byte, 261)
	if _, err := file.Read(head); err != nil {
		return false
	}

	return filetype.IsImage(head)
}

func IsImageFromPath(path string) bool {
	file, err := os.Open(path)
	defer file.Close()
	if err != nil {
		fmt.Println("Error opening:", path)
		return false
	}
	return IsImage(file)
}

func IsArchiveFromPath(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		fmt.Println("Error opening:", path)
		return false
	}
	head := make([]byte, 261)
	if _, err := file.Read(head); err != nil {
		return false
	}

	return filetype.IsArchive(head)
}

func (iv *ImageView) FocusGained() {
}

func (iv *ImageView) FocusLost() {
}

func (iv *ImageView) TypedRune(r rune) {
}

/*****************************
** RENDERER
*****************************/
type ImageViewRenderer struct {
	imageView *ImageView
	objects   []fyne.CanvasObject
}

func (r *ImageView) CreateRenderer() fyne.WidgetRenderer {
	ren := &ImageViewRenderer{
		imageView: r,
		objects:   []fyne.CanvasObject{r.fyneImage},
	}

	return ren
}

// Refresh causes this object to be redrawn in it's current state
func (r *ImageViewRenderer) Destroy() {
}

func (ren *ImageViewRenderer) Refresh() {}

func (ren *ImageViewRenderer) Objects() []fyne.CanvasObject {
	if ren.imageView.region == nil {
		return ren.objects
	} else {
		return []fyne.CanvasObject{ren.imageView.fyneImage, ren.imageView.region}
	}
}

func (ren *ImageViewRenderer) MinSize() fyne.Size {
	return fyne.NewSize(0, 0)
}

func (ren *ImageViewRenderer) Layout(s fyne.Size) {
	ren.imageView.fyneImage.Resize(s)
}
