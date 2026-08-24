package mpvplayer

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// videoController is the playback surface the Video widget drives. mpvPlayer
// satisfies it, but the widget stays decoupled from libmpv specifics.
type videoController interface {
	canvas.GLVideoRenderer
	SetOnUpdate(func())
	Play()
	Pause()
	TogglePause() bool
	IsPaused() bool
	Position() float64
	Duration() float64
	SeekTo(float64)
	Close()
}

// Video is a reusable widget that plays a video with a play/pause button and a
// seek bar. The frame is rendered by a videoController (e.g. libmpv via the
// OpenGL Render API) into a canvas.GLVideo, so it works without any native
// window embedding - including on Wayland.
type Video struct {
	widget.BaseWidget

	player   videoController
	video    *canvas.GLVideo
	playBtn  *widget.Button
	fsBtn    *widget.Button
	seek     *widget.Slider
	timeLbl  *widget.Label
	controls *fyne.Container

	fullscreen bool // true while the owning window is fullscreen; enables tap-to-toggle controls
	iconPaused bool // last play/pause icon state shown, to avoid redundant SetIcon churn

	// OnFullscreen, if set, is called when the fullscreen button is tapped. The
	// owner (gallery) drives the actual window fullscreen toggle and reports the
	// new state back via SetFullscreen.
	OnFullscreen func()

	stop         chan struct{}
	seeking      bool          // true while the user drags the slider, to suppress feedback
	settingSeek  bool          // true while the ticker programmatically updates the slider
	frameReady   chan struct{} // signals when a new frame is ready for display
}

// NewVideo returns a Video widget backed by the given controller.
func NewVideo(player videoController) *Video {
	v := &Video{
		player:     player,
		stop:       make(chan struct{}),
		frameReady: make(chan struct{}, 1), // buffered to coalesce signals
	}
	v.ExtendBaseWidget(v)

	v.video = canvas.NewGLVideo(player)
	v.video.SetMinSize(fyne.NewSize(320, 180))

	v.playBtn = widget.NewButtonWithIcon("", theme.MediaPauseIcon(), v.togglePlay)
	v.fsBtn = widget.NewButtonWithIcon("", theme.ViewFullScreenIcon(), func() {
		if v.OnFullscreen != nil {
			v.OnFullscreen()
		}
	})
	v.timeLbl = widget.NewLabel("0:00 / 0:00")

	v.seek = widget.NewSlider(0, 1)
	v.seek.Step = 0.001
	// SetValue fires OnChanged and OnChangeEnded just like a user interaction,
	// so the ticker's programmatic slider updates would otherwise seek the video
	// to the (rounded) current position every time the slider ticks up a step -
	// once every Step*duration seconds (~7s for a 2h file), causing a periodic
	// stall. settingSeek lets the callbacks ignore those self-inflicted updates.
	v.seek.OnChangeEnded = func(val float64) {
		if v.settingSeek {
			return
		}
		if dur := v.player.Duration(); dur > 0 {
			v.player.SeekTo(val * dur)
		}
		v.seeking = false
	}
	v.seek.OnChanged = func(float64) {
		if v.settingSeek {
			return
		}
		v.seeking = true
	}

	// Signal when a new frame is ready. Multiple signals are coalesced by the
	// buffered channel, preventing UI thread overload.
	player.SetOnUpdate(func() {
		select {
		case v.frameReady <- struct{}{}:
		default:
			// Channel full means a refresh is already pending, skip
		}
	})

	go v.tick()
	go v.refreshLoop()
	return v
}

// refreshLoop waits for frame-ready signals and triggers redraws on the UI
// thread. By using a dedicated goroutine and buffered channel, we coalesce
// multiple rapid update notifications into single refresh calls, preventing UI
// thread saturation while ensuring every frame gets displayed.
func (v *Video) refreshLoop() {
	for {
		select {
		case <-v.stop:
			return
		case <-v.frameReady:
			// Process the frame ready signal immediately
			fyne.Do(v.video.Refresh)
		}
	}
}

func (v *Video) togglePlay() {
	v.setPlayIcon(v.player.TogglePause())
}

// setPlayIcon shows the play icon while paused (including at EOF, where tapping
// it restarts the video) and the pause icon while playing. It only touches the
// button when the state actually changes to avoid per-tick refresh churn.
func (v *Video) setPlayIcon(paused bool) {
	if paused == v.iconPaused && v.playBtn.Icon != nil {
		return
	}
	if paused {
		v.playBtn.SetIcon(theme.MediaPlayIcon())
	} else {
		v.playBtn.SetIcon(theme.MediaPauseIcon())
	}
	v.iconPaused = paused
}

// tick refreshes the seek bar and time label roughly twice a second.
func (v *Video) tick() {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-v.stop:
			return
		case <-t.C:
			pos, dur := v.player.Position(), v.player.Duration()
			paused := v.player.IsPaused()
			fyne.Do(func() {
				v.timeLbl.SetText(formatTime(pos) + " / " + formatTime(dur))
				if dur > 0 && !v.seeking {
					v.settingSeek = true
					v.seek.SetValue(pos / dur)
					v.settingSeek = false
				}
				v.setPlayIcon(paused)
			})
		}
	}
}

// Close stops the update loop and releases the player.
func (v *Video) Close() {
	select {
	case <-v.stop:
	default:
		close(v.stop)
	}
	v.player.Close()
}

func (v *Video) CreateRenderer() fyne.WidgetRenderer {
	right := container.NewHBox(v.timeLbl, v.fsBtn)
	v.controls = container.NewBorder(nil, nil, v.playBtn, right, v.seek)
	content := container.NewBorder(nil, v.controls, nil, nil, v.video)
	return widget.NewSimpleRenderer(content)
}

// Tapped toggles controls visibility while fullscreen. In windowed mode the
// controls stay put (a tap on the video surface does nothing); taps on the
// play/fullscreen buttons and seek slider are consumed by those widgets and
// never reach here.
func (v *Video) Tapped(*fyne.PointEvent) {
	if !v.fullscreen || v.controls == nil {
		return
	}
	if v.controls.Visible() {
		v.controls.Hide()
	} else {
		v.controls.Show()
	}
}

// SetFullscreen records the fullscreen state so Tapped knows whether to toggle
// controls, and updates the button icon. Leaving fullscreen always restores the
// controls so windowed mode never hides them.
func (v *Video) SetFullscreen(fs bool) {
	v.fullscreen = fs
	if fs {
		v.fsBtn.SetIcon(theme.ViewRestoreIcon())
	} else {
		v.fsBtn.SetIcon(theme.ViewFullScreenIcon())
		if v.controls != nil {
			v.controls.Show()
		}
	}
}

// TogglePlay flips play/pause; exported so callers (e.g. a spacebar hotkey) can
// drive playback without a pointer event.
func (v *Video) TogglePlay() {
	v.togglePlay()
}

func formatTime(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	total := int(seconds)
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}
