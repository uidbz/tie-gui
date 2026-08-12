package gallery

import (
	"fmt"
	"image"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/h2non/filetype"
	"github.com/rwcarlsen/goexif/exif"
)

func ScaleImage(img image.Image, w int) (scaledImage image.Image) {
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

// IsImage reports whether the reader contains an image, detected by reading
// the file header.
func IsImage(file io.Reader) bool {
	head := make([]byte, 261)
	if _, err := file.Read(head); err != nil {
		return false
	}

	return filetype.IsImage(head)
}

// IsImageFromPath reports whether the file at path is an image, detected by
// reading the file header.
func IsImageFromPath(path string) bool {
	file, err := os.Open(path)
	defer file.Close()
	if err != nil {
		fmt.Println("Error opening:", path)
		return false
	}
	return IsImage(file)
}

// IsVideoFromPath reports whether the file at path is a common video format,
// detected by file extension.
func IsVideoFromPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".mkv", ".avi", ".webm", ".mov", ".flv", ".wmv", ".m4v",
		".ogv", ".ts", ".mpg", ".mpeg", ".3gp", ".3g2":
		return true
	}
	return false
}

// IsArchiveFromPath reports whether the file at path is an archive, detected
// by reading the file header.
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
