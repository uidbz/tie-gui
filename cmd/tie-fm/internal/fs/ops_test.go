package fs

import (
	"errors"
	"io"
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
// TagImporter + DirLabeler, recording imports and directory labels instead of
// uploading.
type fakeImportFS struct {
	imports []string   // "destDir|name" per imported file
	tags    [][]string // tags per ImportTagged call (parallel to imports when tagged)
	labels  map[string][]DirLabel
}

func (f *fakeImportFS) Scheme() string                    { return "tie" }
func (f *fakeImportFS) List(string) ([]Entry, error)      { return nil, nil }
func (f *fakeImportFS) Materialize(Entry) (string, error) { return "", errors.New("not supported") }

func (f *fakeImportFS) Import(destDir, srcPath, name string) error {
	f.imports = append(f.imports, destDir+"|"+name)
	return nil
}

func (f *fakeImportFS) ImportTagged(destDir, srcPath, name string, tags []string, _ io.Writer) error {
	f.imports = append(f.imports, destDir+"|"+name)
	f.tags = append(f.tags, tags)
	return nil
}

func (f *fakeImportFS) LabelDir(dirURI string, label DirLabel) error {
	if f.labels == nil {
		f.labels = map[string][]DirLabel{}
	}
	f.labels[dirURI] = append(f.labels[dirURI], label)
	return nil
}

// fakePlainImportFS implements FileSystem + Importer but no DirLabeler.
// (It must not embed fakeImportFS — embedding would promote LabelDir.)
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
	got := fake.labels["tie:/music"]
	if len(got) != 1 || got[0].Type != "audio-dir" {
		t.Fatalf("dirTypes[tie:/music] = %v, want Type=audio-dir", got)
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
	got := fake.labels["tie:/music/album"]
	if len(got) != 1 || got[0].Type != "audio-dir" {
		t.Fatalf("dirTypes[tie:/music/album] = %v, want Type=audio-dir", got)
	}
	if len(fake.labels["tie:/music"]) != 0 {
		t.Fatalf("destination dir must stay untyped, got %v", fake.labels["tie:/music"])
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
	got := fake.labels["tie:/music/empty"]
	if len(got) != 1 || got[0].Type != "image-dir" {
		t.Fatalf("dirTypes[tie:/music/empty] = %v, want Type=image-dir", got)
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

	if len(fake.labels) != 0 {
		t.Fatalf("plain copy must not label directories, got %v", fake.labels)
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
	got := fake.labels["tie:/videos"]
	if len(got) != 1 || got[0].Type != "video-dir" {
		t.Fatalf("dirTypes[tie:/videos] = %v, want Type=video-dir", got)
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
	op := ops.ImportAlbum(g, "audio-dir", nil, nil)
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
	got := fake.labels["tie:/music/Miles Davis/1959. Kind of Blue"]
	if len(got) != 1 || got[0].Type != "audio-dir" {
		t.Fatalf("dirTypes[dest] = %v, want Type=audio-dir", got)
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
	op := ops.ImportAlbum(g, "audio-dir", nil, nil)
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
	got := fake.labels["tie:/music/Artist/Album"]
	if len(got) != 1 || got[0].Type != "audio-dir" {
		t.Fatalf("dirTypes[dest] = %v, want Type=audio-dir", got)
	}
	if pct := op.PctComplete(); pct != 1 {
		t.Fatalf("PctComplete = %v, want 1", pct)
	}
}

func TestImportAlbumFileSubPaths(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "rip")
	for _, sub := range []string{filepath.Join("CD1", "a.flac"), filepath.Join("CD2", "b.flac"), filepath.Join("bonus", "c.flac")} {
		if err := os.MkdirAll(filepath.Join(srcDir, filepath.Dir(sub)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, sub), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(nil, fake))
	// The planner's disc-aware routing: CD1/CD2 members land in cd1/cd2
	// subdirectories; a file missing from SubPaths keeps its legacy relative
	// placement.
	a := filepath.Join(srcDir, "CD1", "a.flac")
	b := filepath.Join(srcDir, "CD2", "b.flac")
	c := filepath.Join(srcDir, "bonus", "c.flac")
	g := client.AlbumGroup{
		SourceDir: srcDir,
		Files:     []string{a, b, c},
		SubPaths:  map[string]string{a: "cd1/a.flac", b: "cd2/b.flac"},
		Dest:      "/music/Artist/Album",
	}
	op := ops.ImportAlbum(g, "audio-dir", nil, nil)
	waitDone(t, op)

	wantImports := map[string]bool{
		"tie:/music/Artist/Album/cd1|a.flac":   true,
		"tie:/music/Artist/Album/cd2|b.flac":   true,
		"tie:/music/Artist/Album/bonus|c.flac": true,
	}
	if len(fake.imports) != len(wantImports) {
		t.Fatalf("imports = %v", fake.imports)
	}
	for _, imp := range fake.imports {
		if !wantImports[imp] {
			t.Fatalf("unexpected import %q", imp)
		}
	}
}

func TestImportAlbumConvertedWholeTree(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "rip")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"a.flac", "b.flac", "cover.jpg"} {
		if err := os.WriteFile(filepath.Join(srcDir, sub), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(nil, fake))
	// A whole-tree group the planner converted for its disc structure arrives
	// as Files+Sidecars+SubPaths: the sidecars import too (cover art must not
	// be left behind), at their planned subpaths.
	a := filepath.Join(srcDir, "a.flac")
	b := filepath.Join(srcDir, "b.flac")
	cover := filepath.Join(srcDir, "cover.jpg")
	g := client.AlbumGroup{
		SourceDir: srcDir,
		Files:     []string{a, b},
		Sidecars:  []string{cover},
		SubPaths:  map[string]string{a: "cd1/a.flac", b: "cd2/b.flac", cover: "cover.jpg"},
		Dest:      "/music/Artist/Album",
	}
	op := ops.ImportAlbum(g, "audio-dir", nil, nil)
	waitDone(t, op)

	wantImports := map[string]bool{
		"tie:/music/Artist/Album/cd1|a.flac": true,
		"tie:/music/Artist/Album/cd2|b.flac": true,
		"tie:/music/Artist/Album|cover.jpg":  true,
	}
	if len(fake.imports) != len(wantImports) {
		t.Fatalf("imports = %v", fake.imports)
	}
	for _, imp := range fake.imports {
		if !wantImports[imp] {
			t.Fatalf("unexpected import %q", imp)
		}
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
	op := ops.ImportAlbum(g, "audio-dir", nil, nil)
	waitDone(t, op)

	if len(fake.imports) != 1 || fake.imports[0] != "tie:/music/bootlegs|bootleg.zip" {
		t.Fatalf("imports = %v, want [tie:/music/bootlegs|bootleg.zip]", fake.imports)
	}
	if len(fake.labels) != 0 {
		t.Fatalf("archive groups must not stamp dir types, got %v", fake.labels)
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
		pending = append(pending, ops.ImportAlbum(g, "audio-dir", nil, nil))
	}
	for _, op := range pending {
		waitDone(t, op)
	}
	if len(fake.imports) != 3 {
		t.Fatalf("imports = %v, want 3 albums imported", fake.imports)
	}
	if len(fake.labels) != 3 {
		t.Fatalf("dirTypes = %v, want 3 stamps", fake.labels)
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
		{SourceDir: treeDir, Dest: "/music/tree"},                                                             // whole-tree
		{SourceDir: listDir, Files: []string{filepath.Join(listDir, "b.flac")}, Dest: "/music/list", Size: 2}, // file-list
		{SourceDir: zipDir, Files: []string{zipPath}, IsArchive: true, Dest: "/music", Size: 2},               // archive
	}
	var pending []*Op
	for _, g := range groups {
		pending = append(pending, ops.ImportAlbum(g, "audio-dir", nil, nil))
	}
	for _, op := range pending {
		waitDone(t, op)
	}

	wantImports := map[string]bool{
		"tie:/music/tree|a.flac": true,
		"tie:/music/list|b.flac": true,
		"tie:/music|live.zip":    true,
	}
	if len(fake.imports) != len(wantImports) {
		t.Fatalf("imports = %v", fake.imports)
	}
	for _, imp := range fake.imports {
		if !wantImports[imp] {
			t.Fatalf("unexpected import %q", imp)
		}
	}
	if len(fake.labels["tie:/music/tree"]) != 1 || len(fake.labels["tie:/music/list"]) != 1 {
		t.Fatalf("dirTypes = %v, want tree+list stamped", fake.labels)
	}
	if len(fake.labels["tie:/music"]) != 0 {
		t.Fatalf("archive dest must stay unstamped, got %v", fake.labels["tie:/music"])
	}
}

func TestImportAlbumWithTagsAndMetadata(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "1959. Kind of Blue")
	if err := os.Mkdir(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"01. So What.flac", "02. Freddie Freeloader.flac"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(nil, fake))
	g := client.AlbumGroup{
		SourceDir: srcDir,
		Dest:      "/music/Miles Davis/1959. Kind of Blue",
		Artist:    "Miles Davis",
		Album:     "Kind of Blue",
		Year:      1959,
	}
	op := ops.ImportAlbum(g, "audio-dir", []string{"jazz", "miles"}, nil)
	waitDone(t, op)

	// Every track must be imported through the tagging path with the tags.
	if len(fake.imports) != 2 || len(fake.tags) != 2 {
		t.Fatalf("imports = %v, tags = %v; want 2 tagged imports", fake.imports, fake.tags)
	}
	for i, tags := range fake.tags {
		if len(tags) != 2 || tags[0] != "jazz" || tags[1] != "miles" {
			t.Fatalf("tags[%d] = %v, want [jazz miles]", i, tags)
		}
	}
	// The album root gets the dir-type, the tags, its folder name and the
	// group's aggregated metadata.
	got := fake.labels["tie:/music/Miles Davis/1959. Kind of Blue"]
	if len(got) != 1 {
		t.Fatalf("labels[dest] = %v, want one label", got)
	}
	label := got[0]
	if label.Type != "audio-dir" {
		t.Fatalf("label.Type = %q, want audio-dir", label.Type)
	}
	if len(label.Tags) != 2 || label.Tags[0] != "jazz" || label.Tags[1] != "miles" {
		t.Fatalf("label.Tags = %v, want [jazz miles]", label.Tags)
	}
	if label.Name != "1959. Kind of Blue" {
		t.Fatalf("label.Name = %q, want the folder name", label.Name)
	}
	if label.Artist != "Miles Davis" || label.Album != "Kind of Blue" || label.Year != "1959" {
		t.Fatalf("label aggregates = %q/%q/%q, want Miles Davis/Kind of Blue/1959",
			label.Artist, label.Album, label.Year)
	}
}

func TestImportAlbumFileListWithTags(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "rip")
	if err := os.Mkdir(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	flac := filepath.Join(srcDir, "a.flac")
	if err := os.WriteFile(flac, []byte("aa"), 0644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(nil, fake))
	g := client.AlbumGroup{
		SourceDir: srcDir,
		Files:     []string{flac},
		Dest:      "/music/Artist/Album",
		Size:      2,
		Artist:    "Artist",
		Album:     "Album",
	}
	op := ops.ImportAlbum(g, "audio-dir", []string{"rock"}, nil)
	waitDone(t, op)

	if len(fake.tags) != 1 || len(fake.tags[0]) != 1 || fake.tags[0][0] != "rock" {
		t.Fatalf("tags = %v, want the track tagged [rock]", fake.tags)
	}
	got := fake.labels["tie:/music/Artist/Album"]
	if len(got) != 1 || got[0].Type != "audio-dir" {
		t.Fatalf("labels[dest] = %v, want Type=audio-dir", got)
	}
	if len(got[0].Tags) != 1 || got[0].Tags[0] != "rock" {
		t.Fatalf("label.Tags = %v, want [rock]", got[0].Tags)
	}
	if got[0].Artist != "Artist" || got[0].Album != "Album" {
		t.Fatalf("label aggregates = %q/%q, want Artist/Album", got[0].Artist, got[0].Album)
	}
}

func TestImportAlbumArchiveWithTags(t *testing.T) {
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
	// An archive group is the album itself: the blob is tagged so it is
	// queryable, but the destination directory stays unlabeled and untagged.
	g := client.AlbumGroup{
		SourceDir: srcDir,
		Files:     []string{zipPath},
		IsArchive: true,
		Dest:      "/music/bootlegs",
		Size:      5,
	}
	op := ops.ImportAlbum(g, "audio-dir", []string{"jazz"}, nil)
	waitDone(t, op)

	if len(fake.imports) != 1 || fake.imports[0] != "tie:/music/bootlegs|bootleg.zip" {
		t.Fatalf("imports = %v, want [tie:/music/bootlegs|bootleg.zip]", fake.imports)
	}
	if len(fake.tags) != 1 || len(fake.tags[0]) != 1 || fake.tags[0][0] != "jazz" {
		t.Fatalf("tags = %v, want the archive blob tagged [jazz]", fake.tags)
	}
	if len(fake.labels) != 0 {
		t.Fatalf("archive groups must not label directories, got %v", fake.labels)
	}
}

func TestImportTagsUnsupportedBackendFails(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "album")
	if err := os.Mkdir(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.flac"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}

	// A backend with Importer but no TagImporter must fail a tagged transfer
	// rather than silently dropping the requested tags.
	ops := NewOperations(NewRegistry(nil, &fakePlainImportFS{}))
	g := client.AlbumGroup{SourceDir: srcDir, Dest: "/music/album"}
	op := ops.ImportAlbum(g, "audio-dir", []string{"jazz"}, nil)
	waitSettled(t, op)

	if op.Status != StatusError {
		t.Fatalf("Status = %v, want StatusError", op.Status)
	}
	if op.Err == nil || !strings.Contains(op.Err.Error(), "tagging") {
		t.Fatalf("Err = %v, want a tagging error", op.Err)
	}
}

func TestImportDirLabelsRootWithName(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "album")
	if err := os.Mkdir(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.flac"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeImportFS{}
	ops := NewOperations(NewRegistry(nil, fake))
	src, _ := statEntry(srcDir)
	// Even an untyped directory import records the new root's folder name, so
	// media apps can title the directory without listing it.
	op := ops.Copy(src, Entry{Path: "tie:/music", IsDir: true}, nil)
	waitDone(t, op)

	got := fake.labels["tie:/music/album"]
	if len(got) != 1 {
		t.Fatalf("labels[tie:/music/album] = %v, want one label", got)
	}
	if got[0].Type != "" || len(got[0].Tags) != 0 {
		t.Fatalf("plain copy must not type or tag directories, got %+v", got[0])
	}
	if got[0].Name != "album" {
		t.Fatalf("label.Name = %q, want album", got[0].Name)
	}
}
