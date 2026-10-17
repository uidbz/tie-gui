package appboot

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/tielog"
)

func sampleConfig() client.Config {
	cfg := client.Config{
		TripleStoreURL:   "https://tie.example:2161",
		Namespace:        "Pics",
		Collection:       "photos",
		DefaultFileHosts: []string{"public", "leaky", "basic"},
		FileHosts: map[string]client.FileHost{
			"public": {URL: "http://fh.example:2160", Store: "photos"},
			"leaky":  {URL: "http://media:sekrit@fh.example:2161"},
			"basic":  {URL: "https://fh.example", Username: "u", Password: "hunter2", Insecure: true},
		},
		// A named profile, which is what the GUI apps actually bind: the
		// per-app default collection.
		Collections: map[string]client.CollectionEntry{
			"images": {
				Namespace:  "Pics",
				Collection: "photos",
				FileHosts:  []string{"public", "leaky", "basic"},
			},
		},
		DefaultCollection: "images",
	}
	return cfg
}

// -conf-show is the debugging command for "which store am I actually talking
// to", so it has to print everything that decides the answer - and must not
// turn the config's credentials into a paste-able secret.
func TestShowPrintsTheResolvedTripleWithoutSecrets(t *testing.T) {
	var buf bytes.Buffer
	Show(&buf, "tie-view", "/home/u/.config/tie/config.toml", sampleConfig(), "images")
	out := buf.String()

	for _, want := range []string{
		"app:         tie-view",
		"config:      /home/u/.config/tie/config.toml",
		"triplestore: https://tie.example:2161",
		"namespace:   Pics",
		"store:       photos",
		"http://fh.example:2160",
		"store: photos",
		"[!] URL carries credentials",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	for _, secret := range []string{"sekrit", "hunter2"} {
		if strings.Contains(out, secret) {
			t.Errorf("the output leaks the credential %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "media:****@") {
		t.Errorf("the URL credential is not shown as masked:\n%s", out)
	}
}

// A filehost named by the collection but not defined in the config is the most
// common misconfiguration; the output has to say so rather than print nothing.
func TestShowFlagsUndefinedFilehosts(t *testing.T) {
	cfg := sampleConfig()
	cfg.Collections["images"] = client.CollectionEntry{
		Namespace:  "Pics",
		Collection: "photos",
		FileHosts:  []string{"public", "ghost"},
	}
	var buf bytes.Buffer
	Show(&buf, "tie-fm", "/cfg", cfg, "images")
	if out := buf.String(); !strings.Contains(out, "ghost: NOT DEFINED") {
		t.Errorf("an undefined filehost was not reported:\n%s", out)
	}
}

// A stored selection that no longer names an entry is used as a bare collection
// id, which is a silent change of target unless the output says so.
func TestShowFlagsUnknownCollection(t *testing.T) {
	var buf bytes.Buffer
	Show(&buf, "tie-fm", "/cfg", sampleConfig(), "typo")
	if out := buf.String(); !strings.Contains(out, "no such [Collections] entry") {
		t.Errorf("an unknown collection name was not called out:\n%s", out)
	}
}

func TestShowWithoutAnyCollection(t *testing.T) {
	cfg := sampleConfig()
	cfg.Collections = nil
	cfg.DefaultCollection = ""
	cfg.DefaultFileHosts = []string{"ghost"}
	var buf bytes.Buffer
	Show(&buf, "tie-fm", "/cfg", cfg, "")
	out := buf.String()
	if !strings.Contains(out, "(none: the flat top-level fields)") {
		t.Errorf("no collection should be reported as such:\n%s", out)
	}
	if !strings.Contains(out, "ghost: NOT DEFINED") {
		t.Errorf("the flat DefaultFileHosts were not used:\n%s", out)
	}
}

func TestRedactUserinfo(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://fh.example:2160", "http://fh.example:2160"},
		{"http://u:p@fh.example", "http://u:****@fh.example"},
		{"http://u:p@fh.example/deep/path", "http://u:****@fh.example/deep/path"},
		{"http://u@fh.example", "http://u@fh.example"},
		{"not a url", "not a url"},
	}
	for _, c := range cases {
		if got := redactUserinfo(c.in); got != c.want {
			t.Errorf("redactUserinfo(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMaskHidesContent(t *testing.T) {
	if got := mask("hunter2"); got != "h*****2" {
		t.Errorf("mask = %q, want h*****2", got)
	}
	for _, s := range []string{"", "a", "ab"} {
		if got := mask(s); got != "****" {
			t.Errorf("mask(%q) = %q, want ****", s, got)
		}
	}
}

// An app killed mid-download leaves its partial blob in os.TempDir forever.
// These files are gigabytes, so a startup sweep is worth having - and it must
// not touch a running instance's in-progress file or anything else in /tmp.
func TestSweepTempRemovesOnlyStaleMatches(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	stale := time.Now().Add(-DefaultTempAge - time.Hour)
	fresh := time.Now().Add(-time.Minute)

	type fixture struct {
		name string
		mod  time.Time
		size int
	}
	fixtures := []fixture{
		{"imgview-tie-vid-abc", stale, 4096},  // ours, old: gone
		{"imgview-vid-def", stale, 2048},      // ours, old: gone
		{"imgview-vid-live", fresh, 1024},     // ours, recent: kept
		{"imgview-config-backup", stale, 512}, // ours by name, wrong prefix: kept
		{"tie-fm-extract-old", stale, 256},    // another app's: kept
		{"unrelated-stale-file", stale, 128},  // nobody's: kept
	}
	for _, f := range fixtures {
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, bytes.Repeat([]byte("x"), f.size), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, f.mod, f.mod); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	opts := Options{App: "tie-view"}
	removed, bytes := SweepTemp(log, opts.App, opts.TempPrefixes(), DefaultTempAge)

	if removed != 2 || bytes != 4096+2048 {
		t.Errorf("sweep removed %d entries / %d bytes, want 2 / 6144\nlog:\n%s",
			removed, bytes, buf.String())
	}
	for _, keep := range []string{"imgview-vid-live", "imgview-config-backup",
		"tie-fm-extract-old", "unrelated-stale-file"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s was removed but should have survived: %v", keep, err)
		}
	}
	for _, gone := range []string{"imgview-tie-vid-abc", "imgview-vid-def"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s survived the sweep", gone)
		}
	}
	if !strings.Contains(buf.String(), "removed stale scratch files") {
		t.Errorf("the sweep did not report what it did:\n%s", buf.String())
	}
}

// A staged directory (MTP, archive extraction, materialized tie blobs) is
// removed whole, and its size counts toward what the sweep reports.
func TestSweepTempRemovesStaleDirectories(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	staged := filepath.Join(dir, "tie-fm-xyz")
	if err := os.MkdirAll(filepath.Join(staged, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "sub", "blob"), make([]byte, 3000), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-DefaultTempAge - time.Hour)
	if err := os.Chtimes(staged, old, old); err != nil {
		t.Fatal(err)
	}

	removed, size := SweepTemp(tielog.Discard(), "tie-fm",
		Options{App: "tie-fm"}.TempPrefixes(), DefaultTempAge)
	if removed != 1 || size != 3000 {
		t.Errorf("sweep = %d entries / %d bytes, want 1 / 3000", removed, size)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Error("the staged directory survived")
	}
}

func TestTempPrefixesDefaultToTheAppName(t *testing.T) {
	got := Options{App: "something-new"}.TempPrefixes()
	if len(got) != 1 || got[0] != "something-new-" {
		t.Errorf("TempPrefixes = %v, want [something-new-]", got)
	}
	// tie-view reuses imgview's video prefix, so it must sweep both.
	both := Options{App: "tie-view"}.TempPrefixes()
	if len(both) != 2 {
		t.Errorf("tie-view prefixes = %v, want both video prefixes", both)
	}
}
