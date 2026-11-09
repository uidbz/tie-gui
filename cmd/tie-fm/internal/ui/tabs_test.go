package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
)

func TestTabbedPaneNewAndClose(t *testing.T) {
	test.NewApp()
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	reg := fs.NewRegistry(fs.NewLocalFS(), fs.NewTieFS(nil))
	tp := NewTabbedPane(dir1, reg, fs.NewOperations(reg), nil, nil)

	if len(tp.tabs) != 1 || tp.CurrentPath() != dir1 {
		t.Fatalf("initial tabs = %d path = %q, want 1 tab at %q", len(tp.tabs), tp.CurrentPath(), dir1)
	}

	tp.NewTabAt(dir2)
	if len(tp.tabs) != 2 || tp.CurrentPath() != dir2 {
		t.Fatalf("after new tab: tabs = %d path = %q, want 2 tabs at %q", len(tp.tabs), tp.CurrentPath(), dir2)
	}

	tp.CloseTab()
	if len(tp.tabs) != 1 || tp.CurrentPath() != dir1 {
		t.Fatalf("after close: tabs = %d path = %q, want the first tab at %q", len(tp.tabs), tp.CurrentPath(), dir1)
	}

	// Closing the last tab keeps one pane location available.
	tp.CloseTab()
	if len(tp.tabs) != 1 {
		t.Fatalf("closing the last tab left %d tabs", len(tp.tabs))
	}
}

func TestActivateRowCtrlOpensNewTab(t *testing.T) {
	fm := &FileManager{
		pageEntries: []fs.Entry{{Name: "sub", Path: "/tmp/sub", IsDir: true}},
	}
	var opened string
	fm.onOpenInNewTab = func(path string) { opened = path }

	fm.activateRow(0, true)
	if opened != "/tmp/sub" {
		t.Fatalf("Ctrl+click opened %q, want /tmp/sub", opened)
	}
}

func TestTabTitle(t *testing.T) {
	cases := map[string]string{
		"":                "/",
		"/":               "/",
		"/home/user":      "user",
		"/home/user/":     "user",
		"tie:/":           "tie:/",
		"tie:/music/rock": "rock",
	}
	for path, want := range cases {
		if got := tabTitle(path); got != want {
			t.Errorf("tabTitle(%q) = %q, want %q", path, got, want)
		}
	}
}
