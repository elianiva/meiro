//go:build windows && (amd64 || arm64)

package systemmedia

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// Windows shows what plays in the media flyout, on the lock screen and to
// every app that reads the system's media sessions, such as status bars,
// through SystemMediaTransportControls. MyGo's NowPlaying publishes them on
// the app's own window and reports their buttons and scrubber on the main
// thread.
//
// The media keys stay with the hotkeys the session registers: Windows hands
// a registered hotkey to its window and to no one else, so a key press does
// not also reach the controls.

// nowPlaying is what the session uses of mygo.NowPlaying.
type nowPlaying interface {
	Set(mygo.NowPlayingInfo)
	SetPlayback(mygo.PlaybackState, time.Duration)
	Clear()
	OnCommand(func(mygo.MediaCommand, time.Duration)) (off func())
}

// mediaTransport keeps the system's media controls in step with the state.
// Update and Close run on the UI thread, the artwork's download on its own.
type mediaTransport struct {
	np       nowPlaying
	controls Controls
	load     func(context.Context, string) ([]byte, error)
	off      func()

	mu       sync.Mutex
	closed   bool
	hasState bool
	state    State
	stateAt  time.Time
	// showing is whether the controls show a track.
	showing  bool
	commands mygo.MediaCommand
	// infoPosition and infoAt are the position last published and when, from
	// which Windows advances the position while playing.
	infoPosition  time.Duration
	infoAt        time.Time
	artwork       []byte
	artworkGen    uint64
	cancelArtwork context.CancelFunc
}

func newMediaTransport(np nowPlaying, controls Controls, load func(context.Context, string) ([]byte, error)) *mediaTransport {
	m := &mediaTransport{np: np, controls: controls, load: load}
	m.off = np.OnCommand(m.command)
	return m
}

// command runs a button or the scrubber of the system's controls. NowPlaying
// has already turned play/pause into Play or Pause by the published state.
func (m *mediaTransport) command(cmd mygo.MediaCommand, position time.Duration) {
	var fn func()
	switch cmd {
	case mygo.MediaPlay:
		fn = m.controls.Play
		if fn == nil {
			fn = m.controls.Toggle
		}
	case mygo.MediaPause:
		fn = m.controls.Pause
		if fn == nil {
			fn = m.controls.Toggle
		}
	case mygo.MediaNext:
		fn = m.controls.Next
	case mygo.MediaPrevious:
		fn = m.controls.Previous
	case mygo.MediaSeek:
		if m.controls.Seek != nil {
			m.controls.Seek(position)
		}
		return
	case mygo.MediaStop:
		// Not offered: Meiro has no stopped state to return from.
		return
	}
	if fn != nil {
		fn()
	}
}

// mediaCommands are the controls a state offers. Play and Pause go together,
// since NowPlaying sends play/pause as one of them by the state.
func mediaCommands(state State) mygo.MediaCommand {
	var c mygo.MediaCommand
	if state.CanPlay || state.CanPause {
		c |= mygo.MediaPlay | mygo.MediaPause
	}
	if state.CanNext {
		c |= mygo.MediaNext
	}
	if state.CanPrevious {
		c |= mygo.MediaPrevious
	}
	if state.CanSeek && state.Duration > 0 {
		c |= mygo.MediaSeek
	}
	return c
}

func playbackState(status Status) mygo.PlaybackState {
	switch status {
	case Playing:
		return mygo.PlaybackPlaying
	case Paused:
		return mygo.PlaybackPaused
	case Stopped:
		return mygo.PlaybackStopped
	}
	return mygo.PlaybackStopped
}

func (m *mediaTransport) update(state State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	if state.VideoID == "" {
		m.dropArtwork()
		if m.showing {
			m.np.Clear()
			m.showing = false
		}
		m.remember(state)
		return
	}
	if !m.hasState || state.VideoID != m.state.VideoID || state.ArtworkURL != m.state.ArtworkURL {
		m.dropArtwork()
		if state.ArtworkURL != "" && m.load != nil {
			ctx, cancel := context.WithCancel(context.Background())
			m.cancelArtwork = cancel
			go m.fetchArtwork(ctx, state.ArtworkURL, m.artworkGen)
		}
	}
	commands := mediaCommands(state)
	switch {
	case !m.showing || !state.sameMetadata(m.state) || state.Album != m.state.Album || commands != m.commands:
		m.publish(state, commands)
	case state.Status != m.state.Status,
		state.Status != Playing && state.Position != m.state.Position,
		state.SeekVersion != m.state.SeekVersion,
		// Buffering and the like stop the clock Windows keeps running.
		state.Status == Playing && durationDistance(state.Position, m.infoPosition+time.Since(m.infoAt)) > time.Second:
		m.np.SetPlayback(playbackState(state.Status), state.Position)
		m.infoPosition, m.infoAt = state.Position, time.Now()
	}
	m.remember(state)
}

func (m *mediaTransport) remember(state State) {
	m.state, m.stateAt, m.hasState = state, time.Now(), true
}

// publish shows the whole state, under mu.
func (m *mediaTransport) publish(state State, commands mygo.MediaCommand) {
	m.np.Set(mygo.NowPlayingInfo{
		Title:    state.Title,
		Artist:   state.Artist,
		Album:    state.Album,
		Artwork:  m.artwork,
		Duration: max(state.Duration, 0),
		Position: state.Position,
		State:    playbackState(state.Status),
		Commands: commands,
	})
	m.showing, m.commands = true, commands
	m.infoPosition, m.infoAt = state.Position, time.Now()
}

// dropArtwork forgets the track's artwork and stops its download, under mu.
func (m *mediaTransport) dropArtwork() {
	if m.cancelArtwork != nil {
		m.cancelArtwork()
		m.cancelArtwork = nil
	}
	m.artworkGen++
	m.artwork = nil
}

func (m *mediaTransport) fetchArtwork(ctx context.Context, url string, generation uint64) {
	data, err := m.load(ctx, url)
	if err != nil {
		// Not retried: the next track or URL starts a new download.
		if ctx.Err() == nil {
			log.Printf("load system media artwork: %v", err)
		}
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || generation != m.artworkGen || !m.showing {
		return
	}
	m.artwork = data
	state := m.state
	if state.Status == Playing {
		state.Position += time.Since(m.stateAt)
		if state.Duration > 0 && state.Position > state.Duration {
			state.Position = state.Duration
		}
	}
	m.publish(state, m.commands)
}

func (m *mediaTransport) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	m.dropArtwork()
	if m.off != nil {
		m.off()
	}
	if m.showing {
		m.np.Clear()
		m.showing = false
	}
}
