//go:build nompv || android

package mpvplayer

import (
	"fmt"
	"image"
	"io"
)

// This file is the libmpv-free build of the package, selected by the `nompv`
// tag or on Android (which has no system libmpv). It provides the same public
// API as the real implementation so callers compile unchanged, but video
// playback and frame extraction are disabled: NewMPVPlayer returns an error and
// the ExtractFrame helpers return nil (callers already fall back to a
// placeholder thumbnail when the frame is nil).
//
// See docs/ANDROID.md for what a real libmpv-backed Android build requires.

// MPVPlayer is the no-op stand-in for the libmpv-backed player. It satisfies
// the videoController interface so the Video widget still builds.
type MPVPlayer struct{}

// NewMPVPlayer always fails in this build: there is no libmpv to drive.
func NewMPVPlayer(file string) (*MPVPlayer, error) {
	return nil, fmt.Errorf("video playback unavailable: built without libmpv support")
}

func (p *MPVPlayer) RenderInto(fbo uint32, width, height int) {}
func (p *MPVPlayer) NeedsPaint() bool                         { return false }
func (p *MPVPlayer) Aspect() float32                          { return 0 }
func (p *MPVPlayer) SetOnUpdate(fn func())                    {}
func (p *MPVPlayer) Play()                                    {}
func (p *MPVPlayer) Pause()                                   {}
func (p *MPVPlayer) TogglePause() bool                        { return true }
func (p *MPVPlayer) IsPaused() bool                           { return true }
func (p *MPVPlayer) Position() float64                        { return 0 }
func (p *MPVPlayer) Duration() float64                        { return 0 }
func (p *MPVPlayer) SeekTo(seconds float64)                   {}
func (p *MPVPlayer) Close()                                   {}

// ExtractFrame returns nil in this build; callers fall back to a placeholder.
func ExtractFrame(pathOrURL string, maxWidth, maxHeight int, timestamp float64) image.Image {
	return nil
}

// ExtractFrameFromReader returns nil in this build; callers fall back to a placeholder.
func ExtractFrameFromReader(r io.ReadSeeker, maxWidth, maxHeight int, timestamp float64) image.Image {
	return nil
}

// ExtractFramePercent returns nil in this build; callers fall back to a placeholder.
func ExtractFramePercent(pathOrURL string, maxWidth, maxHeight int, percent float64) image.Image {
	return nil
}

// ExtractFrameFromReaderPercent returns nil in this build; callers fall back to a placeholder.
func ExtractFrameFromReaderPercent(r io.ReadSeeker, maxWidth, maxHeight int, percent float64) image.Image {
	return nil
}
