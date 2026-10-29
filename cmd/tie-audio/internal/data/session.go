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
	// album loading runs on background goroutines. archiving coalesces
	// concurrent resolutions of the same archive (single-flight).
	archivesMu sync.Mutex
	archives   map[string][]Track
	archiving  map[string]*archiveFlight

	// discUp caches disc folder → album lookups (discFolderAlbum).
	discMu sync.Mutex
	discUp map[string]string

	// thumbHints remembers each listed album's thumbnail hash (from the
	// query-expanded rows), so cover resolution skips a Get per tile.
	thumbHints sync.Map // uid → thumbHash
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
//
// Both are wrapped with playback.Serialized: the backend contract is "one
// action at a time", but the UI fires actions from fresh goroutines per
// click beside the continuous status poll — serialization keeps PlayAlbum
// settles, queue moves and polls from interleaving.
func NewSession(cfg config.AppConfig) *Session {
	tieCfg := config.LoadTieConfig(cfg.TieConfig)
	collection := tieconfig.AppCollection(tieCfg, cfg.TieCollection, config.DefaultTieCollection)
	pw := pwclient.New(cfg.PwplayServer)
	s := &Session{
		Cfg:        cfg,
		Tie:        tieclient.NewTieClientFor(tieCfg, collection),
		Pwplay:     pw,
		Backend:    playback.Serialized(playback.NewPwplayRemote(pw)),
		Collection: collection,
	}
	backend := cfg.Backend
	if backend == "" {
		backend = config.DefaultBackend()
	}
	if backend == config.BackendLocal {
		if lb, err := playback.NewLocal(config.PlayLogPath()); err == nil {
			s.Backend = playback.Serialized(lb)
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

// NOTE: there is deliberately no SetCollection/SetTieConfig here. A live
// Session is never rebound in place: that struct-overwrite pattern races with
// every background goroutine reading through the same *TieClient. Connection
// and collection changes build a NEW Session (config.AppConfig carries the
// choice) and the UI adopts it (App.applySession/adoptSession); in-flight
// operations keep using the old session and their pages drop the results.
