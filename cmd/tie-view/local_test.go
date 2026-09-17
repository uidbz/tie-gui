package main

import (
	"archive/zip"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestImage(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range img.Pix {
		img.Pix[i] = 0x7f
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTestZip(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("fake")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// classifyLocalInput must leave tie subjects (tie: URLs, bare hashes,
// nonexistent paths) to the tie resolution and classify existing local
// paths the way imgview's ParseInput does.
func TestClassifyLocalInput(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "photo.jpg")
	writeTestImage(t, imgPath)
	zipPath := filepath.Join(tmp, "pack.zip")
	writeTestZip(t, zipPath)
	txtPath := filepath.Join(tmp, "notes.txt")
	if err := os.WriteFile(txtPath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	// A relative path must classify the same as its absolute form.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	cases := []struct {
		name string
		arg  string
		want localInput
	}{
		{"empty", "", localNone},
		{"tie hash URL", "tie:" + strings.Repeat("a", 64), localNone},
		{"tie path URL", "tie:/photos/2024", localNone},
		{"bare hash", strings.Repeat("a", 64), localNone},
		{"nonexistent", filepath.Join(tmp, "no-such-place"), localNone},
		{"directory", tmp, localDir},
		{"directory relative", ".", localDir},
		{"image", imgPath, localImage},
		{"archive", zipPath, localArchive},
		{"unsupported", txtPath, localUnsupported},
	}
	for _, c := range cases {
		_, got := classifyLocalInput(c.arg)
		if got != c.want {
			t.Errorf("%s: classifyLocalInput(%q) = %v, want %v", c.name, c.arg, got, c.want)
		}
	}
}
