//go:build darwin

package systemmedia

import (
	"errors"
	"sync"
	"time"

	"github.com/go-macos/objc"
)

const mediaPlayerFramework = "/System/Library/Frameworks/MediaPlayer.framework/MediaPlayer"

type macCommand struct {
	command objc.ID
	token   objc.ID
	block   objc.Block
}

type macSession struct {
	mu       sync.Mutex
	center   objc.ID
	commands []macCommand
	state    State
	hasState bool
	lastInfo time.Time
}

// New registers metadata and transport commands with macOS Now Playing.
func New(controls Controls) (Session, error) {
	if err := objc.Load(objc.Foundation, mediaPlayerFramework); err != nil {
		return nil, err
	}
	center := objc.ClassID("MPNowPlayingInfoCenter").Send(objc.Sel("defaultCenter"))
	remote := objc.ClassID("MPRemoteCommandCenter").Send(objc.Sel("sharedCommandCenter"))
	if center == 0 || remote == 0 {
		return nil, errors.New("macOS Now Playing services are unavailable")
	}
	s := &macSession{center: center}
	s.addCommand(remote, "playCommand", controls.Play, nil)
	s.addCommand(remote, "pauseCommand", controls.Pause, nil)
	s.addCommand(remote, "togglePlayPauseCommand", controls.Toggle, nil)
	s.addCommand(remote, "nextTrackCommand", controls.Next, nil)
	s.addCommand(remote, "previousTrackCommand", controls.Previous, nil)
	s.addCommand(remote, "changePlaybackPositionCommand", nil, controls.Seek)
	return s, nil
}

func (s *macSession) addCommand(remote objc.ID, selector string, action func(), seek func(time.Duration)) {
	command := remote.Send(objc.Sel(selector))
	if command == 0 || (action == nil && seek == nil) {
		return
	}
	block := objc.NewBlock(func(_ objc.Block, event objc.ID) int {
		if seek != nil {
			position := objc.Send[float64](event, objc.Sel("positionTime"))
			seek(time.Duration(position * float64(time.Second)))
		} else {
			action()
		}
		return 0 // MPRemoteCommandHandlerStatusSuccess
	})
	token := command.Send(objc.Sel("addTargetWithHandler:"), block)
	s.commands = append(s.commands, macCommand{command: command, token: token, block: block})
}

func (s *macSession) Update(state State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	objc.AutoreleasePool(func() {
		remote := objc.ClassID("MPRemoteCommandCenter").Send(objc.Sel("sharedCommandCenter"))
		s.setEnabled(remote, "playCommand", state.CanPlay)
		s.setEnabled(remote, "pauseCommand", state.CanPause)
		s.setEnabled(remote, "togglePlayPauseCommand", state.CanPlay || state.CanPause)
		s.setEnabled(remote, "nextTrackCommand", state.CanNext)
		s.setEnabled(remote, "previousTrackCommand", state.CanPrevious)
		s.setEnabled(remote, "changePlaybackPositionCommand", state.CanSeek)

		metadataChanged := !s.hasState || state.VideoID != s.state.VideoID || state.Title != s.state.Title || state.Artist != s.state.Artist || state.Duration != s.state.Duration
		statusChanged := !s.hasState || state.Status != s.state.Status
		positionChangedWhilePaused := state.Status != Playing && state.Position != s.state.Position
		if metadataChanged || statusChanged || positionChangedWhilePaused || time.Since(s.lastInfo) >= time.Second {
			if state.VideoID == "" {
				s.center.Send(objc.Sel("setNowPlayingInfo:"), 0)
				s.center.Send(objc.Sel("setPlaybackState:"), 3)
				s.lastInfo = time.Now()
				return
			}
			info := objc.ClassID("NSMutableDictionary").Send(objc.Sel("dictionary"))
			if state.Title != "" {
				info.Send(objc.Sel("setObject:forKey:"), objc.NSString(state.Title), objc.NSString("title"))
			}
			if state.Artist != "" {
				info.Send(objc.Sel("setObject:forKey:"), objc.NSString(state.Artist), objc.NSString("artist"))
			}
			if state.Duration > 0 {
				info.Send(objc.Sel("setObject:forKey:"), number(state.Duration.Seconds()), objc.NSString("playbackDuration"))
			}
			info.Send(objc.Sel("setObject:forKey:"), number(state.Position.Seconds()), objc.NSString("elapsedPlaybackTime"))
			rate := float64(0)
			playbackState := 3 // MPNowPlayingPlaybackStateStopped
			switch state.Status {
			case Playing:
				rate, playbackState = 1, 1 // Playing
			case Paused:
				playbackState = 2 // Paused
			}
			info.Send(objc.Sel("setObject:forKey:"), number(rate), objc.NSString("playbackRate"))
			s.center.Send(objc.Sel("setNowPlayingInfo:"), info)
			s.center.Send(objc.Sel("setPlaybackState:"), playbackState)
			s.lastInfo = time.Now()
		}
	})
	s.state = state
	s.hasState = true
	return nil
}

func (s *macSession) setEnabled(remote objc.ID, selector string, enabled bool) {
	command := remote.Send(objc.Sel(selector))
	if command != 0 {
		command.Send(objc.Sel("setEnabled:"), enabled)
	}
}

func number(value float64) objc.ID {
	return objc.ClassID("NSNumber").Send(objc.Sel("numberWithDouble:"), value)
}

func (s *macSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	objc.AutoreleasePool(func() {
		for _, target := range s.commands {
			if target.token != 0 {
				target.command.Send(objc.Sel("removeTarget:"), target.token)
			}
			target.block.Release()
		}
		s.commands = nil
		s.center.Send(objc.Sel("setNowPlayingInfo:"), 0)
		s.center.Send(objc.Sel("setPlaybackState:"), 3)
	})
	return nil
}
