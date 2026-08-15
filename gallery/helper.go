package gallery

import (
	"image"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/dsoprea/go-exif/v3"
	"github.com/h2non/filetype"
)

func ScaleImage(img image.Image, w int) (scaledImage image.Image) {
	// err := rez.Convert(scaledImage, img, rez.NewBicubicFilter())
	// if err != nil {
	// 	fmt.Println("Error resizing image:", err)
	// }
	// return scaledImage
	// Linear is dramatically cheaper than Lanczos and visually identical at
	// thumbnail sizes (~600 px), speeding up the thumbnail miss path.
	return imaging.Resize(img, w, 0, imaging.Linear)
}

func Decode(reader io.ReadSeeker) (image.Image, string, error) {
	reader.Seek(0, io.SeekStart)
	img, fmt, err := image.Decode(reader)
	if err != nil {
		return img, fmt, err
	}
	orientation := "1"
	if fmt == "jpeg" {
		// EXIF orientation only exists in practice for JPEGs; skip the
		// whole-file read + full EXIF parse for other formats.
		reader.Seek(0, io.SeekStart)
		orientation = getOrientation(reader)
	}
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
	// Read data into buffer since dsoprea/go-exif needs byte slice
	data, err := io.ReadAll(reader)
	if err != nil {
		return "1"
	}

	// Search for and extract EXIF data
	rawExif, err := exif.SearchAndExtractExif(data)
	if err != nil {
		return "1"
	}

	// Parse EXIF tags into flat list
	entries, _, err := exif.GetFlatExifData(rawExif, nil)
	if err != nil {
		return "1"
	}

	// Find orientation tag (0x0112 in IFD0)
	for _, entry := range entries {
		if entry.TagName == "Orientation" {
			return entry.FormattedFirst
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
		return false
	}
	head := make([]byte, 261)
	if _, err := file.Read(head); err != nil {
		return false
	}

	return filetype.IsArchive(head)
}
