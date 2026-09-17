// Package data holds the live tie + pwplay clients and (later) the album/track
// query layer that maps tie metadata onto the browser and playback UI.
package data

import (
	"sync"

	pwclient "github.com/uidbz/pwplay/client"
	tieclient "github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/playback"
	"github.com/uidbz/tie-gui/tieconfig"
)

// Session bundles the app config with the live tie and pwplay clients plus the
// playback backend they drive. It is rebuilt whenever the user changes
// settings.
type Session struct {
	Cfg     config.AppConfig
	Tie     *tieclient.TieClient
	Pwplay  *pwclient.Client
	Backend playback.PlaybackBackend
	// Collection is the [Collections] entry name the tie client is bound to
	// (the app's own profile selection), tracked so the connection editor can
	// preselect it.
	Collection string
	// BackendErr records a failed local-backend construction (the session
	// fell back to the pwplay remote); nil when the configured backend is
	// in use.
	BackendErr error

	// archives caches resolved audio-archive track lists, keyed by
	// "archiveHash@hostURL" (see archiveTracks). Guarded by archivesMu:
	// album loading runs on background goroutines.
	archivesMu sync.Mutex
	archives   map[string][]Track
}

// NewSession constructs the tie and pwplay clients and the playback backend
// from the given app config. The tie client binds the app's own collection
// selection (Cfg.TieCollection, preferring DefaultTieCollection when unset)
// rather than the tie config's shared DefaultCollection.
//
// The backend follows Cfg.Backend: the pwplay remote, or the on-device
// engine (pwplay player) for BackendLocal. When local construction fails the
// session falls back to the remote so the app stays usable, and BackendErr
// records the failure for the UI to surface.
func NewSession(cfg config.AppConfig) *Session {
	tieCfg := config.LoadTieConfig(cfg.TieConfig)
	collection := tieconfig.AppCollection(tieCfg, cfg.TieCollection, config.DefaultTieCollection)
	pw := pwclient.New(cfg.PwplayServer)
	s := &Session{
		Cfg:        cfg,
		Tie:        tieclient.NewTieClientFor(tieCfg, collection),
		Pwplay:     pw,
		Backend:    playback.NewPwplayRemote(pw),
		Collection: collection,
	}
	backend := cfg.Backend
	if backend == "" {
		backend = config.DefaultBackend()
	}
	if backend == config.BackendLocal {
		if lb, err := playback.NewLocal(); err == nil {
			s.Backend = lb
		} else {
			s.BackendErr = err
		}
	}
	return s
}

// PingPwplay verifies the pwplay-server is reachable by requesting its status.
func (s *Session) PingPwplay() error {
	_, err := s.Pwplay.Status()
	return err
}

// SetCollection binds the live tie client to the named collection of its
// current config. The swap overwrites the struct in place (the tie-view
// settings pattern: the resolved collection is baked into the client's
// private fields at construction), so every existing *TieClient holder —
// browse page, queue page — sees the new collection without re-wiring
// pointers.
func (s *Session) SetCollection(name string) {
	*s.Tie = *tieclient.NewTieClientFor(s.Tie.Config, name)
	s.Collection = name
}

// SetTieConfig swaps the live tie client for one built from cfg, bound to
// cfg's default collection (the collection the connection editor applied).
// Same in-place overwrite pattern as SetCollection.
func (s *Session) SetTieConfig(cfg tieclient.Config) {
	*s.Tie = *tieclient.NewTieClientFor(cfg, cfg.DefaultCollection)
	s.Collection = cfg.DefaultCollection
}
