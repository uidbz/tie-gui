package imgviewer

import (
	// "fmt"
	"image"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/disintegration/imaging"
	// "github.com/nfnt/resize"
	// "github.com/bamiaux/rez"
	"github.com/rwcarlsen/goexif/exif"
)

func scaleImage(img image.Image, w int) (scaledImage image.Image) {
	// err := rez.Convert(scaledImage, img, rez.NewBicubicFilter())
	// if err != nil {
	// 	fmt.Println("Error resizing image:", err)
	// }
	// return scaledImage
	return imaging.Resize(img, w, 0, imaging.Lanczos)
}

func Decode(reader io.ReadSeeker) (image.Image, string, error) {
	reader.Seek(0, io.SeekStart)
	img, fmt, err := image.Decode(reader)
	if err != nil {
		return img, fmt, err
	}
	reader.Seek(0, io.SeekStart)
	orientation := getOrientation(reader)
	switch orientation {
	case "1":
	case "2":
		img = imaging.FlipH(img)
	case "3":
		img = imaging.Rotate180(img)
	case "4":
		img = imaging.Rotate180(imaging.FlipH(img))
	case "5":
		img = imaging.Rotate270(imaging.FlipV(img))
	case "6":
		img = imaging.Rotate270(img)
	case "7":
		img = imaging.Rotate90(imaging.FlipV(img))
	case "8":
		img = imaging.Rotate90(img)
	}

	return img, fmt, err
}

func getOrientation(reader io.Reader) string {
	x, err := exif.Decode(reader)
	if err != nil {
		return "1"
	}
	if x != nil {
		orient, err := x.Get(exif.Orientation)
		if err != nil {
			return "1"
		}
		if orient != nil {
			return orient.String()
		}
	}

	return "1"
}

func ReadDir(imgPath string) (string, []fs.DirEntry, int) {
	path := filepath.Dir(imgPath)
	dirContent, _ := os.ReadDir(path)
	for i, x := range dirContent {
		if x.Name() == filepath.Base(imgPath) {
			return path, dirContent, i
		}
	}

	return path, dirContent, 0
}
