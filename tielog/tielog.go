// Package tielog is the logging setup shared by the tie apps.
//
// The rule it exists to enforce: a library in this repository never writes to
// stdout or stderr. gallery, tiethumb, tieconfig, tagselection and mpvplayer
// take a *slog.Logger (defaulting to discard) and the application decides
// where the output goes. That is not cosmetics — the Android builds have no
// stdout at all, which is exactly where the network is least reliable, so a
// library's fmt.Println is simply lost.
//
// A desktop main calls Setup once:
//
//	log, closeLog := tielog.Setup(tielog.Options{App: "tie-view", Verbose: *verbose})
//	defer closeLog()
//
// which gives one logger writing to stderr and to
// $XDG_STATE_HOME/tie/<app>.log, rotated on open once it passes MaxFileSize.
package tielog

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
)

// MaxFileSize is the size at which the log file is rotated, on startup. The
// previous contents move to <app>.log.1; there is no deeper history, which is
// enough to answer "what happened the last time it broke".
const MaxFileSize = 1 << 20

// Options configures Setup.
type Options struct {
	// App names the log file and prefixes every line. Use the binary name.
	App string
	// Verbose raises the level to Debug.
	Verbose bool
	// Quiet drops the stderr sink; the log file still records everything.
	Quiet bool
	// NoFile disables the log file, for tests and for a foreground run that
	// should leave nothing behind.
	NoFile bool
	// Dir overrides the log directory. Empty means StateDir().
	Dir string
	// Stderr overrides the console sink. It defaults to os.Stderr; set it in a
	// test, or to fold console output into another stream.
	Stderr io.Writer
}

// AddFlags registers the logging flags on fs (normally flag.CommandLine) and
// records their values in the Options. Call it before flag.Parse so every app
// takes the same switches:
//
//	var logOpts tielog.Options
//	logOpts.App = "tie-view"
//	logOpts.AddFlags(flag.CommandLine)
//	flag.Parse()
//	log, closeLog := tielog.Setup(logOpts)
//	defer closeLog()
func (o *Options) AddFlags(fs *flag.FlagSet) {
	fs.BoolVar(&o.Verbose, "v", false, "log debug output as well")
	fs.BoolVar(&o.Quiet, "q", false, "keep the terminal clean; the log file still records")
	fs.BoolVar(&o.NoFile, "no-log", false, "do not write a log file")
}

// StateDir is where per-app logs live: $XDG_STATE_HOME/tie, or
// $HOME/.local/state/tie when XDG_STATE_HOME is unset.
func StateDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "tie"), nil
}

// Setup returns a logger writing to stderr and to the app's log file, plus a
// close function. Failing to open the log file is not fatal — the logger just
// loses that sink. A logging setup must never be the reason an app will not
// start.
func Setup(o Options) (*slog.Logger, func() error) {
	app := o.App
	if app == "" {
		app = "tie"
	}

	h := &handler{app: app, mu: &sync.Mutex{}, min: slog.LevelInfo}
	if o.Verbose {
		h.min = slog.LevelDebug
	}

	var file *os.File
	if !o.NoFile {
		dir := o.Dir
		if dir == "" {
			if d, err := StateDir(); err == nil {
				dir = d
			}
		}
		if dir != "" {
			// Best effort: a read-only or missing state directory costs the
			// file sink and nothing else.
			if err := os.MkdirAll(dir, 0o700); err == nil {
				path := filepath.Join(dir, app+".log")
				rotate(path)
				if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
					file = f
					h.writers = append(h.writers, f)
				}
			}
		}
	}
	if !o.Quiet {
		console := o.Stderr
		if console == nil {
			console = os.Stderr
		}
		h.writers = append(h.writers, console)
	}
	if h.writers == nil {
		h.writers = append(h.writers, io.Discard)
	}
	// On Android, mirror to logcat: stderr is not reachable from a terminal
	// there, and the file is inside the app sandbox.
	if androidLog != nil {
		h.writers = append(h.writers, androidWriter{app: app})
	}

	return slog.New(h), func() error {
		if file != nil {
			return file.Close()
		}
		return nil
	}
}

// Discard is the logger libraries fall back to when the app has not given them
// one: cheap, never nil, so the call sites need no nil checks.
func Discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// Recover logs a panic from a background goroutine instead of letting it take
// the process down. Every long-lived goroutine in these apps (the gallery's
// loader workers and tile updater, mpv's event loop) is a place where a panic
// would otherwise kill the app with the message on a console a launcher user
// never sees.
//
// Use as the first deferred call:
//
//	defer tielog.Recover(log, "gallery.tileUpdater")
//
// The stack is logged because it is the only trace that survives.
func Recover(log *slog.Logger, where string) {
	r := recover()
	if r == nil {
		return
	}
	if log == nil {
		log = Discard()
	}
	log.Error("panic", "where", where, "panic", fmt.Sprint(r),
		"stack", string(debug.Stack()))
}

// rotate moves a log file that has outgrown MaxFileSize to <path>.1.
func rotate(path string) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() < MaxFileSize {
		return
	}
	_ = os.Rename(path, path+".1")
}

// androidWriter adapts the platform log to an io.Writer. The handler writes one
// record per Write, so each line becomes one logcat entry.
type androidWriter struct{ app string }

func (w androidWriter) Write(p []byte) (int, error) {
	if msg := strings.TrimRight(string(p), "\n"); msg != "" {
		androidLog(w.app, msg)
	}
	return len(p), nil
}

// handler renders one line per record and hands it to every sink. It is small
// on purpose: the apps need "what happened, in what order" in a form a person
// can read, not a queryable log.
type handler struct {
	app     string
	writers []io.Writer
	mu      *sync.Mutex
	min     slog.Level
	attrs   []slog.Attr
	group   string
}

func (h *handler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.min }

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	var b bytes.Buffer
	b.WriteString(r.Time.Format("2006-01-02 15:04:05"))
	b.WriteString(" ")
	b.WriteString(h.app)
	b.WriteString(": ")
	b.WriteString(strings.ToUpper(r.Level.String()))
	b.WriteString(" ")
	b.WriteString(r.Message)
	for _, a := range h.attrs {
		writeAttr(&b, h.group, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(&b, h.group, a)
		return true
	})
	b.WriteString("\n")

	h.mu.Lock()
	defer h.mu.Unlock()
	for _, w := range h.writers {
		if _, err := w.Write(b.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nh := *h
	nh.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &nh
}

func (h *handler) WithGroup(name string) slog.Handler {
	nh := *h
	if nh.group != "" {
		nh.group += "."
	}
	nh.group += name
	return &nh
}

func writeAttr(b *bytes.Buffer, group string, a slog.Attr) {
	if a.Equal(slog.Attr{}) {
		return
	}
	name := a.Key
	if group != "" {
		name = group + "." + name
	}
	if a.Value.Kind() == slog.KindGroup {
		for _, ga := range a.Value.Group() {
			writeAttr(b, name, ga)
		}
		return
	}
	b.WriteString(" ")
	b.WriteString(name)
	b.WriteString("=")
	s := a.Value.String()
	// Quote anything a reader would otherwise mis-split into two fields.
	if s == "" || strings.ContainsAny(s, " \t\"=") {
		fmt.Fprintf(b, "%q", s)
		return
	}
	b.WriteString(s)
}
