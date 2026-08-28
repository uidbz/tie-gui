package ui

import (
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-audio-player/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio-player/internal/playback"
)

// pollInterval is how often the transport bar polls the backend. pwplay has no
// push events, so the bar reflects server state via periodic Status() calls.
const pollInterval = 500 * time.Millisecond

// transportBar is the persistent playback controller shown at the bottom of
// every view. It drives the backend on button presses and reflects the
// server's state through a Status() poll ticker. It is embedded into each
// window content by the shell wrapper (see app.go) so it survives the gallery's
// own SetContent navigation.
type transportBar struct {
	backend playback.PlaybackBackend

	object *fyne.Container

	prevBtn  *widget.Button
	playBtn  *widget.Button
	nextBtn  *widget.Button
	stopBtn  *widget.Button
	seek     *widget.Slider
	volume   *widget.Slider
	posLabel *widget.Label
	durLabel *widget.Label
	nowLabel *widget.Label

	mu    sync.Mutex
	metas map[string]data.Track // queue URL → track metadata; survives reorder/shuffle
	// resolver re-derives metadata for a queue URL the registry doesn't know,
	// used to re-label a queue that outlived this app's memory (restart, while
	// pwplay still holds the queue). resolving guards against re-fetching a URL
	// that's already in flight or known-unresolvable.
	resolver  func(url string) (data.Track, bool)
	resolving map[string]bool
	// listener, when set, receives every status snapshot on the UI goroutine.
	// The queue view subscribes to stay live off the same poll as the bar.
	listener func(playback.Status)
	playing  bool     // last observed play state, for the play/pause toggle
	seeking  bool     // true while the user drags the seek slider
	adjVol   bool     // true while the user drags the volume slider
	// applying is true while apply() is pushing server state into the sliders.
	// Fyne's Slider.SetValue fires OnChangeEnded, so without this guard every
	// poll's SetValue would echo back a Seek/SetVolume to the server — and each
	// Seek clears pwplay's ring buffer, chopping the audio twice a second.
	applying bool
	pendVol  *float64 // volume just set locally, awaiting server confirmation
	// pendSeek holds a just-released seek target awaiting server confirmation.
	// Like pendVol, this prevents a poll that lands before the Seek propagates
	// from snapping the thumb back to the stale position and then forward again.
	// pendSeekTTL bounds the wait so a target the server never quite reports
	// (e.g. clamped near end-of-track) can't pin the thumb forever.
	pendSeek    *float64
	pendSeekTTL int
	repeatAll   bool // when true, restart the queue from the top after it ends
	repeatFired bool // guards a single restart per end-of-queue event
	stopOnce sync.Once
	stopCh   chan struct{}
}

// newTransportBar builds the transport controls bound to the given backend.
// Call Start to begin polling.
func newTransportBar(backend playback.PlaybackBackend) *transportBar {
	t := &transportBar{backend: backend, metas: map[string]data.Track{}, resolving: map[string]bool{}, stopCh: make(chan struct{})}

	t.prevBtn = widget.NewButtonWithIcon("", theme.MediaSkipPreviousIcon(), func() { t.do(backend.Previous) })
	t.playBtn = widget.NewButtonWithIcon("", theme.MediaPlayIcon(), t.togglePlay)
	t.nextBtn = widget.NewButtonWithIcon("", theme.MediaSkipNextIcon(), func() { t.do(backend.Next) })
	t.stopBtn = widget.NewButtonWithIcon("", theme.MediaStopIcon(), func() { t.do(backend.Stop) })

	t.seek = widget.NewSlider(0, 1)
	t.seek.Step = 0.1
	t.seek.OnChanged = func(float64) {
		t.mu.Lock()
		if !t.applying {
			t.seeking = true
		}
		t.mu.Unlock()
	}
	t.seek.OnChangeEnded = func(v float64) {
		t.mu.Lock()
		applying := t.applying
		if !applying {
			t.seeking = false
			// Hold the target until a poll confirms the server has seeked;
			// otherwise a poll landing before Seek propagates would snap the
			// thumb back to the stale position and then jump forward again.
			vv := v
			t.pendSeek = &vv
			t.pendSeekTTL = 4 // ~2s at the 500ms poll interval
		}
		t.mu.Unlock()
		if applying {
			return
		}
		go func() { _ = t.backend.Seek(v) }()
	}

	t.volume = widget.NewSlider(0, 2)
	t.volume.Step = 0.01
	t.volume.SetValue(1)
	t.volume.OnChanged = func(float64) {
		t.mu.Lock()
		if !t.applying {
			t.adjVol = true
		}
		t.mu.Unlock()
	}
	t.volume.OnChangeEnded = func(v float64) {
		t.mu.Lock()
		applying := t.applying
		if !applying {
			t.adjVol = false
			// Hold the just-set value until a poll confirms the server agrees;
			// otherwise a poll landing before SetVolume propagates would snap the
			// thumb back to the stale server volume.
			vv := v
			t.pendVol = &vv
		}
		t.mu.Unlock()
		if applying {
			return
		}
		go func() { _ = t.backend.SetVolume(v) }()
	}

	t.posLabel = widget.NewLabel("0:00")
	t.durLabel = widget.NewLabel("0:00")
	t.nowLabel = widget.NewLabel("Nothing playing")

	buttons := container.NewHBox(t.prevBtn, t.playBtn, t.nextBtn, t.stopBtn)
	volBox := container.NewCenter(container.NewHBox(
		widget.NewIcon(theme.VolumeUpIcon()),
		container.NewGridWrap(fyne.NewSize(140, 28), t.volume),
	))
	progress := container.NewBorder(nil, nil, t.posLabel, t.durLabel, t.seek)
	center := container.NewVBox(t.nowLabel, progress)

	t.object = container.NewBorder(widget.NewSeparator(), nil, buttons, volBox, center)
	return t
}

// Object returns the bar's root object for embedding in the window content.
func (t *transportBar) Object() fyne.CanvasObject { return t.object }

// SetQueue replaces the track registry; call it when the queue is replaced
// (PlayAlbum). Keying by stream URL (not by index) means the mapping survives
// reorder and shuffle, which permute the server playlist.
func (t *transportBar) SetQueue(urls []string, meta []data.Track) {
	t.mu.Lock()
	t.metas = map[string]data.Track{}
	t.resolving = map[string]bool{}
	for i, u := range urls {
		if i < len(meta) {
			t.metas[u] = meta[i]
		}
	}
	t.mu.Unlock()
}

// SetResolver installs a callback that re-derives metadata for a queue URL the
// registry doesn't know (see resolver). Called once at startup.
func (t *transportBar) SetResolver(fn func(url string) (data.Track, bool)) {
	t.mu.Lock()
	t.resolver = fn
	t.mu.Unlock()
}

// AppendQueue extends the track registry; call it when tracks are enqueued.
func (t *transportBar) AppendQueue(urls []string, meta []data.Track) {
	t.mu.Lock()
	for i, u := range urls {
		if i < len(meta) {
			t.metas[u] = meta[i]
		}
	}
	t.mu.Unlock()
}

// Label resolves a queue URL to its display title, or "" if unknown.
func (t *transportBar) Label(url string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if m, ok := t.metas[url]; ok {
		return m.Display()
	}
	return ""
}

// TrackMeta returns the registered track metadata for a queue URL, if known.
func (t *transportBar) TrackMeta(url string) (data.Track, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	m, ok := t.metas[url]
	return m, ok
}

// SetStatusListener registers (or clears, with nil) a callback invoked with each
// status snapshot on the UI goroutine, so a view can stay live off the bar's
// existing poll instead of running its own ticker.
func (t *transportBar) SetStatusListener(fn func(playback.Status)) {
	t.mu.Lock()
	t.listener = fn
	t.mu.Unlock()
}

// SetRepeat enables or disables repeat-all (restart the queue after it ends).
func (t *transportBar) SetRepeat(on bool) {
	t.mu.Lock()
	t.repeatAll = on
	t.mu.Unlock()
}

// RepeatAll reports whether repeat-all is enabled.
func (t *transportBar) RepeatAll() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.repeatAll
}

// togglePlay pauses when playing, otherwise resumes.
func (t *transportBar) togglePlay() {
	t.mu.Lock()
	playing := t.playing
	t.mu.Unlock()
	if playing {
		t.do(t.backend.Pause)
	} else {
		t.do(t.backend.Play)
	}
}

// do runs a backend action off the UI goroutine so the click returns instantly.
func (t *transportBar) do(fn func() error) {
	go func() { _ = fn() }()
}

// Start launches the poll loop. Stop ends it.
func (t *transportBar) Start() {
	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-t.stopCh:
				return
			case <-ticker.C:
				s, err := t.backend.Status()
				if err != nil {
					continue
				}
				fyne.Do(func() { t.apply(s) })
			}
		}
	}()
}

// Stop ends the poll loop; safe to call more than once.
func (t *transportBar) Stop() {
	t.stopOnce.Do(func() { close(t.stopCh) })
}

// apply updates the widgets from a status snapshot. It runs on the UI goroutine.
func (t *transportBar) apply(s playback.Status) {
	t.mu.Lock()
	t.playing = s.Playing
	seeking, adjVol := t.seeking, t.adjVol
	pendVol := t.pendVol
	if pendVol != nil && absDiff(s.Volume, *pendVol) < 0.02 {
		// Server now reflects our local change; stop holding.
		t.pendVol = nil
		pendVol = nil
	}
	pendSeek := t.pendSeek
	if pendSeek != nil {
		if t.pendSeekTTL > 0 {
			t.pendSeekTTL--
		}
		// Clear once the server position is near the target (allowing for
		// playback advancing since the seek) or the wait times out.
		if absDiff(s.Position, *pendSeek) < 1.0 || t.pendSeekTTL == 0 {
			t.pendSeek = nil
			pendSeek = nil
		}
	}
	now := t.nowPlaying(s)
	listener := t.listener
	var missing []string
	if t.resolver != nil {
		for _, u := range s.Playlist {
			if _, ok := t.metas[u]; !ok && !t.resolving[u] {
				t.resolving[u] = true
				missing = append(missing, u)
			}
		}
	}
	t.mu.Unlock()

	if len(missing) > 0 {
		go t.resolveMissing(missing)
	}

	if listener != nil {
		listener(s)
	}

	if s.Playing {
		t.playBtn.SetIcon(theme.MediaPauseIcon())
	} else {
		t.playBtn.SetIcon(theme.MediaPlayIcon())
	}

	t.nowLabel.SetText(now)

	// Mark programmatic slider updates so the sliders' OnChangeEnded handlers
	// don't echo a Seek/SetVolume back to the server. Set outside t.mu because
	// SetValue invokes those handlers synchronously and they take t.mu.
	t.mu.Lock()
	t.applying = true
	t.mu.Unlock()

	if !seeking {
		// While a seek is pending confirmation, pin the thumb/label to the
		// target so a stale poll doesn't bounce it to the old position.
		pos := s.Position
		if pendSeek != nil {
			pos = *pendSeek
		}
		if s.TrackDuration > 0 {
			t.seek.Max = s.TrackDuration
			t.seek.SetValue(pos)
		} else {
			t.seek.Max = 1
			t.seek.SetValue(0)
		}
		t.posLabel.SetText(formatDuration(pos))
		t.durLabel.SetText(formatDuration(s.TrackDuration))
	}
	if !adjVol && pendVol == nil {
		t.volume.SetValue(s.Volume)
	}

	t.mu.Lock()
	t.applying = false
	t.mu.Unlock()

	t.maybeRepeat(s)
}

// maybeRepeat restarts the queue from the top when repeat-all is on and the last
// track has just finished. End-of-queue is detected as: stopped, on the last
// track, with the position at (near) the track's end. This is distinguishable
// from a user Stop, which now rewinds the position to 0. repeatFired guards a
// single restart until playback is observed again.
func (t *transportBar) maybeRepeat(s playback.Status) {
	t.mu.Lock()
	repeat := t.repeatAll
	fired := t.repeatFired
	atEnd := s.Stopped && s.TotalTracks > 0 &&
		s.CurrentTrack == s.TotalTracks-1 &&
		s.TrackDuration > 0 && s.Position >= s.TrackDuration-0.75
	switch {
	case repeat && atEnd && !fired:
		t.repeatFired = true
	case s.Playing:
		t.repeatFired = false
	}
	restart := repeat && atEnd && !fired
	t.mu.Unlock()

	if restart {
		go func() { _ = t.backend.Play() }()
	}
}

// absDiff returns the absolute difference between two floats.
func absDiff(a, b float64) float64 {
	if a < b {
		return b - a
	}
	return a - b
}

// nowPlaying resolves the current track's label, preferring the queued title
// and falling back to the backend's file identifier. Caller holds t.mu.
// resolveMissing resolves queue URLs the registry doesn't know (off the UI
// goroutine, since the resolver hits tie over the network) and stores what it
// finds. URLs that don't resolve stay marked in `resolving` so they aren't
// retried every poll. The next status poll re-labels the now-playing text and
// the queue rows from the freshly populated registry.
func (t *transportBar) resolveMissing(urls []string) {
	t.mu.Lock()
	resolver := t.resolver
	t.mu.Unlock()
	if resolver == nil {
		return
	}
	found := map[string]data.Track{}
	for _, u := range urls {
		if trk, ok := resolver(u); ok {
			found[u] = trk
		}
	}
	if len(found) == 0 {
		return
	}
	t.mu.Lock()
	for u, trk := range found {
		t.metas[u] = trk
	}
	t.mu.Unlock()
}

func (t *transportBar) nowPlaying(s playback.Status) string {
	if s.CurrentTrack >= 0 && s.CurrentTrack < len(s.Playlist) {
		if m, ok := t.metas[s.Playlist[s.CurrentTrack]]; ok {
			if label := m.Display(); label != "" {
				return label
			}
		}
	}
	if s.TotalTracks == 0 {
		return "Nothing playing"
	}
	if s.CurrentFile != "" {
		return s.CurrentFile
	}
	return "Nothing playing"
}

// formatDuration renders seconds as m:ss (or h:mm:ss past an hour).
func formatDuration(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	total := int(sec + 0.5)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
