//go:build linux && !android

package mpris

import (
	"bufio"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// privateBus starts a throwaway session bus for the test, so the MPRIS name
// is never claimed on the developer's real session.
func privateBus(t *testing.T) {
	t.Helper()
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon not installed")
	}
	cmd := exec.Command(daemon, "--session", "--nofork", "--print-address=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Skip("cannot start dbus-daemon:", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	addr, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal("reading bus address:", err)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", strings.TrimSpace(addr))
}

// recorder is a Controller that logs every call.
type recorder struct {
	mu    sync.Mutex
	calls []string
	seek  float64
	vol   float64
}

func (r *recorder) log(s string) { r.mu.Lock(); r.calls = append(r.calls, s); r.mu.Unlock() }
func (r *recorder) PlayPause()   { r.log("PlayPause") }
func (r *recorder) Play()        { r.log("Play") }
func (r *recorder) Pause()       { r.log("Pause") }
func (r *recorder) Stop()        { r.log("Stop") }
func (r *recorder) Next()        { r.log("Next") }
func (r *recorder) Previous()    { r.log("Previous") }
func (r *recorder) Raise()       { r.log("Raise") }
func (r *recorder) Quit()        { r.log("Quit") }
func (r *recorder) SeekTo(s float64) {
	r.mu.Lock()
	r.seek = s
	r.mu.Unlock()
	r.log("SeekTo")
}
func (r *recorder) SetVolume(v float64) {
	r.mu.Lock()
	r.vol = v
	r.mu.Unlock()
	r.log("SetVolume")
}

func (r *recorder) seekPos() float64 { r.mu.Lock(); defer r.mu.Unlock(); return r.seek }
func (r *recorder) volume() float64  { r.mu.Lock(); defer r.mu.Unlock(); return r.vol }

func (r *recorder) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return ""
	}
	return r.calls[len(r.calls)-1]
}

func TestServerMethodsPropertiesAndControl(t *testing.T) {
	privateBus(t)
	rec := &recorder{}
	srv, err := Start(rec)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if srv.Name() != BusName {
		t.Fatalf("name = %q, want %q", srv.Name(), BusName)
	}

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	obj := conn.Object(BusName, objectPath)

	// Subscribe to PropertiesChanged before the first Update.
	if err := conn.AddMatchSignal(dbus.WithMatchInterface(ifaceProps), dbus.WithMatchMember("PropertiesChanged")); err != nil {
		t.Fatal(err)
	}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)

	srv.Update(State{
		Playing: true, HasTrack: true, TrackID: "abc123", Title: "Song", Artist: "Band",
		Album: "Record", Duration: 200, Position: 10, Volume: 0.8, CanNext: true, CanPrev: true,
	})
	select {
	case sig := <-signals:
		changed := sig.Body[1].(map[string]dbus.Variant)
		if changed["PlaybackStatus"].Value() != "Playing" {
			t.Errorf("PlaybackStatus change = %v", changed["PlaybackStatus"])
		}
		if _, ok := changed["Position"]; ok {
			t.Error("Position must not be signalled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no PropertiesChanged after Update")
	}
	// An identical snapshot emits nothing.
	srv.Update(State{
		Playing: true, HasTrack: true, TrackID: "abc123", Title: "Song", Artist: "Band",
		Album: "Record", Duration: 200, Position: 10.5, Volume: 0.8, CanNext: true, CanPrev: true,
	})
	select {
	case sig := <-signals:
		t.Errorf("unchanged Update emitted %v", sig.Body)
	case <-time.After(200 * time.Millisecond):
	}

	var meta map[string]dbus.Variant
	if err := obj.Call(ifaceProps+".Get", 0, ifacePlayer, "Metadata").Store(&meta); err != nil {
		t.Fatal(err)
	}
	if meta["xesam:title"].Value() != "Song" || meta["mpris:length"].Value() != int64(200e6) {
		t.Errorf("metadata = %v", meta)
	}
	var pos int64
	if err := obj.Call(ifaceProps+".Get", 0, ifacePlayer, "Position").Store(&pos); err != nil {
		t.Fatal(err)
	}
	if pos != int64(10.5e6) {
		t.Errorf("Position = %d, want %d", pos, int64(10.5e6))
	}

	for _, m := range []string{"PlayPause", "Play", "Pause", "Stop", "Next", "Previous"} {
		if err := obj.Call(ifacePlayer+"."+m, 0).Err; err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		if rec.last() != m {
			t.Errorf("after %s, controller got %q", m, rec.last())
		}
	}
	if err := obj.Call(ifacePlayer+".Seek", 0, int64(5e6)).Err; err != nil {
		t.Fatal(err)
	}
	if rec.last() != "SeekTo" || rec.seekPos() != 15.5 {
		t.Errorf("Seek +5s: last %q seek %v, want SeekTo 15.5", rec.last(), rec.seekPos())
	}
	// SetPosition with a stale track id is ignored.
	_ = obj.Call(ifacePlayer+".SetPosition", 0, dbus.ObjectPath("/org/tie_audio/track/tother"), int64(1e6)).Err
	if rec.last() != "SeekTo" || rec.seekPos() != 15.5 {
		t.Error("SetPosition with a stale track id was applied")
	}
	if err := obj.Call(ifacePlayer+".SetPosition", 0, trackPath("abc123"), int64(42e6)).Err; err != nil {
		t.Fatal(err)
	}
	if rec.seekPos() != 42 {
		t.Errorf("SetPosition seek = %v, want 42", rec.seekPos())
	}
	if err := obj.Call(ifaceProps+".Set", 0, ifacePlayer, "Volume", dbus.MakeVariant(0.5)).Err; err != nil {
		t.Fatal(err)
	}
	if rec.volume() != 0.5 {
		t.Errorf("Volume set = %v, want 0.5", rec.volume())
	}

	// The client mode reaches the server by name.
	if err := Control("next"); err != nil {
		t.Fatal(err)
	}
	if rec.last() != "Next" {
		t.Errorf("Control(next) → %q", rec.last())
	}
	if err := Control("nope"); err == nil {
		t.Error("Control accepted an unknown action")
	}

	// A second instance gets a per-process name, and Control still finds one.
	srv2, err := Start(&recorder{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(srv2.Name(), BusName+".instance") {
		t.Errorf("second instance name = %q", srv2.Name())
	}
	srv2.Close()
}

func TestControlWithoutInstance(t *testing.T) {
	privateBus(t)
	if err := Control("play-pause"); err != ErrNotRunning {
		t.Errorf("Control with nothing running = %v, want ErrNotRunning", err)
	}
}
