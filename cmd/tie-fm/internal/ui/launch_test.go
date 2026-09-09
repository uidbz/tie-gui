package ui

import (
	"reflect"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
)

func TestBuildCommand(t *testing.T) {
	cases := []struct {
		cmdline string
		file    string
		want    []string // program + args, nil = nil cmd
	}{
		{"mpv", "/tmp/a.mkv", []string{"mpv", "/tmp/a.mkv"}},                      // path appended
		{"mpv %f", "/tmp/a.mkv", []string{"mpv", "/tmp/a.mkv"}},                   // %f replaced
		{"foo --flag %f --loop", "/x", []string{"foo", "--flag", "/x", "--loop"}}, // %f mid-args, no append
		{"", "/x", nil},
	}
	for _, c := range cases {
		cmd := buildCommand(c.cmdline, c.file)
		if c.want == nil {
			if cmd != nil {
				t.Errorf("buildCommand(%q) = %v, want nil", c.cmdline, cmd.Args)
			}
			continue
		}
		if cmd == nil {
			t.Errorf("buildCommand(%q) = nil, want %v", c.cmdline, c.want)
			continue
		}
		if !reflect.DeepEqual(cmd.Args, c.want) {
			t.Errorf("buildCommand(%q) args = %v, want %v", c.cmdline, cmd.Args, c.want)
		}
	}
}

// stubTieFS is a tie backend that never touches the network: Materialize
// reports a fixed path and remembers it was called.
type stubTieFS struct {
	materializedPath string
	materialized     bool
}

func (s *stubTieFS) Scheme() string                  { return "tie" }
func (s *stubTieFS) List(string) ([]fs.Entry, error) { return nil, nil }
func (s *stubTieFS) Materialize(fs.Entry) (string, error) {
	s.materialized = true
	return s.materializedPath, nil
}

// captureLaunch stubs the process launcher and returns what it was handed.
func captureLaunch(t *testing.T) func() (targets []string) {
	t.Helper()
	var got []string
	old := launch
	launch = func(cfg *config.Config, target, name string) error {
		got = append(got, target)
		return nil
	}
	t.Cleanup(func() { launch = old })
	return func() []string { return got }
}

// TestOpenEntryTieURL checks the tie: URL association dispatch: a tie entry
// is handed to the app as tie:<hash> without a download; TieURL wins over
// Stream; entries without a usable hash (or on another backend) fall back to
// the materialized path.
func TestOpenEntryTieURL(t *testing.T) {
	test.NewApp()
	win := test.NewWindow(nil)
	targets := captureLaunch(t)

	tie := &stubTieFS{materializedPath: "/tmp/tie-fm-stub"}
	registry := fs.NewRegistry(fs.NewLocalFS(), tie)
	ops := fs.NewOperations(registry)

	cfg := config.Default()
	cfg.SetApp("jpg", config.AppAssoc{Command: "tie-view %f", Stream: true, TieURL: true})

	fm := NewFileManager(t.TempDir(), registry, ops, &cfg, win)

	// Tie entry: handed over as tie:<hash>, no materialization.
	fm.openEntry(fs.Entry{Name: "pic.jpg", Path: "tie:/x/pic.jpg", Hash: "abc123"})
	got := targets()
	if len(got) != 1 || got[0] != "tie:abc123" {
		t.Fatalf("openEntry tie entry launched %v, want [tie:abc123]", got)
	}
	if tie.materialized {
		t.Fatal("tie: URL association must not materialize the entry")
	}

	// Local entry with the same association: real path, not a tie: URL.
	local := fs.Entry{Name: "pic.jpg", Path: "/home/x/pic.jpg"}
	fm.openEntry(local)
	got = targets()
	if len(got) != 2 || got[1] != local.Path {
		t.Fatalf("openEntry local entry launched %v, want the real path appended", got)
	}

	// Tie entry without a content hash: falls back to a materialized copy.
	fm.openEntry(fs.Entry{Name: "pic.jpg", Path: "tie:/x/pic.jpg"})
	got = targets()
	if len(got) != 3 || got[2] != tie.materializedPath {
		t.Fatalf("openEntry hashless tie entry launched %v, want the materialized path", got)
	}

	// A plain association (no flags) still materializes.
	cfg.SetApp("jpg", config.AppAssoc{Command: "tie-view %f"})
	tie.materialized = false
	fm.openEntry(fs.Entry{Name: "pic.jpg", Path: "tie:/x/pic.jpg", Hash: "abc123"})
	if got = targets(); len(got) != 4 || got[3] != tie.materializedPath {
		t.Fatalf("openEntry plain association launched %v, want the materialized path", got)
	}
}
