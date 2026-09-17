package ui

import (
	"fmt"
	"image"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/playback"
)

// pollInterval is how often the player polls the backend. pwplay has no
// push events, so the UI reflects server state via periodic Status() calls.
const pollInterval = 500 * time.Millisecond

// transportState is a status snapshot rendered for display: everything a
// transport view needs, with the decisions about what to apply already made by
// the player (which owns the slider-echo guards).
type transportState struct {
	playing  bool
	title    string // now-playing track title, or a placeholder
	subtitle string // artist · album, empty when unknown
	position float64
	duration float64
	volume   float64
	// applyPosition is false while the user drags a seek slider, and
	// applyVolume while they drag a volume slider: the server's value must not
	// fight the thumb under the finger.
	applyPosition bool
	applyVolume   bool
}

// transportView is one rendering of the player: the desktop bar, the compact
// mini bar, or the full-screen Now Playing page. A view owns its widgets
// (Fyne objects cannot be shared between parents, so every view builds its own
// sliders and buttons) and the player pushes state into all of them.
type transportView interface {
	// apply renders a status snapshot.
	apply(transportState)
	// setCover shows the current track's album art; nil means none is known,
	// and the view falls back to a placeholder.
	setCover(image.Image)
}

// player is the playback controller: it drives the backend on user actions and
// reflects the server's state through a Status() poll ticker, pushing each
// snapshot into every registered view. It holds no widgets of its own — the
// compact layout renders a mini bar plus a Now Playing page while the regular
// layout renders one wide bar, and both are fed from here so navigating
// between them never loses (or double-applies) playback state.
type player struct {
	backend playback.PlaybackBackend

	mu    sync.Mutex
	metas map[string]data.Track // queue URL → track metadata; survives reorder/shuffle
	// resolver re-derives metadata for a queue URL the registry doesn't know,
	// used to re-label a queue that outlived this app's memory (restart, while
	// pwplay still holds the queue). resolving guards against re-fetching a URL
	// that's already in flight or known-unresolvable.
	resolver  func(url string) (data.Track, bool)
	resolving map[string]bool
	// listener, when set, receives every status snapshot on the UI goroutine.
	// The queue view subscribes to stay live off the same poll as the player.
	listener func(playback.Status)
	playing  bool // last observed play state, for the play/pause toggle
	volume   float64
	seeking  bool // true while the user drags a seek slider
	adjVol   bool // true while the user drags a volume slider
	// applying is true while apply() is pushing server state into the views'
	// sliders. Fyne's Slider.SetValue fires OnChangeEnded, so without this
	// guard every poll's SetValue would echo back a Seek/SetVolume to the
	// server — and each Seek clears pwplay's ring buffer, chopping the audio
	// twice a second.
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

	// views receive every snapshot. Mutated only during setup and read on the
	// UI goroutine, both under mu since Start's goroutine reads it.
	views []transportView

	// covers resolves album art for the current track. The following three
	// fields are touched only on the UI goroutine (from apply), so they need
	// no lock: coverURL is the queue URL whose art is on screen and coverGen
	// drops results from a track the user has already skipped past.
	covers   *coverStore
	coverURL string
	coverGen int

	stopOnce sync.Once
	stopCh   chan struct{}
}

// newPlayer builds the playback controller bound to the given backend. Add
// views with AddView, then call Start to begin polling.
func newPlayer(backend playback.PlaybackBackend, covers *coverStore) *player {
	return &player{
		backend:   backend,
		covers:    covers,
		volume:    1,
		metas:     map[string]data.Track{},
		resolving: map[string]bool{},
		stopCh:    make(chan struct{}),
	}
}

// AddView registers a rendering of the player. Views are never removed: a view
// that is off screen costs one widget update per poll, which is cheaper than
// re-subscribing (and re-syncing) every time the user navigates.
func (p *player) AddView(v transportView) {
	p.mu.Lock()
	p.views = append(p.views, v)
	p.mu.Unlock()
}

// newSeekSlider builds a seek slider wired to this player. Each view needs its
// own instance, and they must all share the echo guards, so construction lives
// here rather than in the views.
func (p *player) newSeekSlider() *widget.Slider {
	s := widget.NewSlider(0, 1)
	s.Step = 0.1
	s.OnChanged = func(float64) {
		p.mu.Lock()
		if !p.applying {
			p.seeking = true
		}
		p.mu.Unlock()
	}
	s.OnChangeEnded = func(v float64) {
		p.mu.Lock()
		applying := p.applying
		if !applying {
			p.seeking = false
			// Hold the target until a poll confirms the server has seeked;
			// otherwise a poll landing before Seek propagates would snap the
			// thumb back to the stale position and then jump forward again.
			vv := v
			p.pendSeek = &vv
			p.pendSeekTTL = 4 // ~2s at the 500ms poll interval
		}
		p.mu.Unlock()
		if applying {
			return
		}
		go func() { _ = p.backend.Seek(v) }()
	}
	return s
}

// newVolumeSlider builds a volume slider wired to this player (see
// newSeekSlider for why construction lives here).
func (p *player) newVolumeSlider() *widget.Slider {
	s := widget.NewSlider(0, 2)
	s.Step = 0.01
	s.SetValue(1)
	s.OnChanged = func(float64) {
		p.mu.Lock()
		if !p.applying {
			p.adjVol = true
		}
		p.mu.Unlock()
	}
	s.OnChangeEnded = func(v float64) {
		p.mu.Lock()
		applying := p.applying
		if !applying {
			p.adjVol = false
			// Hold the just-set value until a poll confirms the server agrees;
			// otherwise a poll landing before SetVolume propagates would snap
			// the thumb back to the stale server volume.
			vv := v
			p.pendVol = &vv
		}
		p.mu.Unlock()
		if applying {
			return
		}
		go func() { _ = p.backend.SetVolume(v) }()
	}
	return s
}

// SetQueue replaces the track registry; call it when the queue is replaced
// (PlayAlbum). Keying by stream URL (not by index) means the mapping survives
// reorder and shuffle, which permute the server playlist.
func (p *player) SetQueue(urls []string, meta []data.Track) {
	p.mu.Lock()
	p.metas = map[string]data.Track{}
	p.resolving = map[string]bool{}
	for i, u := range urls {
		if i < len(meta) {
			p.metas[u] = meta[i]
		}
	}
	p.mu.Unlock()
}

// SetResolver installs a callback that re-derives metadata for a queue URL the
// registry doesn't know (see resolver). Called once at startup.
func (p *player) SetResolver(fn func(url string) (data.Track, bool)) {
	p.mu.Lock()
	p.resolver = fn
	p.mu.Unlock()
}

// AppendQueue extends the track registry; call it when tracks are enqueued.
func (p *player) AppendQueue(urls []string, meta []data.Track) {
	p.mu.Lock()
	for i, u := range urls {
		if i < len(meta) {
			p.metas[u] = meta[i]
		}
	}
	p.mu.Unlock()
}

// Label resolves a queue URL to its display title, or "" if unknown.
func (p *player) Label(url string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if m, ok := p.metas[url]; ok {
		return m.Display()
	}
	return ""
}

// TrackMeta returns the registered track metadata for a queue URL, if known.
func (p *player) TrackMeta(url string) (data.Track, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.metas[url]
	return m, ok
}

// SetStatusListener registers (or clears, with nil) a callback invoked with each
// status snapshot on the UI goroutine, so a view can stay live off the player's
// existing poll instead of running its own ticker.
func (p *player) SetStatusListener(fn func(playback.Status)) {
	p.mu.Lock()
	p.listener = fn
	p.mu.Unlock()
}

// SetRepeat enables or disables repeat-all (restart the queue after it ends).
func (p *player) SetRepeat(on bool) {
	p.mu.Lock()
	p.repeatAll = on
	p.mu.Unlock()
}

// RepeatAll reports whether repeat-all is enabled.
func (p *player) RepeatAll() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.repeatAll
}

// Playing reports the last observed play state.
func (p *player) Playing() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.playing
}

// togglePlay pauses when playing, otherwise resumes.
func (p *player) togglePlay() {
	if p.Playing() {
		p.do(p.backend.Pause)
	} else {
		p.do(p.backend.Play)
	}
}

// volumeStep nudges the volume by delta (the sliders' range is 0…2), holding
// the new value as pending so the next poll doesn't snap the sliders back
// before the server confirms — the same guard the volume slider uses. Bound
// to the VolumeUp/VolumeDown hotkeys.
func (p *player) volumeStep(delta float64) {
	p.mu.Lock()
	v := p.volume + delta
	if v < 0 {
		v = 0
	}
	if v > 2 {
		v = 2
	}
	p.pendVol = &v
	p.mu.Unlock()
	go func() { _ = p.backend.SetVolume(v) }()
}

// seekBy jumps relative to the current position; bound to the
// SeekForward/SeekBackward hotkeys.
func (p *player) seekBy(sec float64) {
	p.do(func() error { return p.backend.SeekRelative(sec) })
}

// do runs a backend action off the UI goroutine so the click returns instantly.
func (p *player) do(fn func() error) {
	go func() { _ = fn() }()
}

// Start launches the poll loop. Stop ends it.
func (p *player) Start() {
	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-p.stopCh:
				return
			case <-ticker.C:
				s, err := p.backend.Status()
				if err != nil {
					continue
				}
				fyne.Do(func() { p.apply(s) })
			}
		}
	}()
}

// Stop ends the poll loop; safe to call more than once.
func (p *player) Stop() {
	p.stopOnce.Do(func() { close(p.stopCh) })
}

// apply renders a status snapshot into every view. It runs on the UI goroutine.
func (p *player) apply(s playback.Status) {
	p.mu.Lock()
	p.playing = s.Playing
	p.volume = s.Volume
	seeking, adjVol := p.seeking, p.adjVol
	pendVol := p.pendVol
	if pendVol != nil && absDiff(s.Volume, *pendVol) < 0.02 {
		// Server now reflects our local change; stop holding.
		p.pendVol = nil
		pendVol = nil
	}
	pendSeek := p.pendSeek
	if pendSeek != nil {
		if p.pendSeekTTL > 0 {
			p.pendSeekTTL--
		}
		// Clear once the server position is near the target (allowing for
		// playback advancing since the seek) or the wait times out.
		if absDiff(s.Position, *pendSeek) < 1.0 || p.pendSeekTTL == 0 {
			p.pendSeek = nil
			pendSeek = nil
		}
	}
	now := p.nowPlaying(s)
	listener := p.listener
	views := p.views
	var missing []string
	if p.resolver != nil {
		for _, u := range s.Playlist {
			if _, ok := p.metas[u]; !ok && !p.resolving[u] {
				p.resolving[u] = true
				missing = append(missing, u)
			}
		}
	}
	p.mu.Unlock()

	if len(missing) > 0 {
		go p.resolveMissing(missing)
	}

	if listener != nil {
		listener(s)
	}

	// While a seek is pending confirmation, pin the thumb/label to the target
	// so a stale poll doesn't bounce it to the old position.
	pos := s.Position
	if pendSeek != nil {
		pos = *pendSeek
	}
	st := transportState{
		playing:       s.Playing,
		title:         now.title,
		subtitle:      now.subtitle,
		position:      pos,
		duration:      s.TrackDuration,
		volume:        s.Volume,
		applyPosition: !seeking,
		applyVolume:   !adjVol && pendVol == nil,
	}

	// Mark programmatic slider updates so the sliders' OnChangeEnded handlers
	// don't echo a Seek/SetVolume back to the server. Set outside p.mu because
	// SetValue invokes those handlers synchronously and they take p.mu.
	p.mu.Lock()
	p.applying = true
	p.mu.Unlock()

	for _, v := range views {
		v.apply(st)
	}

	p.mu.Lock()
	p.applying = false
	p.mu.Unlock()

	p.syncCover(now)
	p.maybeRepeat(s)
}

// syncCover keeps the views' album art in step with the current track. It only
// acts when the current queue URL changes, so the cover is fetched once per
// track rather than twice a second, and a result arriving after the user has
// skipped on is dropped via the generation counter. Runs on the UI goroutine.
func (p *player) syncCover(now trackInfo) {
	if now.url == p.coverURL {
		return
	}
	if !now.known {
		// Metadata is still being resolved (or the entry is not a tie blob):
		// clear the art but don't record the URL, so the next poll retries
		// once the registry has it.
		p.coverURL = ""
		p.coverGen++
		p.pushCover(nil)
		return
	}
	p.coverURL = now.url
	p.coverGen++
	gen := p.coverGen
	if p.covers == nil {
		p.pushCover(nil)
		return
	}
	p.covers.Request(now.albumUID, func(img image.Image) {
		if gen != p.coverGen {
			return // the user moved on while this was loading
		}
		p.pushCover(img)
	})
}

// pushCover hands album art to every view. Runs on the UI goroutine.
func (p *player) pushCover(img image.Image) {
	p.mu.Lock()
	views := p.views
	p.mu.Unlock()
	for _, v := range views {
		v.setCover(img)
	}
}

// maybeRepeat restarts the queue from the top when repeat-all is on and the last
// track has just finished. End-of-queue is detected as: stopped, on the last
// track, with the position at (near) the track's end. This is distinguishable
// from a user Stop, which now rewinds the position to 0. repeatFired guards a
// single restart until playback is observed again.
func (p *player) maybeRepeat(s playback.Status) {
	p.mu.Lock()
	repeat := p.repeatAll
	fired := p.repeatFired
	atEnd := s.Stopped && s.TotalTracks > 0 &&
		s.CurrentTrack == s.TotalTracks-1 &&
		s.TrackDuration > 0 && s.Position >= s.TrackDuration-0.75
	switch {
	case repeat && atEnd && !fired:
		p.repeatFired = true
	case s.Playing:
		p.repeatFired = false
	}
	restart := repeat && atEnd && !fired
	p.mu.Unlock()

	if restart {
		go func() { _ = p.backend.Play() }()
	}
}

// absDiff returns the absolute difference between two floats.
func absDiff(a, b float64) float64 {
	if a < b {
		return b - a
	}
	return a - b
}

// resolveMissing resolves queue URLs the registry doesn't know (off the UI
// goroutine, since the resolver hits tie over the network) and stores what it
// finds. URLs that don't resolve stay marked in `resolving` so they aren't
// retried every poll. The next status poll re-labels the now-playing text and
// the queue rows from the freshly populated registry.
func (p *player) resolveMissing(urls []string) {
	p.mu.Lock()
	resolver := p.resolver
	p.mu.Unlock()
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
	p.mu.Lock()
	for u, trk := range found {
		p.metas[u] = trk
	}
	p.mu.Unlock()
}

// trackInfo is the resolved identity of the current track: what to display,
// which album's art to show, and whether tie metadata was available at all.
type trackInfo struct {
	url      string
	title    string
	subtitle string
	albumUID string
	known    bool
}

// nowPlaying resolves the current track's labels and album, preferring the
// queued metadata and falling back to the backend's file identifier. Caller
// holds p.mu.
func (p *player) nowPlaying(s playback.Status) trackInfo {
	info := trackInfo{title: "Nothing playing"}
	if s.CurrentTrack >= 0 && s.CurrentTrack < len(s.Playlist) {
		info.url = s.Playlist[s.CurrentTrack]
		if m, ok := p.metas[info.url]; ok {
			info.known = true
			info.albumUID = m.AlbumUID
			info.subtitle = trackSubtitle(m)
			if label := m.Display(); label != "" {
				info.title = label
				return info
			}
		}
	}
	if s.TotalTracks == 0 {
		return trackInfo{title: "Nothing playing"}
	}
	if s.CurrentFile != "" {
		info.title = s.CurrentFile
	}
	return info
}

// trackSubtitle renders a track's secondary line: artist, album, or both.
func trackSubtitle(t data.Track) string {
	switch {
	case t.Artist != "" && t.Album != "":
		return t.Artist + " · " + t.Album
	case t.Artist != "":
		return t.Artist
	default:
		return t.Album
	}
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
