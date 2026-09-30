//go:build linux && !android

package mpris

import (
	"fmt"
	"math"
	"os"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

const (
	objectPath   = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	ifaceRoot    = "org.mpris.MediaPlayer2"
	ifacePlayer  = "org.mpris.MediaPlayer2.Player"
	ifaceProps   = "org.freedesktop.DBus.Properties"
	noTrackPath  = dbus.ObjectPath("/org/mpris/MediaPlayer2/TrackList/NoTrack")
	trackPathPfx = "/org/tie_audio/track/"
)

// Server is a running MPRIS endpoint. Update is safe to call from any
// goroutine.
type Server struct {
	conn *dbus.Conn
	ctrl Controller
	name string

	mu    sync.Mutex
	state State
	// sent holds the last value signalled per Player property, so an
	// unchanged poll emits nothing.
	sent map[string]any
}

// Start connects to the session bus, claims BusName (or a per-process
// variant when another instance holds it) and exports the MPRIS objects.
func Start(ctrl Controller) (*Server, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("mpris: session bus: %w", err)
	}
	s := &Server{conn: conn, ctrl: ctrl, sent: map[string]any{}}
	s.state.Stopped = true
	s.state.Volume = 1

	root := &rootObject{s: s}
	player := &playerObject{s: s}
	if err := conn.Export(root, objectPath, ifaceRoot); err != nil {
		conn.Close()
		return nil, err
	}
	// SeekBy is exported as MPRIS "Seek" (a Go method named Seek trips vet's
	// io.Seeker signature check).
	if err := conn.ExportWithMap(player, map[string]string{"SeekBy": "Seek"}, objectPath, ifacePlayer); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.Export(&propsObject{s: s}, objectPath, ifaceProps); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.Export(introspect.Introspectable(introspectXML), objectPath,
		"org.freedesktop.DBus.Introspectable"); err != nil {
		conn.Close()
		return nil, err
	}

	name := BusName
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err == nil && reply != dbus.RequestNameReplyPrimaryOwner {
		name = fmt.Sprintf("%s.instance%d", BusName, os.Getpid())
		reply, err = conn.RequestName(name, dbus.NameFlagDoNotQueue)
	}
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("mpris: request name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		return nil, fmt.Errorf("mpris: bus name %s is taken", name)
	}
	s.name = name
	return s, nil
}

// Name reports the bus name the server owns.
func (s *Server) Name() string { return s.name }

// Close releases the bus name and the connection.
func (s *Server) Close() error {
	if s == nil || s.conn == nil {
		return nil
	}
	_, _ = s.conn.ReleaseName(s.name)
	return s.conn.Close()
}

// Update publishes a playback snapshot, emitting PropertiesChanged for the
// properties that changed (Position is never signalled, per the spec; a jump
// larger than normal playback progress emits Seeked instead).
func (s *Server) Update(st State) {
	if s == nil {
		return
	}
	s.mu.Lock()
	prev := s.state
	s.state = st
	props := playerProps(st)
	changed := map[string]dbus.Variant{}
	for k, v := range props {
		if k == "Position" {
			continue
		}
		if old, ok := s.sent[k]; ok && equalValue(old, v.Value()) {
			continue
		}
		s.sent[k] = v.Value()
		changed[k] = v
	}
	seeked := st.TrackID == prev.TrackID && st.HasTrack &&
		math.Abs(st.Position-prev.Position) > 3 // polls run every 0.5 s
	s.mu.Unlock()

	if len(changed) > 0 {
		_ = s.conn.Emit(objectPath, ifaceProps+".PropertiesChanged",
			ifacePlayer, changed, []string{})
	}
	if seeked {
		_ = s.conn.Emit(objectPath, ifacePlayer+".Seeked", micros(st.Position))
	}
}

// equalValue compares property values; Metadata maps are compared entry by
// entry (maps are not comparable with ==).
func equalValue(a, b any) bool {
	am, aok := a.(map[string]dbus.Variant)
	bm, bok := b.(map[string]dbus.Variant)
	if aok || bok {
		if !aok || !bok || len(am) != len(bm) {
			return false
		}
		for k, av := range am {
			bv, ok := bm[k]
			if !ok || av.String() != bv.String() {
				return false
			}
		}
		return true
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func micros(sec float64) int64 { return int64(sec * 1e6) }

// trackPath turns a content hash into a valid D-Bus object path.
func trackPath(id string) dbus.ObjectPath {
	if id == "" {
		return noTrackPath
	}
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return dbus.ObjectPath(trackPathPfx + "t" + b.String())
}

func playbackStatus(st State) string {
	switch {
	case st.Playing:
		return "Playing"
	case st.Stopped || !st.HasTrack:
		return "Stopped"
	default:
		return "Paused"
	}
}

func metadata(st State) map[string]dbus.Variant {
	m := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(trackPath(st.TrackID)),
	}
	if !st.HasTrack {
		return m
	}
	if st.Duration > 0 {
		m["mpris:length"] = dbus.MakeVariant(micros(st.Duration))
	}
	if st.Title != "" {
		m["xesam:title"] = dbus.MakeVariant(st.Title)
	}
	if st.Artist != "" {
		m["xesam:artist"] = dbus.MakeVariant([]string{st.Artist})
	}
	if st.Album != "" {
		m["xesam:album"] = dbus.MakeVariant(st.Album)
	}
	if st.ArtURL != "" {
		m["mpris:artUrl"] = dbus.MakeVariant(st.ArtURL)
	}
	return m
}

func playerProps(st State) map[string]dbus.Variant {
	loop := "None"
	if st.RepeatAll {
		loop = "Playlist"
	}
	return map[string]dbus.Variant{
		"PlaybackStatus": dbus.MakeVariant(playbackStatus(st)),
		"LoopStatus":     dbus.MakeVariant(loop),
		"Rate":           dbus.MakeVariant(1.0),
		"Shuffle":        dbus.MakeVariant(false),
		"Metadata":       dbus.MakeVariant(metadata(st)),
		"Volume":         dbus.MakeVariant(st.Volume),
		"Position":       dbus.MakeVariant(micros(st.Position)),
		"MinimumRate":    dbus.MakeVariant(1.0),
		"MaximumRate":    dbus.MakeVariant(1.0),
		"CanGoNext":      dbus.MakeVariant(st.CanNext),
		"CanGoPrevious":  dbus.MakeVariant(st.CanPrev),
		"CanPlay":        dbus.MakeVariant(true),
		"CanPause":       dbus.MakeVariant(true),
		"CanSeek":        dbus.MakeVariant(st.HasTrack && st.Duration > 0),
		"CanControl":     dbus.MakeVariant(true),
	}
}

func rootProps() map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"CanQuit":             dbus.MakeVariant(true),
		"CanRaise":            dbus.MakeVariant(true),
		"HasTrackList":        dbus.MakeVariant(false),
		"Identity":            dbus.MakeVariant("tie-audio"),
		"DesktopEntry":        dbus.MakeVariant("tie-audio"),
		"SupportedUriSchemes": dbus.MakeVariant([]string{}),
		"SupportedMimeTypes":  dbus.MakeVariant([]string{}),
	}
}

// rootObject implements org.mpris.MediaPlayer2.
type rootObject struct{ s *Server }

func (r *rootObject) Raise() *dbus.Error { r.s.ctrl.Raise(); return nil }
func (r *rootObject) Quit() *dbus.Error  { r.s.ctrl.Quit(); return nil }

// playerObject implements org.mpris.MediaPlayer2.Player.
type playerObject struct{ s *Server }

func (p *playerObject) Next() *dbus.Error      { p.s.ctrl.Next(); return nil }
func (p *playerObject) Previous() *dbus.Error  { p.s.ctrl.Previous(); return nil }
func (p *playerObject) Pause() *dbus.Error     { p.s.ctrl.Pause(); return nil }
func (p *playerObject) PlayPause() *dbus.Error { p.s.ctrl.PlayPause(); return nil }
func (p *playerObject) Stop() *dbus.Error      { p.s.ctrl.Stop(); return nil }
func (p *playerObject) Play() *dbus.Error      { p.s.ctrl.Play(); return nil }

// SeekBy (MPRIS "Seek") moves by offset microseconds; past the end skips to the next track,
// before the start clamps to 0 (per the spec).
func (p *playerObject) SeekBy(offset int64) *dbus.Error {
	p.s.mu.Lock()
	st := p.s.state
	p.s.mu.Unlock()
	if !st.HasTrack {
		return nil
	}
	target := st.Position + float64(offset)/1e6
	if st.Duration > 0 && target >= st.Duration {
		p.s.ctrl.Next()
		return nil
	}
	if target < 0 {
		target = 0
	}
	p.s.ctrl.SeekTo(target)
	return nil
}

// SetPosition jumps to an absolute position; ignored when trackID is stale
// or the position is out of range (per the spec).
func (p *playerObject) SetPosition(trackID dbus.ObjectPath, pos int64) *dbus.Error {
	p.s.mu.Lock()
	st := p.s.state
	p.s.mu.Unlock()
	if trackID != trackPath(st.TrackID) || pos < 0 {
		return nil
	}
	sec := float64(pos) / 1e6
	if st.Duration > 0 && sec > st.Duration {
		return nil
	}
	p.s.ctrl.SeekTo(sec)
	return nil
}

// OpenUri is unsupported (SupportedUriSchemes is empty).
func (p *playerObject) OpenUri(string) *dbus.Error { return nil }

// propsObject implements org.freedesktop.DBus.Properties for both MPRIS
// interfaces.
type propsObject struct{ s *Server }

func (o *propsObject) all(iface string) (map[string]dbus.Variant, *dbus.Error) {
	switch iface {
	case ifaceRoot:
		return rootProps(), nil
	case ifacePlayer:
		o.s.mu.Lock()
		st := o.s.state
		o.s.mu.Unlock()
		return playerProps(st), nil
	}
	return nil, dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", []any{iface})
}

func (o *propsObject) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	m, err := o.all(iface)
	if err != nil {
		return dbus.Variant{}, err
	}
	v, ok := m[name]
	if !ok {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", []any{name})
	}
	return v, nil
}

func (o *propsObject) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	return o.all(iface)
}

// Set handles the writable Player properties: Volume (LoopStatus, Shuffle and
// Rate are read-only here — the repeat toggle lives in the playlist view).
func (o *propsObject) Set(iface, name string, v dbus.Variant) *dbus.Error {
	if iface == ifacePlayer && name == "Volume" {
		vol, ok := v.Value().(float64)
		if !ok {
			return dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []any{"Volume must be a double"})
		}
		if vol < 0 {
			vol = 0
		}
		if vol > 2 {
			vol = 2 // the engine's range
		}
		o.s.ctrl.SetVolume(vol)
		return nil
	}
	return dbus.NewError("org.freedesktop.DBus.Error.PropertyReadOnly", []any{name})
}

// Control sends one player action to a running tie-audio instance over the
// session bus (the client side of the -control flag). It prefers the
// primary BusName and falls back to any per-instance name.
func Control(action string) error {
	method, ok := methodForAction(action)
	if !ok {
		return fmt.Errorf("mpris: unknown action %q (want one of %s)", action, strings.Join(Actions, ", "))
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("mpris: session bus: %w", err)
	}
	defer conn.Close()

	var names []string
	if err := conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return fmt.Errorf("mpris: list names: %w", err)
	}
	target := ""
	for _, n := range names {
		if n == BusName {
			target = n
			break
		}
		if target == "" && strings.HasPrefix(n, BusName+".") {
			target = n
		}
	}
	if target == "" {
		return ErrNotRunning
	}
	call := conn.Object(target, objectPath).Call(ifacePlayer+"."+method, 0)
	return call.Err
}

const introspectXML = `<node>
  <interface name="org.mpris.MediaPlayer2">
    <method name="Raise"/>
    <method name="Quit"/>
    <property name="CanQuit" type="b" access="read"/>
    <property name="CanRaise" type="b" access="read"/>
    <property name="HasTrackList" type="b" access="read"/>
    <property name="Identity" type="s" access="read"/>
    <property name="DesktopEntry" type="s" access="read"/>
    <property name="SupportedUriSchemes" type="as" access="read"/>
    <property name="SupportedMimeTypes" type="as" access="read"/>
  </interface>
  <interface name="org.mpris.MediaPlayer2.Player">
    <method name="Next"/>
    <method name="Previous"/>
    <method name="Pause"/>
    <method name="PlayPause"/>
    <method name="Stop"/>
    <method name="Play"/>
    <method name="Seek"><arg direction="in" name="Offset" type="x"/></method>
    <method name="SetPosition">
      <arg direction="in" name="TrackId" type="o"/>
      <arg direction="in" name="Position" type="x"/>
    </method>
    <method name="OpenUri"><arg direction="in" name="Uri" type="s"/></method>
    <signal name="Seeked"><arg name="Position" type="x"/></signal>
    <property name="PlaybackStatus" type="s" access="read"/>
    <property name="LoopStatus" type="s" access="read"/>
    <property name="Rate" type="d" access="read"/>
    <property name="Shuffle" type="b" access="read"/>
    <property name="Metadata" type="a{sv}" access="read"/>
    <property name="Volume" type="d" access="readwrite"/>
    <property name="Position" type="x" access="read"/>
    <property name="MinimumRate" type="d" access="read"/>
    <property name="MaximumRate" type="d" access="read"/>
    <property name="CanGoNext" type="b" access="read"/>
    <property name="CanGoPrevious" type="b" access="read"/>
    <property name="CanPlay" type="b" access="read"/>
    <property name="CanPause" type="b" access="read"/>
    <property name="CanSeek" type="b" access="read"/>
    <property name="CanControl" type="b" access="read"/>
  </interface>` + introspect.IntrospectDataString + `
  <interface name="org.freedesktop.DBus.Properties">
    <method name="Get">
      <arg direction="in" name="interface" type="s"/>
      <arg direction="in" name="property" type="s"/>
      <arg direction="out" name="value" type="v"/>
    </method>
    <method name="GetAll">
      <arg direction="in" name="interface" type="s"/>
      <arg direction="out" name="properties" type="a{sv}"/>
    </method>
    <method name="Set">
      <arg direction="in" name="interface" type="s"/>
      <arg direction="in" name="property" type="s"/>
      <arg direction="in" name="value" type="v"/>
    </method>
    <signal name="PropertiesChanged">
      <arg name="interface" type="s"/>
      <arg name="changed_properties" type="a{sv}"/>
      <arg name="invalidated_properties" type="as"/>
    </signal>
  </interface>
</node>`
