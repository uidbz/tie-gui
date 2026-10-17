package tielog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupWritesFileAndStderr(t *testing.T) {
	dir := t.TempDir()
	fake := &bytes.Buffer{}

	log, closeLog := Setup(Options{App: "tie-view", Dir: dir, Stderr: fake})
	defer closeLog()

	log.Error("upload thumbnail", "err", "connection refused", "path", "abc123")
	log.Info("ignored at info level? no, info is kept")

	body, err := os.ReadFile(filepath.Join(dir, "tie-view.log"))
	if err != nil {
		t.Fatalf("log file: %v", err)
	}
	s := string(body)
	for _, want := range []string{
		"tie-view: ERROR upload thumbnail",
		`err="connection refused"`,
		"path=abc123",
		"tie-view: INFO ignored at info level? no, info is kept",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("log file is missing %q:\n%s", want, s)
		}
	}
	if fake.String() != s {
		t.Errorf("stderr and the file disagree:\nstderr:\n%s\nfile:\n%s", fake.String(), s)
	}
	// The file holds a config-adjacent record of every operation the app
	// performed, so it must not be world-readable.
	if fi, err := os.Stat(filepath.Join(dir, "tie-view.log")); err == nil && fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("log file mode = %v, want private to the user", fi.Mode().Perm())
	}
}

func TestQuietDropsStderr(t *testing.T) {
	dir := t.TempDir()
	fake := &bytes.Buffer{}

	log, closeLog := Setup(Options{App: "tie-fm", Dir: dir, Quiet: true, Stderr: fake})
	defer closeLog()

	log.Error("still recorded")
	if fake.Len() != 0 {
		t.Errorf("-q still wrote to stderr: %q", fake.String())
	}
	body, err := os.ReadFile(filepath.Join(dir, "tie-fm.log"))
	if err != nil || !bytes.Contains(body, []byte("still recorded")) {
		t.Errorf("quiet mode lost the log file: %q (%v)", body, err)
	}
}

func TestLevelFiltering(t *testing.T) {
	dir := t.TempDir()
	fake := &bytes.Buffer{}

	normal, closeNormal := Setup(Options{App: "a", Dir: dir, Stderr: fake})
	normal.Debug("hidden")
	normal.Info("shown")
	closeNormal()

	if strings.Contains(fake.String(), "hidden") {
		t.Error("Debug reached stderr without -v")
	}
	if !strings.Contains(fake.String(), "shown") {
		t.Error("Info was filtered without -v")
	}

	fake.Reset()
	verbose, closeVerbose := Setup(Options{App: "b", Dir: dir, Verbose: true, Stderr: fake})
	verbose.Debug("now visible")
	closeVerbose()
	if !strings.Contains(fake.String(), "now visible") {
		t.Errorf("-v did not enable Debug:\n%s", fake.String())
	}
}

func TestRotateOnOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tie-audio.log")
	big := bytes.Repeat([]byte("x"), MaxFileSize+1)
	if err := os.WriteFile(path, big, 0o600); err != nil {
		t.Fatal(err)
	}

	log, closeLog := Setup(Options{App: "tie-audio", Dir: dir, Quiet: true})
	log.Info("fresh run")
	closeLog()

	old, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("the oversized log was not rotated: %v", err)
	}
	if len(old) != len(big) {
		t.Errorf("rotated file is %d bytes, want %d", len(old), len(big))
	}
	fresh, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(fresh, []byte("fresh run")) || bytes.Contains(fresh, []byte("xxxxx")) {
		t.Errorf("new log = %q, want only the new records", fresh)
	}
}

func TestSmallFileIsNotRotated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tie.log")
	if err := os.WriteFile(path, []byte("previous\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	log, closeLog := Setup(Options{App: "tie", Dir: dir, Quiet: true})
	log.Info("appended")
	closeLog()

	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Error("a small log file was rotated away")
	}
	body, _ := os.ReadFile(path)
	if !bytes.Contains(body, []byte("previous")) || !bytes.Contains(body, []byte("appended")) {
		t.Errorf("log = %q, want the old contents kept and the new line appended", body)
	}
}

// Recover is what keeps a panic on a loader goroutine from killing the app.
func TestRecoverLogsAndDoesNotRethrow(t *testing.T) {
	fake := &bytes.Buffer{}

	log, closeLog := Setup(Options{App: "tie-view", Dir: t.TempDir(), Stderr: fake})
	defer closeLog()

	func() {
		defer Recover(log, "gallery.tileUpdater")
		panic("boom")
	}() // must return normally

	s := fake.String()
	for _, want := range []string{"panic", "where=gallery.tileUpdater", "panic=boom", "stack="} {
		if !strings.Contains(s, want) {
			t.Errorf("the panic record is missing %q:\n%s", want, s)
		}
	}
}

func TestRecoverOnNilLoggerIsSafe(t *testing.T) {
	defer Recover(nil, "nowhere")
	panic("still swallowed")
}

func TestDiscardIsUsable(t *testing.T) {
	log := Discard()
	if log == nil {
		t.Fatal("Discard returned nil")
	}
	// The point of the default is that a library can log unconditionally: no
	// nil checks at the call sites.
	log.Error("dropped", "k", "v")
	log.With("app", "x").Warn("also dropped")
}

func TestStateDirHonoursXDG(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/state")
	got, err := StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join("/tmp/state", "tie") {
		t.Errorf("StateDir = %q, want /tmp/state/tie", got)
	}

	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/u")
	got, err = StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join("/home/u", ".local", "state", "tie") {
		t.Errorf("StateDir = %q, want $HOME/.local/state/tie", got)
	}
}
