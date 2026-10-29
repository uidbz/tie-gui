package main

import (
	"sync/atomic"

	"github.com/uidbz/tie/client"
)

// tieRef is the swappable handle to the live tie client. The old collection
// switch pattern — `*tieClient = *client.NewTieClientFor(...)` — overwrote
// the whole client struct in place while background goroutines (thumbnailer,
// tag reload, co-tags, star toggles, de-import, gallery fetches, reader
// instances) were mid-call through the same pointer: client.TieClient has no
// internal locking, so the swap was a data race (torn struct fields,
// concurrent Config map access) and could execute an old-collection request
// through the new client.
//
// Instead the switch installs a NEW client pointer atomically: in-flight
// operations keep using the old (still valid) client, and the gallery/tag
// generation counters drop their superseded replies. Holders that keep a
// client across calls store the *tieRef and snapshot via get() per
// operation; per-fetch callers take the snapshot at dispatch.
type tieRef struct {
	p atomic.Pointer[client.TieClient]
}

func newTieRef(tc *client.TieClient) *tieRef {
	r := &tieRef{}
	r.p.Store(tc)
	return r
}

// get snapshots the live client for one operation.
func (r *tieRef) get() *client.TieClient { return r.p.Load() }

// swap installs a newly built client (collection or connection change).
func (r *tieRef) swap(tc *client.TieClient) { r.p.Store(tc) }
