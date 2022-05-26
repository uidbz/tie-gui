package main

import (
	"fmt"
	"image"
	"image/color"
	"io/fs"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"fyne.io/fyne/v2/widget"
)

type ImageView struct {
	widget.BaseWidget

	origImage         image.Image
	fyneImage         *canvas.Image
	raster            *canvas.Raster
	format            string
	imgWidth          int
	imgHeight         int
	zoom              float32
	zoomable          bool
	dragable          bool
	imgScale          float32
	region            *Region
	startPos          fyne.Position
	pos               fyne.Position
	size              fyne.Size
	lastContainerSize fyne.Size
	newSize           bool
	fillWindow        bool
	OnClicked         func()
	path              string
	dragStart         bool
	focus             func(fyne.Focusable)
	hotkeys           []Hotkey
	w                 fyne.Window
	container         *fyne.Container
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
				newX := iv.pos.X + (containerSize.Width-iv.lastContainerSize.Width)/2
				newY := iv.pos.Y + (containerSize.Height-iv.lastContainerSize.Height)/2
				iv.lastContainerSize = containerSize
				iv.pos = fyne.NewPos(newX, newY)
			}
		}
		if o.(*ImageView).newSize {
			newSize := iv.size
			o.Resize(newSize)
			iv.newSize = false
		}
		o.Move(o.(*ImageView).pos)
	}
}

func NewImageView(path string, size fyne.Size, hideRegion bool, zoomable bool, dragable bool, w fyne.Window, focusFunc func(fyne.Focusable)) *ImageView {
	iv := &ImageView{
		focus:    focusFunc,
		zoom:     2,
		zoomable: zoomable,
		dragable: dragable,
		path:     path,
		w:        w,
		size:     size,
		newSize:  true,
	}
	if err := iv.LoadImage(); err != nil {
		fmt.Println("Error:", err)
	}
	iv.path = path

	if !hideRegion {
		iv.region = NewRegion(color.Black, iv)
	}
	iv.ExtendBaseWidget(iv)

	return iv
}

func (iv *ImageView) LoadImage() error {

	imgReader, err := os.Open(iv.path)
	if err != nil {
		return err
	}
	img, format, _ := Decode(imgReader)
	iv.origImage = img
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
	if !iv.dragable {
		return
	}
	if !iv.dragStart {
		iv.startPos = drag.PointEvent.Position
		iv.dragStart = true
		iv.fillWindow = false
	}
	iv.pos = drag.AbsolutePosition.Subtract(iv.startPos)

	iv.container.Refresh()
}

func (iv *ImageView) DragEnd() {
	iv.dragStart = false
}

func (iv *ImageView) TypedKey(key *fyne.KeyEvent) {
	fmt.Println("KeyEvent", key)
	for _, x := range iv.hotkeys {
		if key.Name == x.Name {
			x.Functon()
		}
	}
}
func (iv *ImageView) Tapped(_ *fyne.PointEvent) {
	if iv.OnClicked != nil {
		iv.OnClicked()
	}
}

func (e *ImageView) RegisterKey(name fyne.KeyName, function func()) {
	e.hotkeys = append(e.hotkeys, Hotkey{name, function})
}

func (iv *ImageView) Scrolled(ev *fyne.ScrollEvent) {
	if !iv.zoomable {
		fmt.Println("Not zoomable")
		return
	}
	iv.fillWindow = false
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

	iv.container.Refresh()
}

func IsImage(entry fs.DirEntry) bool {
	if entry.IsDir() {
		return false
	}

	return IsImageFromPath(entry.Name())

}

func IsImageFromPath(path string) bool {
	switch filepath.Ext(path) {
	case ".jpg":
		return true
	case ".png":
		return true
	case ".gif":
		return true
	default:
		return false
	}
}

func (iv *ImageView) FocusGained() {
	fmt.Println("Focus gained")
}
func (iv *ImageView) FocusLost() {
	fmt.Println("Focus lost")
}

func (iv *ImageView) TypedRune(r rune) {
	fmt.Println("rune", r)
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
	return fyne.NewSize(800, 800)
}

func (ren *ImageViewRenderer) Layout(s fyne.Size) {
	ren.imageView.fyneImage.Resize(s)
}
