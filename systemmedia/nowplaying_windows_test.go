//go:build windows && (amd64 || arm64)

package systemmedia

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// recordedNowPlaying records the calls the session makes, and wraps the
// real NowPlaying so what it shows can be read back.
type recordedNowPlaying struct {
	calls   []string
	command func(mygo.MediaCommand, time.Duration)
	offs    int
}

func (r *recordedNowPlaying) Set(info mygo.NowPlayingInfo) {
	r.calls = append(r.calls, "set")
	mygo.NowPlaying.Set(info)
}

func (r *recordedNowPlaying) SetPlayback(state mygo.PlaybackState, position time.Duration) {
	r.calls = append(r.calls, "playback")
	mygo.NowPlaying.SetPlayback(state, position)
}

func (r *recordedNowPlaying) Clear() {
	r.calls = append(r.calls, "clear")
	mygo.NowPlaying.Clear()
}

func (r *recordedNowPlaying) OnCommand(fn func(mygo.MediaCommand, time.Duration)) func() {
	r.command = fn
	return func() { r.offs++ }
}

// take returns the calls since the last take.
func (r *recordedNowPlaying) take() []string {
	calls := r.calls
	r.calls = nil
	return calls
}

func pausedTrack() State {
	return State{
		VideoID:     "v1",
		Title:       "Song",
		Artist:      "Artist",
		Album:       "Album",
		Duration:    3 * time.Minute,
		Position:    42 * time.Second,
		Status:      Paused,
		CanPlay:     true,
		CanNext:     true,
		CanPrevious: true,
		CanSeek:     true,
	}
}

func shown(t *testing.T) mygo.NowPlayingInfo {
	t.Helper()
	info, ok := mygo.NowPlaying.Info()
	if !ok {
		t.Fatal("NowPlaying shows nothing")
	}
	return info
}

func TestMediaTransportPublishesTrack(t *testing.T) {
	np := &recordedNowPlaying{}
	m := newMediaTransport(np, Controls{}, nil)
	t.Cleanup(m.close)

	m.update(pausedTrack())
	info := shown(t)
	if info.Title != "Song" || info.Artist != "Artist" || info.Album != "Album" {
		t.Errorf("metadata = %q, %q, %q", info.Title, info.Artist, info.Album)
	}
	if info.Duration != 3*time.Minute || info.Position != 42*time.Second || info.State != mygo.PlaybackPaused {
		t.Errorf("timeline = %v of %v, state %v", info.Position, info.Duration, info.State)
	}
	// Paused, the play/pause button needs Play; NowPlaying drops a play/pause
	// whose command is not offered, so Pause goes with it for when it plays.
	want := mygo.MediaPlay | mygo.MediaPause | mygo.MediaNext | mygo.MediaPrevious | mygo.MediaSeek
	if info.Commands != want {
		t.Errorf("commands = %b, want %b", info.Commands, want)
	}
}

func TestMediaTransportOffersOnlyWhatStateAllows(t *testing.T) {
	np := &recordedNowPlaying{}
	m := newMediaTransport(np, Controls{}, nil)
	t.Cleanup(m.close)

	state := pausedTrack()
	state.CanNext, state.CanPrevious, state.CanSeek = false, false, false
	m.update(state)
	if got := shown(t).Commands; got != mygo.MediaPlay|mygo.MediaPause {
		t.Errorf("commands = %b, want play and pause only", got)
	}
	// A live stream has no length to seek in.
	state.CanSeek, state.Duration = true, 0
	m.update(state)
	if got := shown(t).Commands; got&mygo.MediaSeek != 0 {
		t.Errorf("commands = %b offer seeking without a duration", got)
	}
}

func TestMediaTransportPublishesOnlyChanges(t *testing.T) {
	np := &recordedNowPlaying{}
	m := newMediaTransport(np, Controls{}, nil)
	t.Cleanup(m.close)

	state := pausedTrack()
	m.update(state)
	np.take()

	m.update(state)
	if calls := np.take(); len(calls) != 0 {
		t.Errorf("same state published %v", calls)
	}

	state.Status, state.CanPause = Playing, true
	m.update(state)
	if calls := np.take(); !slices.Equal(calls, []string{"playback"}) {
		t.Errorf("play published %v, want playback", calls)
	}
	if got := shown(t).State; got != mygo.PlaybackPlaying {
		t.Errorf("state = %v, want playing", got)
	}

	// Windows advances the position itself while playing.
	state.Position += 500 * time.Millisecond
	m.update(state)
	if calls := np.take(); len(calls) != 0 {
		t.Errorf("progress published %v", calls)
	}

	state.SeekVersion++
	state.Position = time.Minute
	m.update(state)
	if calls := np.take(); !slices.Equal(calls, []string{"playback"}) {
		t.Errorf("seek published %v, want playback", calls)
	}

	state.CanNext = false
	m.update(state)
	if calls := np.take(); !slices.Equal(calls, []string{"set"}) {
		t.Errorf("new commands published %v, want set", calls)
	}

	state.VideoID, state.Title = "v2", "Other"
	m.update(state)
	if calls := np.take(); !slices.Equal(calls, []string{"set"}) {
		t.Errorf("new track published %v, want set", calls)
	}
	if got := shown(t).Title; got != "Other" {
		t.Errorf("title = %q, want Other", got)
	}
}

func TestMediaTransportClearsWithoutTrack(t *testing.T) {
	np := &recordedNowPlaying{}
	m := newMediaTransport(np, Controls{}, nil)
	t.Cleanup(m.close)

	m.update(State{Status: Stopped})
	if calls := np.take(); len(calls) != 0 {
		t.Errorf("no track published %v", calls)
	}
	m.update(pausedTrack())
	m.update(State{Status: Stopped})
	if calls := np.take(); !slices.Equal(calls, []string{"set", "clear"}) {
		t.Errorf("calls = %v, want set then clear", calls)
	}
	if _, ok := mygo.NowPlaying.Info(); ok {
		t.Error("NowPlaying still shows a track")
	}
	m.update(State{Status: Stopped})
	if calls := np.take(); len(calls) != 0 {
		t.Errorf("second empty state published %v", calls)
	}
}

func TestMediaTransportCommands(t *testing.T) {
	var got []string
	np := &recordedNowPlaying{}
	m := newMediaTransport(np, Controls{
		Play:     func() { got = append(got, "play") },
		Pause:    func() { got = append(got, "pause") },
		Toggle:   func() { got = append(got, "toggle") },
		Stop:     func() { got = append(got, "stop") },
		Next:     func() { got = append(got, "next") },
		Previous: func() { got = append(got, "previous") },
		Seek:     func(p time.Duration) { got = append(got, "seek "+p.String()) },
	}, nil)
	t.Cleanup(m.close)

	for _, cmd := range []mygo.MediaCommand{mygo.MediaPlay, mygo.MediaPause, mygo.MediaNext, mygo.MediaPrevious, mygo.MediaStop} {
		np.command(cmd, 0)
	}
	np.command(mygo.MediaSeek, 95*time.Second)
	want := []string{"play", "pause", "next", "previous", "seek 1m35s"}
	if !slices.Equal(got, want) {
		t.Errorf("commands ran %v, want %v", got, want)
	}
}

func TestMediaTransportCommandsFallBackToToggle(t *testing.T) {
	toggles := 0
	np := &recordedNowPlaying{}
	m := newMediaTransport(np, Controls{Toggle: func() { toggles++ }}, nil)
	t.Cleanup(m.close)

	np.command(mygo.MediaPlay, 0)
	np.command(mygo.MediaPause, 0)
	np.command(mygo.MediaSeek, time.Second)
	if toggles != 2 {
		t.Errorf("toggles = %d, want 2", toggles)
	}
}

func TestMediaTransportArtwork(t *testing.T) {
	type request struct {
		ctx  context.Context
		url  string
		data chan []byte
	}
	requests := make(chan request, 2)
	load := func(ctx context.Context, url string) ([]byte, error) {
		r := request{ctx, url, make(chan []byte)}
		requests <- r
		select {
		case data := <-r.data:
			return data, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	np := &recordedNowPlaying{}
	m := newMediaTransport(np, Controls{}, load)
	t.Cleanup(m.close)

	state := pausedTrack()
	state.ArtworkURL = "https://example.invalid/a.jpg"
	m.update(state)
	first := <-requests
	if first.url != state.ArtworkURL {
		t.Fatalf("loaded %q", first.url)
	}
	if got := shown(t).Artwork; got != nil {
		t.Errorf("artwork before load = %v", got)
	}

	// The track changes before its artwork arrives: the old one is dropped.
	state.VideoID, state.ArtworkURL = "v2", "https://example.invalid/b.jpg"
	m.update(state)
	second := <-requests
	select {
	case <-first.ctx.Done():
	case <-time.After(5 * time.Second):
		t.Error("the old download was not cancelled")
	}
	second.data <- []byte("new")
	deadline := time.Now().Add(5 * time.Second)
	for string(shown(t).Artwork) != "new" {
		if time.Now().After(deadline) {
			t.Fatalf("artwork = %q, want new", shown(t).Artwork)
		}
		time.Sleep(time.Millisecond)
	}

	// What follows keeps the artwork.
	state.CanNext = false
	m.update(state)
	if got := string(shown(t).Artwork); got != "new" {
		t.Errorf("artwork after update = %q", got)
	}
}

func TestMediaTransportArtworkFailureKeepsTrack(t *testing.T) {
	np := &recordedNowPlaying{}
	done := make(chan struct{})
	m := newMediaTransport(np, Controls{}, func(context.Context, string) ([]byte, error) {
		defer close(done)
		return nil, errors.New("offline")
	})
	t.Cleanup(m.close)

	state := pausedTrack()
	state.ArtworkURL = "https://example.invalid/a.jpg"
	m.update(state)
	<-done
	if info := shown(t); info.Title != "Song" || info.Artwork != nil {
		t.Errorf("after a failed load: %q with %d bytes of artwork", info.Title, len(info.Artwork))
	}
}

func TestMediaTransportClose(t *testing.T) {
	np := &recordedNowPlaying{}
	m := newMediaTransport(np, Controls{}, nil)
	m.update(pausedTrack())
	np.take()

	m.close()
	m.close()
	if calls := np.take(); !slices.Equal(calls, []string{"clear"}) {
		t.Errorf("close made %v, want one clear", calls)
	}
	if np.offs != 1 {
		t.Errorf("command handler removed %d times, want 1", np.offs)
	}
	m.update(pausedTrack())
	if calls := np.take(); len(calls) != 0 {
		t.Errorf("update after close made %v", calls)
	}
}

func TestWindowsSessionUpdatesMediaControls(t *testing.T) {
	np := &recordedNowPlaying{}
	s := &windowsSession{media: newMediaTransport(np, Controls{}, nil)}
	if err := s.Update(pausedTrack()); err != nil {
		t.Fatal(err)
	}
	if got := shown(t).Title; got != "Song" {
		t.Errorf("title = %q", got)
	}
	s.release()
	if _, ok := mygo.NowPlaying.Info(); ok {
		t.Error("released session still shows a track")
	}
}
