package fs

import (
	"os"
	"path/filepath"
	"strings"
)

// LocalFS serves the local disk (bare paths and "file:" URIs).
type LocalFS struct{}

func NewLocalFS() *LocalFS { return &LocalFS{} }

func (l *LocalFS) Scheme() string { return "file" }

// osPath strips an optional "file:" scheme prefix to a real filesystem path.
func osPath(path string) string {
	return strings.TrimPrefix(path, "file:")
}

func (l *LocalFS) List(path string) ([]Entry, error) {
	dir := osPath(path)
	dirents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(dirents))
	for _, d := range dirents {
		if strings.HasPrefix(d.Name(), ".") {
			continue
		}
		info, err := d.Info()
		if err != nil {
			continue
		}
		entries = append(entries, Entry{
			Name:    d.Name(),
			Path:    filepath.Join(dir, d.Name()),
			IsDir:   info.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	return entries, nil
}

func (l *LocalFS) Materialize(e Entry) (string, error) {
	return osPath(e.Path), nil
}

// Mkdir creates a new directory named name inside parent. Implements DirMaker.
func (l *LocalFS) Mkdir(parent, name string) error {
	return os.Mkdir(filepath.Join(osPath(parent), name), 0755)
}

// Stat returns an Entry for a single local path.
func (l *LocalFS) Stat(path string) (Entry, error) {
	p := osPath(path)
	info, err := os.Stat(p)
	if err != nil {
		return Entry{}, err
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		Name:    info.Name(),
		Path:    abs,
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}, nil
}
