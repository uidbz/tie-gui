package tieconfig

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uidbz/tie/client"
)

// connectionTimeout bounds the settings editor's "Test connection" requests.
// The client default is a full minute, which is too long for a button the user
// is waiting on; a healthy local server answers in milliseconds.
const connectionTimeout = 10 * time.Second

// targetSummary describes the connection a collection resolves to, including
// the top-level fallbacks an entry inherits. The settings form edits raw entry
// fields, so an empty Namespace or Username can look harmless while the value
// actually used comes from elsewhere; this line shows what a client will use.
func targetSummary(cfg client.Config, key string) string {
	r := cfg.ResolveCollection(key)

	store := r.TripleStoreURL
	if r.TripleStoreFile != "" {
		store = "local file " + r.TripleStoreFile + " (TripleStoreURL ignored)"
	} else if store == "" {
		store = "(no triplestore URL)"
	}

	namespace := r.Namespace
	if namespace == "" {
		namespace = "(empty)"
	}
	collection := r.Collection
	if collection == "" {
		collection = "(empty)"
	}
	user := r.Username
	if user == "" {
		user = "(none)"
	}
	if r.Insecure {
		user += " · TLS verify off"
	}

	var hosts []string
	for _, name := range r.FileHosts {
		h := cfg.FileHosts[name]
		url := h.URL
		if url == "" {
			url = "(no URL)"
		}
		hosts = append(hosts, name+" · "+url)
	}
	hostLine := "(no filehost)"
	if len(hosts) > 0 {
		hostLine = strings.Join(hosts, ", ")
	}
	// The GUI's thumbnail/content resolution prefers a host literally named
	// "fast" when one exists, so name it here even when the collection uses a
	// different first host.
	if fast, ok := cfg.FileHosts["fast"]; ok {
		url := fast.URL
		if url == "" {
			url = "(no URL)"
		}
		hostLine += " · fast host preferred: " + url
	}

	return fmt.Sprintf("Effective: %s · namespace %q · collection %q · user %s\nFilehost: %s",
		store, namespace, collection, user, hostLine)
}

// testConnection checks the resolved triplestore and filehost for a collection
// and returns a short, user-facing report. It performs network I/O and must run
// off the UI goroutine.
func testConnection(cfg client.Config, key string) string {
	tc := client.NewTieClientFor(cfg, key)
	defer tc.Close() // no-op over HTTP; releases a local .tie handle
	if err := tc.Err(); err != nil {
		return "Triplestore: " + err.Error()
	}
	tc.SetTimeout(connectionTimeout)

	var report strings.Builder
	_, err := tc.Get(string(client.RelTags))
	switch {
	case err == nil:
		report.WriteString("Triplestore: OK")
	case errors.Is(err, client.ErrNotFound):
		// A reachable collection with no tags registry is healthy, not broken.
		report.WriteString("Triplestore: OK (no tags yet)")
	default:
		report.WriteString("Triplestore: " + err.Error())
	}

	host, err := tc.ResolveHost("")
	switch {
	case err != nil:
		report.WriteString("\nFilehost: " + err.Error())
	case host.URL == "":
		report.WriteString("\nFilehost: no URL configured")
	default:
		if _, err := client.FileHostVersion(host); err != nil {
			report.WriteString("\nFilehost: " + err.Error())
		} else {
			report.WriteString("\nFilehost: OK")
		}
	}
	return report.String()
}
