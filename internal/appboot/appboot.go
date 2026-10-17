// Package appboot is the startup sequence the tie-gui apps share: logging, the
// tie client config, signal handling, and the sweep of scratch files an earlier
// killed run left behind.
//
// It deliberately does not import mpvplayer: that package needs libmpv headers,
// and tie-audio builds without them. Apps that play video call
// mpvplayer.SetLogger themselves.
package appboot

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/tieconfig"
	"github.com/uidbz/tie-gui/tielog"
)

// Options configures Start.
type Options struct {
	// App is the binary name. It names the log file, the stale-temp prefixes
	// and the app's default [Collections] profile.
	App string

	// Log carries the -v / -q / -no-log values registered by AddFlags.
	Log tielog.Options

	// ConfigName is the app's -config/-c value; "" means the default.
	ConfigName string

	// ShowConfig prints the resolved configuration and exits 0.
	ShowConfig bool

	// Collection resolves which [Collections] entry this app binds. Each app
	// keeps its own stored selection, which appboot cannot know, so the app
	// supplies the resolution. -conf-show prints the result.
	Collection func(client.Config) string

	// Grace is how long a signal-initiated shutdown may take before the
	// process is killed. Zero turns signal handling off.
	Grace time.Duration

	// CleanTemp sweeps stale scratch files from an earlier run.
	CleanTemp bool

	// Out is where -conf-show writes. Defaults to os.Stdout.
	Out io.Writer
}

// AddFlags registers the bootstrap flags on fs (normally flag.CommandLine).
// Call it before flag.Parse.
func AddFlags(fs *flag.FlagSet, o *Options) {
	o.Log.AddFlags(fs)
	fs.BoolVar(&o.ShowConfig, "conf-show", false,
		"print the resolved tie config — triplestore, filehosts, collection — and exit")
}

// Run is what Start hands back.
type Run struct {
	Opts       Options
	Log        *slog.Logger
	TieConfig  client.Config
	ConfigPath string
	Collection string

	// Close flushes and closes the log. Defer it in main.
	Close func() error
}

// Start sets up logging, loads the tie client config, installs the library
// loggers, sweeps stale temp files and installs the signal handler. With
// -conf-show it prints the resolved configuration and exits.
func Start(o Options) *Run {
	if o.App == "" {
		o.App = "tie"
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.Log.App == "" {
		o.Log.App = o.App
	}

	log, closeLog := tielog.Setup(o.Log)
	// The config package reports the problems that are not fatal — an
	// unreadable file falling back to defaults — so it needs the logger
	// before the first Load.
	tieconfig.SetLogger(log)

	path := tieconfig.ResolvePath(o.ConfigName)
	cfg := tieconfig.Load(o.ConfigName)

	r := &Run{
		Opts:       o,
		Log:        log,
		TieConfig:  cfg,
		ConfigPath: path,
		Close:      closeLog,
	}
	if o.Collection != nil {
		r.Collection = o.Collection(cfg)
	} else {
		r.Collection = cfg.DefaultCollection
	}

	if o.CleanTemp {
		SweepTemp(log, o.App, o.TempPrefixes(), DefaultTempAge)
	}

	if o.Grace > 0 {
		HandleSignals(log, o.Grace)
	}
	return r
}

// MaybeShowConfig prints the resolved configuration and exits 0 when
// -conf-show was given. Call it as soon as the config values are known.
func (r *Run) MaybeShowConfig() {
	r.ShowConfigFor(r.ConfigPath, r.TieConfig, r.Collection)
}

// ShowConfigFor is MaybeShowConfig for an app that loads the tie config
// itself — tie-fm keeps the path in its own settings and falls back to a
// built-in local config rather than the user's config.toml, so what it uses is
// not what Start loaded.
func (r *Run) ShowConfigFor(path string, cfg client.Config, collection string) {
	if !r.Opts.ShowConfig {
		return
	}
	Show(r.Opts.Out, r.Opts.App, path, cfg, collection)
	r.Close()
	os.Exit(0)
}

// TempPrefixes returns the os.TempDir prefixes this app's scratch files carry.
// They are listed rather than derived from App because they are already in
// users' temp directories, and tie-view shares imgview's prefix.
func (o Options) TempPrefixes() []string {
	if p, ok := tempPrefixes[o.App]; ok {
		return p
	}
	return []string{o.App + "-"}
}

// tempPrefixes maps an app to the prefixes of the scratch files it leaves in
// os.TempDir: downloaded videos, MTP and archive staging, materialized tie
// blobs. Those can be gigabytes, and an app killed mid-download never removes
// them.
var tempPrefixes = map[string][]string{
	"imgview":  {"imgview-vid-"},
	"tie-view": {"imgview-tie-vid-", "imgview-vid-"},
	"tie-fm":   {"tie-fm-"},
	// The MPRIS bridge writes the current track's cover art next to the
	// session bus's expectations, one PNG per album per run, and nothing ever
	// removes them: a long-lived player leaks tens of megabytes into /tmp.
	"tie-audio": {"tie-audio-"},
}

// DefaultTempAge is how old a scratch file has to be before a startup sweep
// treats it as abandoned. Conservative on purpose: a download from a running
// instance is never that old.
const DefaultTempAge = 24 * time.Hour

// SweepTemp removes stale scratch files and directories matching any of
// prefixes in os.TempDir, and reports how many were removed and how much space
// that freed. It never follows symlinks and never descends into a directory it
// is not removing.
func SweepTemp(log *slog.Logger, app string, prefixes []string, maxAge time.Duration) (int, int64) {
	dir := os.TempDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Warn("reading temp dir", "dir", dir, "err", err)
		return 0, 0
	}
	cutoff := time.Now().Add(-maxAge)
	removed, bytes := 0, int64(0)
	for _, e := range entries {
		name := e.Name()
		// The prefixes are the whole filter. A fast path on a common prefix
		// would be wrong: tie-view's scratch files carry imgview's prefix.
		if !matchesAny(name, prefixes) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		path := filepath.Join(dir, name)
		sz := sizeOf(path)
		if err := os.RemoveAll(path); err != nil {
			log.Warn("removing stale temp file", "path", path, "err", err)
			continue
		}
		removed++
		bytes += sz
	}
	if removed > 0 {
		log.Info("removed stale scratch files", "app", app, "count", removed,
			"megabytes", bytes/(1<<20), "dir", dir)
	}
	return removed, bytes
}

func matchesAny(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// sizeOf reports the bytes under path, symlinks not followed.
func sizeOf(path string) int64 {
	var total int64
	filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// HandleSignals is the backstop for SIGINT/SIGTERM. Fyne's own handler queues a
// Quit onto the UI goroutine, so a normal Ctrl-C shuts the app down properly —
// but if that goroutine is blocked (tie-audio once hung in the playback
// engine's Close) the signal does nothing and only SIGKILL helps. This gives
// the normal shutdown `grace` to finish, then exits; a second signal exits at
// once.
func HandleSignals(log *slog.Logger, grace time.Duration) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-ch
		log.Info("signal received, shutting down", "signal", sig.String())
		select {
		case sig = <-ch:
			log.Warn("second signal, exiting now", "signal", sig.String())
		case <-time.After(grace):
			log.Warn("shutdown did not finish in time, forcing exit", "grace", grace)
		}
		os.Exit(130)
	}()
}

// Show prints the resolved configuration. This is the single most useful
// debugging output for this project: when an app talks to the wrong store, the
// question is always which triplestore, which filehost and which collection it
// actually picked, and the answer depends on the config path, the app's stored
// selection and the fallbacks in between. Passwords are masked.
func Show(w io.Writer, app, path string, cfg client.Config, collection string) {
	fmt.Fprintf(w, "app:         %s\n", app)
	fmt.Fprintf(w, "config:      %s\n", path)
	// Mirror client.Config.ResolveCollection's own name resolution so the
	// output says which entry was really used, not which one was asked for.
	effective := collection
	if effective == "" {
		effective = cfg.DefaultCollection
	}
	if _, ok := cfg.Collections[effective]; !ok {
		if effective == "" {
			fmt.Fprintf(w, "collection:  (none: the flat top-level fields)\n")
		} else {
			fmt.Fprintf(w, "collection:  %s (no such [Collections] entry: used as a bare "+
				"collection id on the top-level triplestore)\n", effective)
		}
	} else {
		fmt.Fprintf(w, "collection:  %s\n", effective)
	}
	res := cfg.ResolveCollection(collection)
	fmt.Fprintf(w, "triplestore: %s\n", res.TripleStoreURL)
	if res.Insecure {
		fmt.Fprintf(w, "             (TLS verification off)\n")
	}
	fmt.Fprintf(w, "namespace:   %s\n", res.Namespace)
	fmt.Fprintf(w, "store:       %s\n", res.Collection)
	if res.Username != "" {
		fmt.Fprintf(w, "user:        %s\n", res.Username)
	}
	if res.Password != "" {
		fmt.Fprintf(w, "password:    %s\n", mask(res.Password))
	}

	fmt.Fprintf(w, "filehosts:\n")
	if len(res.FileHosts) == 0 {
		fmt.Fprintf(w, "  (none)\n")
	}
	for _, name := range res.FileHosts {
		host, ok := cfg.FileHosts[name]
		if !ok {
			fmt.Fprintf(w, "  %s: NOT DEFINED in the config\n", name)
			continue
		}
		note := ""
		if ui := tieconfig.HostURLUserinfo(host.URL); ui != "" {
			note = "  [!] URL carries credentials; stream URLs would leak them"
		}
		fmt.Fprintf(w, "  %s: %s\n", name, redactUserinfo(host.URL))
		if host.Store != "" {
			fmt.Fprintf(w, "      store: %s\n", host.Store)
		}
		if host.Insecure {
			fmt.Fprintf(w, "      (TLS verification off)\n")
		}
		if host.Username != "" || host.Password != "" {
			fmt.Fprintf(w, "      basic auth: %s / %s\n", host.Username, mask(host.Password))
		}
		if note != "" {
			fmt.Fprintln(w, note)
		}
	}

	if len(cfg.Collections) > 1 {
		names := make([]string, 0, len(cfg.Collections))
		for name := range cfg.Collections {
			names = append(names, name)
		}
		fmt.Fprintf(w, "other collections: %s\n", strings.Join(names, ", "))
	}
}

// redactUserinfo replaces the password in a URL's userinfo with ****, so the
// output shows that credentials are configured without repeating them.
func redactUserinfo(rawURL string) string {
	i := strings.Index(rawURL, "://")
	if i < 0 {
		return rawURL
	}
	rest := rawURL[i+3:]
	slash := strings.IndexAny(rest, "/?#")
	authority := rest
	tail := ""
	if slash >= 0 {
		authority, tail = rest[:slash], rest[slash:]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return rawURL
	}
	user := authority[:at]
	if colon := strings.Index(user, ":"); colon >= 0 {
		user = user[:colon] + ":****"
	}
	return rawURL[:i+3] + user + "@" + authority[at+1:] + tail
}

// mask shortens a secret so its length is visible but its content is not.
func mask(s string) string {
	if len(s) <= 2 {
		return "****"
	}
	return s[:1] + strings.Repeat("*", len(s)-2) + s[len(s)-1:]
}
