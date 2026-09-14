package fs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uidbz/tie/client"
)

func waitDone(t *testing.T, op *Op) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for op.Status != StatusCompleted && op.Status != StatusError {
		if time.Now().After(deadline) {
			t.Fatal("operation did not finish in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if op.Status == StatusError {
		t.Fatalf("operation failed: %v", op.Err)
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(srcPath, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	destDir := filepath.Join(dir, "dest")
	if err := os.Mkdir(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	ops := NewOperations(nil)
	src, _ := statEntry(srcPath)
	dst, _ := statEntry(destDir)
	op := ops.Copy(src, dst, nil)
	waitDone(t, op)

	got, err := os.ReadFile(filepath.Join(destDir, "src.txt"))
	if err != nil {
		t.Fatalf("copied file missing: %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("content mismatch: %q", got)
	}
	// original must still exist after a copy
	if _, err := os.Stat(srcPath); err != nil {
		t.Fatalf("source removed by copy: %v", err)
	}
	if pct := op.PctComplete(); pct != 1 {
		t.Fatalf("PctComplete = %v, want 1", pct)
	}
}

func TestMoveFile(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(srcPath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	destDir := filepath.Join(dir, "dest")
	if err := os.Mkdir(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	ops := NewOperations(nil)
	src, _ := statEntry(srcPath)
	dst, _ := statEntry(destDir)
	op := ops.Move(src, dst, nil)
	waitDone(t, op)

	if _, err := os.Stat(filepath.Join(destDir, "src.txt")); err != nil {
		t.Fatalf("moved file missing: %v", err)
	}
	// move must remove the source
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Fatalf("source still exists after move (err=%v)", err)
	}
}

// fakeImportFS is a fake remote backend implementing FileSystem + Importer +
// DirTypeSetter, recording imports and dir-type stamps instead of uploading.
type fakeImportFS struct {
	imports  []string // "destDir|name" per imported file
	dirTypes map[string][]string
}

func (f *fakeImportFS) Scheme() string                    { return "tie" }
func (f *fakeImportFS) List(string) ([]Entry, error)      { return nil, nil }
func (f *fakeImportFS) Materialize(Entry) (string, error) { return "", errors.New("not supported") }

func (f *fakeImportFS) Import(destDir, srcPath, name string) error {
	f.imports = append(f.imports, destDir+"|"+name)
	return nil
}

func (f *fakeImportFS) AddDirType(dirURI, label string) error {
	if f.dirTypes == nil {
		f.dirTypes = map[string][]string{}
	}
	f.dirTypes[dirURI] = append(f.dirTypes[dirURI], label)
	return nil
}

// fakePlainImportFS implements FileSystem + Importer but no DirTypeSetter.
// (It must not embed fakeImportFS — embedding would promote AddDirType.)
type fakePlainImportFS struct{}

func (f *fakePlainImportFS) Scheme() string               { return "tie" }
func (f *fakePlainImportFS) List(string) ([]Entry, error) { return nil, nil }
func (f *fakePlainImportFS) Materialize(Entry) (string, error) {
	return "", errors.New("not supported")
}
func (f *fakePlainImportFS) Import(destDir, srcPath, name string) error {
	return nil
}

// waitSettled waits for the op to leave the pending/running states, without
// failing on StatusError (the caller asserts the error itself).
func waitSettled(t *testing.T, op *Op) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for op.Status != StatusCompleted && op.Status != StatusError {
		if time.Now().After(deadline) {
			t.Fatal("operation did not finish in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestImportFileAppliesDirTypeToDestDir(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "song.flac")
	if err := os.WriteFile(srcPath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(nil, fake))
	src, _ := statEntry(srcPath)
	op := ops.CopyAs(src, Entry{Path: "tie:/music", IsDir: true}, "audio-dir", nil)
	waitDone(t, op)

	if len(fake.imports) != 1 || fake.imports[0] != "tie:/music|song.flac" {
		t.Fatalf("imports = %v, want [tie:/music|song.flac]", fake.imports)
	}
	got := fake.dirTypes["tie:/music"]
	if len(got) != 1 || got[0] != "audio-dir" {
		t.Fatalf("dirTypes[tie:/music] = %v, want [audio-dir]", got)
	}
}

func TestImportDirAppliesDirTypeToNewRoot(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "album")
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"a.txt": "a", filepath.Join("sub", "b.txt"): "b"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(nil, fake))
	src, _ := statEntry(srcDir)
	op := ops.CopyAs(src, Entry{Path: "tie:/music", IsDir: true}, "audio-dir", nil)
	waitDone(t, op)

	wantImports := map[string]bool{
		"tie:/music/album|a.txt":     true,
		"tie:/music/album/sub|b.txt": true,
	}
	if len(fake.imports) != len(wantImports) {
		t.Fatalf("imports = %v", fake.imports)
	}
	for _, imp := range fake.imports {
		if !wantImports[imp] {
			t.Fatalf("unexpected import %q", imp)
		}
	}
	// The label goes on the freshly created directory root, not the destination.
	got := fake.dirTypes["tie:/music/album"]
	if len(got) != 1 || got[0] != "audio-dir" {
		t.Fatalf("dirTypes[tie:/music/album] = %v, want [audio-dir]", got)
	}
	if len(fake.dirTypes["tie:/music"]) != 0 {
		t.Fatalf("destination dir must stay untyped, got %v", fake.dirTypes["tie:/music"])
	}
}

func TestImportEmptyDirStillAppliesDirType(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "empty")
	if err := os.Mkdir(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(nil, fake))
	src, _ := statEntry(srcDir)
	op := ops.CopyAs(src, Entry{Path: "tie:/music", IsDir: true}, "image-dir", nil)
	waitDone(t, op)

	if len(fake.imports) != 0 {
		t.Fatalf("empty tree must import no files, got %v", fake.imports)
	}
	got := fake.dirTypes["tie:/music/empty"]
	if len(got) != 1 || got[0] != "image-dir" {
		t.Fatalf("dirTypes[tie:/music/empty] = %v, want [image-dir]", got)
	}
}

func TestImportWithoutDirTypeLeavesLabelsAlone(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(srcPath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(nil, fake))
	src, _ := statEntry(srcPath)
	op := ops.Copy(src, Entry{Path: "tie:/docs", IsDir: true}, nil)
	waitDone(t, op)

	if len(fake.dirTypes) != 0 {
		t.Fatalf("plain copy must not label directories, got %v", fake.dirTypes)
	}
}

func TestMoveIntoTieAppliesDirType(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(srcPath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(nil, fake))
	src, _ := statEntry(srcPath)
	op := ops.MoveAs(src, Entry{Path: "tie:/videos", IsDir: true}, "video-dir", nil)
	waitDone(t, op)

	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Fatalf("source still exists after move (err=%v)", err)
	}
	got := fake.dirTypes["tie:/videos"]
	if len(got) != 1 || got[0] != "video-dir" {
		t.Fatalf("dirTypes[tie:/videos] = %v, want [video-dir]", got)
	}
}

func TestImportDirTypeUnsupportedBackendFails(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "song.flac")
	if err := os.WriteFile(srcPath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	// A backend with Importer but no DirTypeSetter must fail a typed transfer
	// rather than silently dropping the requested label.
	ops := NewOperations(NewRegistry(nil, &fakePlainImportFS{}))
	src, _ := statEntry(srcPath)
	op := ops.CopyAs(src, Entry{Path: "tie:/music", IsDir: true}, "audio-dir", nil)
	waitSettled(t, op)

	if op.Status != StatusError {
		t.Fatalf("Status = %v, want StatusError", op.Status)
	}
	if op.Err == nil || !strings.Contains(op.Err.Error(), "directory types") {
		t.Fatalf("Err = %v, want a directory-types error", op.Err)
	}
}

func TestImportAlbumWholeTreeExactDest(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "1959. Kind of Blue")
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"a.flac": "a", filepath.Join("sub", "b.flac"): "b"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(nil, fake))
	// A whole-tree group (Files == nil) mirrors its SourceDir at Dest
	// verbatim: the source directory's own name must NOT be appended.
	g := client.AlbumGroup{SourceDir: srcDir, Dest: "/music/Miles Davis/1959. Kind of Blue"}
	op := ops.ImportAlbum(g, "audio-dir", nil)
	waitDone(t, op)

	wantImports := map[string]bool{
		"tie:/music/Miles Davis/1959. Kind of Blue|a.flac":     true,
		"tie:/music/Miles Davis/1959. Kind of Blue/sub|b.flac": true,
	}
	if len(fake.imports) != len(wantImports) {
		t.Fatalf("imports = %v", fake.imports)
	}
	for _, imp := range fake.imports {
		if !wantImports[imp] {
			t.Fatalf("unexpected import %q", imp)
		}
	}
	got := fake.dirTypes["tie:/music/Miles Davis/1959. Kind of Blue"]
	if len(got) != 1 || got[0] != "audio-dir" {
		t.Fatalf("dirTypes[dest] = %v, want [audio-dir]", got)
	}
}

func TestImportAlbumFileList(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "rip")
	if err := os.MkdirAll(filepath.Join(srcDir, "CD2"), 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"a.flac":                       "aa",
		filepath.Join("CD2", "b.flac"): "bbb",
		"notes.txt":                    "not in the group",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(nil, fake))
	// A file-list group imports only its listed files, into
	// Dest/<rel-below-SourceDir>; unlisted tree members stay local.
	g := client.AlbumGroup{
		SourceDir: srcDir,
		Files: []string{
			filepath.Join(srcDir, "a.flac"),
			filepath.Join(srcDir, "CD2", "b.flac"),
		},
		Dest: "/music/Artist/Album",
		Size: 5, // 2 + 3 bytes, as probed by the planner
	}
	op := ops.ImportAlbum(g, "audio-dir", nil)
	waitDone(t, op)

	wantImports := map[string]bool{
		"tie:/music/Artist/Album|a.flac":     true,
		"tie:/music/Artist/Album/CD2|b.flac": true,
	}
	if len(fake.imports) != len(wantImports) {
		t.Fatalf("imports = %v", fake.imports)
	}
	for _, imp := range fake.imports {
		if !wantImports[imp] {
			t.Fatalf("unexpected import %q", imp)
		}
	}
	got := fake.dirTypes["tie:/music/Artist/Album"]
	if len(got) != 1 || got[0] != "audio-dir" {
		t.Fatalf("dirTypes[dest] = %v, want [audio-dir]", got)
	}
	if pct := op.PctComplete(); pct != 1 {
		t.Fatalf("PctComplete = %v, want 1", pct)
	}
}

func TestImportAlbumArchiveSingleFile(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "live")
	if err := os.Mkdir(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(srcDir, "bootleg.zip")
	if err := os.WriteFile(zipPath, []byte("pk..."), 0644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(nil, fake))
	// An archive group is a single-file import into the destination directory;
	// the blob carries the audio-archive classification itself, so no dir-type
	// is stamped even though one was requested for the album groups.
	g := client.AlbumGroup{
		SourceDir: srcDir,
		Files:     []string{zipPath},
		IsArchive: true,
		Dest:      "/music/bootlegs",
		Size:      5,
	}
	op := ops.ImportAlbum(g, "audio-dir", nil)
	waitDone(t, op)

	if len(fake.imports) != 1 || fake.imports[0] != "tie:/music/bootlegs|bootleg.zip" {
		t.Fatalf("imports = %v, want [tie:/music/bootlegs|bootleg.zip]", fake.imports)
	}
	if len(fake.dirTypes) != 0 {
		t.Fatalf("archive groups must not stamp dir types, got %v", fake.dirTypes)
	}
}

func TestImportAlbumMultipleGroupsSequential(t *testing.T) {
	dir := t.TempDir()
	var groups []client.AlbumGroup
	for _, name := range []string{"album1", "album2", "album3"} {
		srcDir := filepath.Join(dir, name)
		if err := os.Mkdir(srcDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, name+".flac"), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
		groups = append(groups, client.AlbumGroup{SourceDir: srcDir, Dest: "/music/" + name})
	}

	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(nil, fake))
	var pending []*Op
	for _, g := range groups {
		pending = append(pending, ops.ImportAlbum(g, "audio-dir", nil))
	}
	for _, op := range pending {
		waitDone(t, op)
	}
	if len(fake.imports) != 3 {
		t.Fatalf("imports = %v, want 3 albums imported", fake.imports)
	}
	if len(fake.dirTypes) != 3 {
		t.Fatalf("dirTypes = %v, want 3 stamps", fake.dirTypes)
	}
}

func TestImportAlbumMixedGroupTypes(t *testing.T) {
	dir := t.TempDir()
	treeDir := filepath.Join(dir, "tree")
	listDir := filepath.Join(dir, "list")
	zipDir := filepath.Join(dir, "zips")
	for _, d := range []string{treeDir, listDir, zipDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(treeDir, "a.flac"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(listDir, "b.flac"), []byte("bb"), 0644); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(zipDir, "live.zip")
	if err := os.WriteFile(zipPath, []byte("pk"), 0644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(nil, fake))
	groups := []client.AlbumGroup{
		{SourceDir: treeDir, Dest: "/music/tree"},                                            // whole-tree
		{SourceDir: listDir, Files: []string{filepath.Join(listDir, "b.flac")}, Dest: "/music/list", Size: 2}, // file-list
		{SourceDir: zipDir, Files: []string{zipPath}, IsArchive: true, Dest: "/music", Size: 2},                // archive
	}
	var pending []*Op
	for _, g := range groups {
		pending = append(pending, ops.ImportAlbum(g, "audio-dir", nil))
	}
	for _, op := range pending {
		waitDone(t, op)
	}

	wantImports := map[string]bool{
		"tie:/music/tree|a.flac":   true,
		"tie:/music/list|b.flac":   true,
		"tie:/music|live.zip":      true,
	}
	if len(fake.imports) != len(wantImports) {
		t.Fatalf("imports = %v", fake.imports)
	}
	for _, imp := range fake.imports {
		if !wantImports[imp] {
			t.Fatalf("unexpected import %q", imp)
		}
	}
	if len(fake.dirTypes["tie:/music/tree"]) != 1 || len(fake.dirTypes["tie:/music/list"]) != 1 {
		t.Fatalf("dirTypes = %v, want tree+list stamped", fake.dirTypes)
	}
	if len(fake.dirTypes["tie:/music"]) != 0 {
		t.Fatalf("archive dest must stay unstamped, got %v", fake.dirTypes["tie:/music"])
	}
}
