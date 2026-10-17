// Package httpx gives tie-gui's HTTP requests a deadline.
//
// tie's client.HTTPClientFor is the right place to get a client from — it owns
// the TLS and basic-auth handling and, since tie v0.7.0, caches one client per
// (URL, credentials, insecure) so connections are reused. What it does not set
// is any timeout on the http.Client itself. A filehost that accepts the TCP
// connection and then never answers — a half-dead tie-filehost, a captive
// portal, an NFS-backed store on a sleeping server — leaves the request parked
// forever, and with it the gallery loader worker or the ops goroutine that made
// it. The UI keeps painting; the transfer never finishes and never fails, and
// the only way out is to kill the app.
//
// The clients returned here share tie's transport, so they share its connection
// pool: adding a deadline costs nothing in handshakes.
package httpx

import (
	"net/http"
	"time"

	"github.com/uidbz/tie/client"
)

// Timeouts for the two kinds of request tie-gui makes.
const (
	// SmallTimeout bounds the fetches the interface waits on directly:
	// thumbnail reads, cover art, stat probes. These are kilobytes from a
	// server on the same network; anything slower than this is a dead
	// connection, and the tile pipeline has a retry budget that prefers a
	// fast failure over a stalled worker.
	SmallTimeout = 15 * time.Second

	// BulkTimeout bounds whole-blob downloads and uploads. Deliberately
	// generous — a multi-gigabyte archive from a home server legitimately
	// takes minutes — but finite, so a connection that dies mid-transfer
	// releases the worker instead of pinning it for the rest of the session.
	BulkTimeout = 10 * time.Minute
)

// Small returns a client for host suited to short, UI-blocking fetches.
func Small(host client.FileHost) *http.Client {
	return WithTimeout(host, SmallTimeout)
}

// Bulk returns a client for host suited to whole-blob transfers.
func Bulk(host client.FileHost) *http.Client {
	return WithTimeout(host, BulkTimeout)
}

// WithTimeout returns a client that reuses tie's cached transport for host but
// bounds the whole request — dial, headers and body — at timeout.
//
// Building the http.Client here rather than mutating tie's is deliberate:
// tie's client is shared and cached, so writing to it from several goroutines
// would race. An http.Client is a value pointing at the transport, so a fresh
// one per call is cheap and changes nothing about connection reuse.
func WithTimeout(host client.FileHost, timeout time.Duration) *http.Client {
	base := client.HTTPClientFor(host)
	if timeout <= 0 {
		return base
	}
	return &http.Client{
		Transport: base.Transport,
		Timeout:   timeout,
	}
}
