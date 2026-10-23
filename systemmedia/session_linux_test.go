//go:build linux

package systemmedia

import (
	"os"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestMPRISArtworkURLRequestsLargeGoogleImages(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "dimensions",
			url:  "https://lh3.googleusercontent.com/cover=w544-h544-l90-rj",
			want: "https://lh3.googleusercontent.com/cover=w1024-h1024-l90-rj",
		},
		{
			name: "scale",
			url:  "https://lh3.googleusercontent.com/cover=s544",
			want: "https://lh3.googleusercontent.com/cover=s1024",
		},
		{
			name: "other URL",
			url:  "https://example.test/cover.jpg",
			want: "https://example.test/cover.jpg",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := mprisArtworkURL(test.url); got != test.want {
				t.Errorf("mprisArtworkURL(%q) = %q, want %q", test.url, got, test.want)
			}
		})
	}
}

// The media session talks to the user's session bus, so these need one. Run
// them under dbus-run-session, as CI does:
//
//	dbus-run-session -- go test ./systemmedia/
//
// Without a bus they skip rather than fail, so a plain `go test ./...` still
// passes on a machine that has none.
func TestMPRISSessionPublishesStateAndForwardsCommands(t *testing.T) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("run under dbus-run-session to exercise the MPRIS D-Bus service")
	}

	commands := make(chan string, 8)
	seeks := make(chan time.Duration, 4)
	session, err := New(Controls{
		Play:     func() { commands <- "play" },
		Pause:    func() { commands <- "pause" },
		Toggle:   func() { commands <- "toggle" },
		Stop:     func() { commands <- "stop" },
		Next:     func() { commands <- "next" },
		Previous: func() { commands <- "previous" },
		Seek:     func(position time.Duration) { seeks <- position },
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
		ArtworkURL: "https://lh3.googleusercontent.com/cover=w544-h544-l90-rj", Duration: 3 * time.Minute,
		Position: 17 * time.Second, Status: Playing, Volume: 0.7,
		CanPlay: true, CanPause: true, CanNext: true, CanPrevious: true, CanSeek: true,
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

	// What a desktop reads is what the session published.
	title, status := "", ""
	var artists []string
	var metadata map[string]dbus.Variant
	if err := getProperty(object, "Metadata", &metadata); err != nil {
		t.Fatal(err)
	}
	if err := metadata["xesam:title"].Store(&title); err != nil || title != state.Title {
		t.Errorf("MPRIS title = %q, err %v; want %q", title, err, state.Title)
	}
	if err := metadata["xesam:artist"].Store(&artists); err != nil || len(artists) != 1 || artists[0] != state.Artist {
		t.Errorf("MPRIS artist = %v, err %v; want [%q]", artists, err, state.Artist)
	}
	var artworkURL string
	wantArtworkURL := "https://lh3.googleusercontent.com/cover=w1024-h1024-l90-rj"
	if err := metadata["mpris:artUrl"].Store(&artworkURL); err != nil || artworkURL != wantArtworkURL {
		t.Errorf("MPRIS artwork URL = %q, err %v; want %q", artworkURL, err, wantArtworkURL)
	}
	if err := getProperty(object, "PlaybackStatus", &status); err != nil {
		t.Fatal(err)
	}
	if status != string(Playing) {
		t.Errorf("MPRIS status = %q, want %q", status, Playing)
	}
	var volume float64
	if err := getProperty(object, "Volume", &volume); err != nil {
		t.Fatal(err)
	}
	if volume != state.Volume {
		t.Errorf("MPRIS volume = %v, want %v", volume, state.Volume)
	}

	// A command the desktop sends reaches the app.
	if err := object.Call(playerInterface+".Next", 0).Err; err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-commands:
		if got != "next" {
			t.Errorf("command = %q, want next", got)
		}
	default:
		t.Error("Next was not forwarded to the app")
	}

	// A seek is announced with the position it landed on.
	signals := make(chan *dbus.Signal, 4)
	client.Signal(signals)
	if err := client.AddMatchSignal(
		dbus.WithMatchObjectPath(objectPath),
		dbus.WithMatchInterface(playerInterface),
		dbus.WithMatchMember("Seeked"),
	); err != nil {
		t.Fatal(err)
	}
	state.Position, state.SeekVersion = 42*time.Second, state.SeekVersion+1
	if err := linux.Update(state); err != nil {
		t.Fatal(err)
	}
	select {
	case signal := <-signals:
		if signal.Name != playerInterface+".Seeked" {
			t.Fatalf("signal %q, want Seeked", signal.Name)
		}
		if position, ok := signal.Body[0].(int64); !ok || position != int64(state.Position/time.Microsecond) {
			t.Errorf("Seeked position = %v, want %d", signal.Body[0], int64(state.Position/time.Microsecond))
		}
	case <-time.After(5 * time.Second):
		t.Error("no Seeked signal after a seek")
	}

	// Seek is relative to the current position, and both Seek and SetPosition
	// stay within the track.
	for _, test := range []struct {
		name   string
		offset time.Duration
		want   time.Duration
	}{
		{"positive offset", 5 * time.Second, 47 * time.Second},
		{"negative offset", -50 * time.Second, 0},
		{"upper clamp", time.Hour, state.Duration},
	} {
		if err := object.Call(playerInterface+".Seek", 0, int64(test.offset/time.Microsecond)).Err; err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		if got := <-seeks; got != test.want {
			t.Errorf("Seek %s = %v, want %v", test.name, got, test.want)
		}
	}
	trackID := metadata["mpris:trackid"].Value().(dbus.ObjectPath)
	for _, test := range []struct {
		name     string
		position time.Duration
		want     time.Duration
	}{
		{"in range", 12 * time.Second, 12 * time.Second},
	} {
		if err := object.Call(playerInterface+".SetPosition", 0, trackID, int64(test.position/time.Microsecond)).Err; err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		if got := <-seeks; got != test.want {
			t.Errorf("SetPosition %s = %v, want %v", test.name, got, test.want)
		}
	}
	// Positions outside the track are ignored, as the MPRIS spec says.
	for _, position := range []time.Duration{-time.Second, 4 * time.Minute} {
		if err := object.Call(playerInterface+".SetPosition", 0, trackID, int64(position/time.Microsecond)).Err; err != nil {
			t.Fatal(err)
		}
	}
	// Seek is a round trip behind these calls, so a forwarded seek would be queued by now.
	if err := object.Call(playerInterface+".Seek", 0, int64(time.Second/time.Microsecond)).Err; err != nil {
		t.Fatal(err)
	}
	if got := <-seeks; got != 43*time.Second {
		t.Errorf("seek after out-of-range SetPosition = %v, want 43s (earlier calls ignored)", got)
	}
}

func getProperty(object dbus.BusObject, property string, out any) error {
	value, err := object.GetProperty(playerInterface + "." + property)
	if err != nil {
		return err
	}
	return value.Store(out)
}
