// Package mpris exposes tie-audio's player on the D-Bus session bus as an
// MPRIS 2 media player (https://specifications.freedesktop.org/mpris-spec/),
// so desktop-wide controls reach it: media keys handled by the desktop,
// `playerctl`, status bars (waybar's mpris module), KDE Connect, and a window
// manager binding such as sway's
//
//	bindsym XF86AudioPlay exec playerctl --player=tie_audio play-pause
//
// or, without playerctl installed, tie-audio's own client mode
// (`tie-audio -control play-pause`, see Control).
//
// The package has no Fyne dependency: the UI hands it a Controller for the
// incoming method calls and pushes playback state with Server.Update. On
// platforms without a session bus (Android, macOS, Windows) Start returns
// ErrUnsupported and the UI carries on without it.
package mpris

import "errors"

// BusName is the well-known name tie-audio claims (a second instance gets a
// ".instance<pid>" suffix, as the spec recommends). playerctl addresses it as
// "tie_audio".
const BusName = "org.mpris.MediaPlayer2.tie_audio"

// ErrUnsupported is returned by Start and Control where there is no D-Bus
// session bus to use.
var ErrUnsupported = errors.New("mpris: not supported on this platform")

// ErrNotRunning is returned by Control when no tie-audio instance owns an
// MPRIS name on the session bus.
var ErrNotRunning = errors.New("mpris: no running tie-audio instance found")

// Controller receives the player actions MPRIS clients request. Calls arrive
// on D-Bus goroutines, so implementations must be goroutine-safe (and must
// marshal any widget work onto the UI goroutine).
type Controller interface {
	PlayPause()
	Play()
	Pause()
	Stop()
	Next()
	Previous()
	// SeekTo jumps to an absolute position in seconds within the current
	// track.
	SeekTo(seconds float64)
	// SetVolume sets the output volume; 1.0 is 100 % (MPRIS allows more).
	SetVolume(v float64)
	// Raise brings the window to the front.
	Raise()
	// Quit closes the application.
	Quit()
}

// State is a playback snapshot, pushed by the UI after every status poll.
type State struct {
	Playing  bool
	Stopped  bool // stopped (or nothing queued), rather than paused
	HasTrack bool
	// TrackID identifies the current track (the content hash); it becomes
	// the MPRIS track object path.
	TrackID  string
	Title    string
	Artist   string
	Album    string
	Duration float64 // seconds; 0 when unknown
	Position float64 // seconds
	Volume   float64 // 1.0 = 100 %
	// ArtURL is a file:// URL of the current album cover, "" for none.
	ArtURL    string
	CanNext   bool
	CanPrev   bool
	RepeatAll bool
}

// Action names accepted by Control (and tie-audio's -control flag).
var Actions = []string{"play-pause", "play", "pause", "stop", "next", "previous"}

// methodForAction maps a Control action to its MPRIS Player method.
func methodForAction(action string) (string, bool) {
	switch action {
	case "play-pause", "toggle":
		return "PlayPause", true
	case "play":
		return "Play", true
	case "pause":
		return "Pause", true
	case "stop":
		return "Stop", true
	case "next":
		return "Next", true
	case "previous", "prev":
		return "Previous", true
	}
	return "", false
}
