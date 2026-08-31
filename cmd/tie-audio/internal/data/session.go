// Package data holds the live tie + pwplay clients and (later) the album/track
// query layer that maps tie metadata onto the browser and playback UI.
package data

import (
	pwclient "github.com/uidbz/tie-gui/cmd/tie-audio/internal/pwplay/client"
	tieclient "github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/playback"
)

// Session bundles the app config with the live tie and pwplay clients plus the
// playback backend they drive. It is rebuilt whenever the user changes
// settings.
type Session struct {
	Cfg     config.AppConfig
	Tie     *tieclient.TieClient
	Pwplay  *pwclient.Client
	Backend playback.PlaybackBackend
}

// NewSession constructs the tie and pwplay clients and the playback backend
// from the given app config.
func NewSession(cfg config.AppConfig) *Session {
	tieCfg := config.LoadTieConfig(cfg.TieConfig)
	pw := pwclient.New(cfg.PwplayServer)
	return &Session{
		Cfg:     cfg,
		Tie:     tieclient.NewTieClient(tieCfg),
		Pwplay:  pw,
		Backend: playback.NewPwplayRemote(pw),
	}
}

// PingPwplay verifies the pwplay-server is reachable by requesting its status.
func (s *Session) PingPwplay() error {
	_, err := s.Pwplay.Status()
	return err
}
