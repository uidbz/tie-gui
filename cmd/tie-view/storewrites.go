package main

import (
	"strconv"
	"sync"

	"github.com/uidbz/tie/client"
)

// storeWrites serializes tie triple mutations (tag toggles, rating writes,
// tag registration) issued from the UI. Both the quick tag bar and the image
// tagger write the SAME (hash,"tag",*) / (hash,"rating",*) subjects, and each
// used to fire a fresh goroutine per click: two fast taps could reach the
// store out of order (the optimistic UI applied add→remove while the network
// wrote remove→add, leaving the tag stuck on), and the two widgets could
// race each other on one image.
//
// Queueing keeps UI dispatch order == store write order. A queue entry runs
// its whole compound operation (e.g. write + register, or write + reconcile
// fetch) before the next starts, so those pairs stay atomic with respect to
// each other too. Clients are snapshotted INSIDE the entry (ref.get()) so a
// collection switch mid-queue sends pending intent to the live client rather
// than a retired one.
var storeWrites writeQueue

type writeQueue struct {
	mu     sync.Mutex
	queue  []func()
	worker bool
}

// run enqueues fn behind everything already queued.
func (q *writeQueue) run(fn func()) {
	q.mu.Lock()
	q.queue = append(q.queue, fn)
	if q.worker {
		q.mu.Unlock()
		return
	}
	q.worker = true
	q.mu.Unlock()
	go func() {
		for {
			q.mu.Lock()
			if len(q.queue) == 0 {
				q.worker = false
				q.mu.Unlock()
				return
			}
			f := q.queue[0]
			q.queue = q.queue[1:]
			q.mu.Unlock()
			f()
		}
	}()
}

// writeRating stores a 0-5 rating (0 clears) as ONE Set replacement: the
// Delete(old)+Add(new) pair issued from per-click goroutines could land out
// of order and lose or double the rating.
func writeRating(tc *client.TieClient, hash string, rating int) error {
	if rating == 0 {
		return tc.Set(hash, "rating", nil)
	}
	return tc.Set(hash, "rating", []string{strconv.Itoa(rating)})
}
