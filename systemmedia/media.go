// Package systemmedia connects app playback to the operating system's media
// controls.
package systemmedia

import "time"

// Status is the playback state published to the operating system.
type Status string

const (
	Stopped Status = "Stopped"
	Paused  Status = "Paused"
	Playing Status = "Playing"
)

// State describes the track and controls currently available to the user.
type State struct {
	VideoID     string
	Title       string
	Artist      string
	ArtworkURL  string
	Duration    time.Duration
	Position    time.Duration
	Status      Status
	LoopStatus  string
	Volume      float64
	CanPlay     bool
	CanPause    bool
	CanNext     bool
	CanPrevious bool
	CanSeek     bool
	Shuffle     bool
}

// Controls are called when the operating system sends a media command.
type Controls struct {
	Play       func()
	Pause      func()
	Toggle     func()
	Stop       func()
	Next       func()
	Previous   func()
	Seek       func(time.Duration)
	SetVolume  func(float64)
	SetShuffle func(bool)
	SetRepeat  func(string)
}

// Session publishes state and receives commands from the operating system.
type Session interface {
	// Update publishes the latest player state. A failed update means the
	// operating-system media session is no longer usable.
	Update(State) error
	Close() error
}
