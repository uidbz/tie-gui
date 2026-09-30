package fs

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// member is one test archive entry, in write order.
type member struct{ name, body string }

func writeZip(t *testing.T, p string, members []member) {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, m := range members {
		w, err := zw.Create(m.name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(m.body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func writeTarGz(t *testing.T, p string, members []member) {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		tw.WriteHeader(&tar.Header{Name: m.name, Mode: 0644, Size: int64(len(m.body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(m.body))
	}
	tw.Close()
	gz.Close()
	f.Close()
}

func extractLocal(t *testing.T, reg *Registry, archive, dest string) *Op {
	t.Helper()
	info, err := os.Stat(archive)
	if err != nil {
		t.Fatal(err)
	}
	ops := NewOperations(reg)
	op := ops.Extract(
		Entry{Name: filepath.Base(archive), Path: archive, Size: info.Size()},
		Entry{Name: filepath.Base(dest), Path: dest, IsDir: true}, nil)
	waitDone(t, op)
	return op
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// A multi-entry archive lands in a folder named after it; extracting it again
// picks a fresh " (2)" folder instead of overwriting.
func TestExtractMultiEntryWrapsInFolder(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "photos.zip")
	writeZip(t, archive, []member{{"a.txt", "A"}, {"sub/b.txt", "B"}})
	reg := NewRegistry(NewLocalFS(), nil)

	op := extractLocal(t, reg, archive, dir)
	if got := readFile(t, filepath.Join(dir, "photos", "a.txt")); got != "A" {
		t.Errorf("a.txt = %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "photos", "sub", "b.txt")); got != "B" {
		t.Errorf("sub/b.txt = %q", got)
	}
	if op.TotalSize != 2 || op.TotalBytesRead != 2 {
		t.Errorf("progress = %d/%d, want 2/2", op.TotalBytesRead, op.TotalSize)
	}

	extractLocal(t, reg, archive, dir)
	if got := readFile(t, filepath.Join(dir, "photos (2)", "a.txt")); got != "A" {
		t.Errorf("second extraction a.txt = %q", got)
	}
}

// An archive holding a single top-level folder extracts as-is (no extra
// wrapping level), also for compressed tarballs.
func TestExtractSingleTopFolderAsIs(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "release.tar.gz")
	writeTarGz(t, archive, []member{{"album/01.flac", "one"}, {"album/cd2/02.flac", "two"}})
	extractLocal(t, NewRegistry(NewLocalFS(), nil), archive, dir)

	if got := readFile(t, filepath.Join(dir, "album", "cd2", "02.flac")); got != "two" {
		t.Errorf("album/cd2/02.flac = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "release")); !os.IsNotExist(err) {
		t.Errorf("single-folder archive was wrapped (err=%v)", err)
	}
}

// Members escaping the extraction root are dropped, never written.
func TestExtractSkipsUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "dest")
	os.Mkdir(dest, 0755)
	archive := filepath.Join(dest, "bad.zip")
	writeZip(t, archive, []member{{"../evil.txt", "x"}, {"ok.txt", "ok"}})
	extractLocal(t, NewRegistry(NewLocalFS(), nil), archive, dest)

	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); !os.IsNotExist(err) {
		t.Fatalf("zip-slip member was written outside the destination")
	}
	if got := readFile(t, filepath.Join(dest, "ok.txt")); got != "ok" {
		t.Errorf("ok.txt = %q", got)
	}
}

// A remote destination receives each member through its Importer at the
// planned subpath.
func TestExtractIntoRemoteImports(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "set.zip")
	writeZip(t, archive, []member{{"x.txt", "X"}, {"sub/y.txt", "Y"}})
	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(NewLocalFS(), fake))
	op := ops.Extract(Entry{Name: "set.zip", Path: archive},
		Entry{Name: "music", Path: "tie:/music", IsDir: true}, nil)
	waitDone(t, op)

	want := []string{"tie:/music/set|x.txt", "tie:/music/set/sub|y.txt"}
	if !slices.Equal(fake.imports, want) {
		t.Errorf("imports = %v, want %v", fake.imports, want)
	}
}

func TestIsArchiveName(t *testing.T) {
	for name, want := range map[string]bool{
		"a.zip": true, "A.ZIP": true, "b.tar.gz": true, "c.tgz": true, "d.7z": true,
		"e.rar": true, "comic.cbz": true, "f.tar": true,
		"g.gz": false, "h.txt": false, "zip": false, ".zip": false, "dir": false,
	} {
		if got := IsArchiveName(name); got != want {
			t.Errorf("IsArchiveName(%q) = %v, want %v", name, got, want)
		}
	}
	if got := archiveStem("music.tar.gz"); got != "music" {
		t.Errorf("archiveStem = %q", got)
	}
}

func TestUniqueName(t *testing.T) {
	existing := map[string]bool{"a": true, "a (2)": true, "b.txt": true, ".cfg": true}
	for in, want := range map[string]string{
		"a": "a (3)", "b.txt": "b (2).txt", "new": "new", ".cfg": ".cfg (2)",
	} {
		if got := uniqueName(in, existing); got != want {
			t.Errorf("uniqueName(%q) = %q, want %q", in, got, want)
		}
	}
}
