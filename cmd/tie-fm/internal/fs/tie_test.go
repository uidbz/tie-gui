package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uidbz/tie/client"
)

func TestTiePath(t *testing.T) {
	cases := map[string]string{
		"tie:/":            "/",
		"tie:":             "/",
		"tie:/music":       "/music",
		"tie:/music/album": "/music/album",
		"tie:music":        "/music",
	}
	for in, want := range cases {
		if got := tiePath(in); got != want {
			t.Errorf("tiePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTieURI(t *testing.T) {
	s := client.FileURIScheme // the tie client's internal path scheme
	cases := map[string]string{
		s + "/music/album": "tie:/music/album",
		"/music/album":     "tie:/music/album",
		s + "/":            "tie:/",
		"":                 "tie:/",
	}
	for in, want := range cases {
		if got := tieURI(in); got != want {
			t.Errorf("tieURI(%q) = %q, want %q", in, got, want)
		}
	}
}

// Delete on a tie entry must unlink its triples but leave the content blob on
// the filehost for retention to collect (shared content may still reference
// it). Runs only when the local tie test-env is up, like the client package's
// own integration tests.
func TestTieRemoveDeletesTriplesNotBlob(t *testing.T) {
	tc := client.NewTieClient(client.TestingConfig())
	if _, err := tc.ServerVersion(); err != nil {
		t.Skipf("tie test-env not reachable: %v", err)
	}
	host, err := tc.ResolveHost("")
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}

	rootPath := fmt.Sprintf("/tie-fm-remove-%d", time.Now().UnixNano())
	root, err := tc.MkTieDirAll(client.FileURIScheme + rootPath)
	if err != nil {
		t.Fatal(err)
	}
	tfs := NewTieFS(tc)
	t.Cleanup(func() {
		_ = tfs.Remove(Entry{Name: filepath.Base(rootPath), Path: "tie:" + rootPath, IsDir: true, Hash: string(root)})
	})

	write := func(parent client.DirUID, name, content string) string {
		t.Helper()
		src := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(src, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		hash, err := tc.WriteFile(host, "", parent, name, src, nil)
		if err != nil {
			t.Fatal(err)
		}
		return hash
	}

	hash := write(root, "gone.txt", "content survives the delete")
	entries, err := tfs.List("tie:" + rootPath)
	if err != nil {
		t.Fatal(err)
	}
	var file Entry
	for _, e := range entries {
		if e.Name == "gone.txt" {
			file = e
		}
	}
	if file.Hash == "" {
		t.Fatal("file not listed before delete")
	}
	if err := tfs.Remove(file); err != nil {
		t.Fatalf("Remove file: %v", err)
	}
	entries, err = tfs.List("tie:" + rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("listing after file delete = %v, want empty", entries)
	}
	if _, err := tc.Get(hash); err == nil {
		t.Error("file still has metadata triples after its last parent was removed")
	}
	if _, err := client.DownloadSize(host, hash); err != nil {
		t.Errorf("content blob was deleted from the filehost: %v", err)
	}

	childPath := rootPath + "/subdir"
	child, err := tc.MkTieDirAll(client.FileURIScheme + childPath)
	if err != nil {
		t.Fatal(err)
	}
	write(child, "nested.txt", "nested content")
	entries, err = tfs.List("tie:" + rootPath)
	if err != nil {
		t.Fatal(err)
	}
	var dir Entry
	for _, e := range entries {
		if e.Name == "subdir" {
			dir = e
		}
	}
	if !dir.IsDir {
		t.Fatal("directory not listed before delete")
	}
	if err := tfs.Remove(dir); err != nil {
		t.Fatalf("Remove directory: %v", err)
	}
	if got, err := tc.DirUIDFromPath(childPath); err != nil || got != "" {
		t.Errorf("DirUIDFromPath after recursive delete = %q, %v; want empty", got, err)
	}
}
