package main

import (
	"fmt"
	"time"

	pwclient "github.com/uidbz/pwplay/client"
)

// playNow replaces the pwplay queue with one stream and starts playback.
//
// pwplay applies playlist mutations asynchronously in its decoder loop, so a
// Play issued right after AddTracks can be swallowed (the engine is still
// stopped on an empty queue). It also keeps its queue across client restarts,
// so a plain AddTracks+Play appends to whatever was playing before. Clear,
// wait for the empty queue, add, wait for exactly the new stream, then Play.
func playNow(c *pwclient.Client, stream string) error {
	st, err := c.Status()
	if err != nil {
		return err
	}
	if st.TotalTracks > 0 {
		if err := c.ClearTracks(); err == nil {
			if err := waitPlaylist(c, nil); err != nil {
				return err
			}
			if err := c.AddTracks(stream); err != nil {
				return err
			}
			if err := waitPlaylist(c, []string{stream}); err != nil {
				return err
			}
			return c.Play()
		}
	}
	// Older servers without /clear: append then trim, never leaving the queue
	// transiently empty (which stops the engine and races Play).
	old := st.TotalTracks
	if err := c.AddTracks(stream); err != nil {
		return err
	}
	for i := 0; i < old; i++ {
		if err := c.RemoveTrack(0); err != nil {
			return err
		}
	}
	if err := waitPlaylist(c, []string{stream}); err != nil {
		return err
	}
	return c.Play()
}

const settleTimeout = 10 * time.Second

// waitPlaylist polls until the applied queue is exactly want (nil = empty).
// Matching content, not just the length, avoids firing Play into a half-applied
// mutation.
func waitPlaylist(c *pwclient.Client, want []string) error {
	deadline := time.Now().Add(settleTimeout)
	for {
		st, err := c.Status()
		if err != nil {
			return err
		}
		if playlistEquals(st.Playlist, want) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pwplay queue did not settle within %v", settleTimeout)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func playlistEquals(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
