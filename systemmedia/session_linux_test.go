//go:build linux

package systemmedia

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestTrackPathIsValidAndUniquePerVideo(t *testing.T) {
	first := trackPath(State{VideoID: "abc-123_def"})
	second := trackPath(State{VideoID: "different"})
	if !first.IsValid() || first == second {
		t.Errorf("track paths = %q and %q; want distinct valid object paths", first, second)
	}
	if got := trackPath(State{}); !got.IsValid() {
		t.Errorf("empty track path %q is invalid", got)
	}
}

func TestMPRISSessionPublishesStateAndForwardsCommands(t *testing.T) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("run under dbus-run-session to exercise the MPRIS D-Bus service")
	}

	commands := make(chan string, 8)
	seeks := make(chan time.Duration, 4)
	volumes := make(chan float64, 1)
	shuffles := make(chan bool, 1)
	repeats := make(chan string, 1)
	session, err := New(Controls{
		Play:     func() { commands <- "play" },
		Pause:    func() { commands <- "pause" },
		Toggle:   func() { commands <- "toggle" },
		Stop:     func() { commands <- "stop" },
		Next:     func() { commands <- "next" },
		Previous: func() { commands <- "previous" },
		Seek:     func(position time.Duration) { seeks <- position },
		SetVolume: func(volume float64) {
			volumes <- volume
		},
		SetShuffle: func(shuffle bool) { shuffles <- shuffle },
		SetRepeat:  func(status string) { repeats <- status },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close MPRIS session: %v", err)
		}
	}()

	linux := session.(*linuxSession)
	state := State{
		VideoID: "video-1", Title: "A song", Artist: "An artist",
		ArtworkURL: "https://example.test/art.jpg", Duration: 3 * time.Minute,
		Position: 17 * time.Second, Status: Playing, Volume: 0.7,
		CanPlay: true, CanPause: true, CanNext: true, CanPrevious: true,
		CanSeek: true,
	}
	if err := linux.Update(state); err != nil {
		t.Fatal(err)
	}

	client, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	object := client.Object(linux.busName, objectPath)

	var metadata map[string]dbus.Variant
	metadataValue, err := object.GetProperty(playerInterface + ".Metadata")
	if err != nil {
		t.Fatal(err)
	}
	if err := metadataValue.Store(&metadata); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := metadata["xesam:title"].Store(&title); err != nil || title != state.Title {
		t.Errorf("MPRIS title = %q, err %v; want %q", title, err, state.Title)
	}
	var status string
	statusValue, err := object.GetProperty(playerInterface + ".PlaybackStatus")
	if err != nil {
		t.Fatal(err)
	}
	if err := statusValue.Store(&status); err != nil || status != string(Playing) {
		t.Errorf("MPRIS status = %q, err %v; want %q", status, err, Playing)
	}
	var position int64
	positionValue, err := object.GetProperty(playerInterface + ".Position")
	if err != nil {
		t.Fatal(err)
	}
	if err := positionValue.Store(&position); err != nil || position != int64(state.Position/time.Microsecond) {
		t.Errorf("MPRIS position = %d, err %v; want %d", position, err, int64(state.Position/time.Microsecond))
	}
	state.Position, state.Status = 20*time.Second, Paused
	if err := linux.Update(state); err != nil {
		t.Fatal(err)
	}
	positionValue, err = object.GetProperty(playerInterface + ".Position")
	if err != nil {
		t.Fatal(err)
	}
	if err := positionValue.Store(&position); err != nil || position != int64(state.Position/time.Microsecond) {
		t.Errorf("paused MPRIS position = %d, err %v; want %d", position, err, int64(state.Position/time.Microsecond))
	}

	for _, method := range []struct{ name, want string }{
		{"Play", "play"},
		{"Pause", "pause"},
		{"PlayPause", "toggle"},
		{"Stop", "stop"},
		{"Next", "next"},
		{"Previous", "previous"},
	} {
		if err := object.Call(playerInterface+"."+method.name, 0).Err; err != nil {
			t.Fatalf("call %s: %v", method.name, err)
		}
		if got := <-commands; got != method.want {
			t.Errorf("%s command = %q, want %q", method.name, got, method.want)
		}
	}
	if err := object.Call(playerInterface+".Seek", 0, int64(5*time.Second/time.Microsecond)).Err; err != nil {
		t.Fatal(err)
	}
	if got := <-seeks; got != 5*time.Second {
		t.Errorf("Seek requested %v, want 5s", got)
	}
	trackID := metadata["mpris:trackid"].Value().(dbus.ObjectPath)
	if err := object.Call(playerInterface+".SetPosition", 0, trackID, int64(12*time.Second/time.Microsecond)).Err; err != nil {
		t.Fatal(err)
	}
	if got := <-seeks; got != 12*time.Second {
		t.Errorf("SetPosition requested %v, want 12s", got)
	}
	if err := object.Call("org.freedesktop.DBus.Properties.Set", 0, playerInterface, "Volume", dbus.MakeVariant(0.4)).Err; err != nil {
		t.Fatal(err)
	}
	if got := <-volumes; got != 0.4 {
		t.Errorf("Volume callback = %v, want 0.4", got)
	}
	if err := object.Call("org.freedesktop.DBus.Properties.Set", 0, playerInterface, "Shuffle", dbus.MakeVariant(true)).Err; err != nil {
		t.Fatal(err)
	}
	if got := <-shuffles; !got {
		t.Errorf("Shuffle callback = %v, want true", got)
	}
	if err := object.Call("org.freedesktop.DBus.Properties.Set", 0, playerInterface, "LoopStatus", dbus.MakeVariant("Playlist")).Err; err != nil {
		t.Fatal(err)
	}
	if got := <-repeats; got != "Playlist" {
		t.Errorf("LoopStatus callback = %q, want Playlist", got)
	}
	if err := object.Call("org.freedesktop.DBus.Properties.Set", 0, playerInterface, "Rate", dbus.MakeVariant(1.25)).Err; err == nil {
		t.Error("setting an unsupported playback rate should fail")
	}

	var xml string
	if err := object.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, playerInterface) || !strings.Contains(xml, "SetPosition") {
		t.Errorf("MPRIS introspection is missing player methods: %s", xml)
	}
}

func TestMPRISSessionRecordsAnUpdateFailure(t *testing.T) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("run under dbus-run-session to exercise the MPRIS D-Bus service")
	}

	session, err := New(Controls{})
	if err != nil {
		t.Fatal(err)
	}
	linux := session.(*linuxSession)
	if err := linux.conn.Close(); err != nil {
		t.Fatal(err)
	}

	first := linux.Update(State{Status: Playing})
	if first == nil {
		t.Fatal("Update succeeded after its D-Bus connection closed")
	}
	second := linux.Update(State{Status: Paused})
	if !errors.Is(second, first) {
		t.Fatalf("second Update error = %v, want the stored failure %v", second, first)
	}
}

func TestMPRISSetPropertyDoesNotHideRuntimePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("setProperty hid a runtime panic")
		}
	}()
	(&linuxSession{}).setProperty("PlaybackStatus", string(Playing))
}
