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

// Loop is what playback repeats, by the names MPRIS gives them.
type Loop string

const (
	LoopNone     Loop = "None"     // play on, and stop at the end
	LoopTrack    Loop = "Track"    // play the same track again
	LoopPlaylist Loop = "Playlist" // go back to the start after the last
)

// valid reports whether l is one of the loops above.
func (l Loop) valid() bool { return l == LoopNone || l == LoopTrack || l == LoopPlaylist }

// State describes the track and controls currently available to the user.
type State struct {
	VideoID    string
	Title      string
	Artist     string
	ArtworkURL string
	Duration   time.Duration
	Position   time.Duration
	// SeekVersion advances on explicit seeks, including small seeks while playing.
	SeekVersion uint64
	Status      Status
	Loop        Loop
	Volume      float64
	CanPlay     bool
	CanPause    bool
	CanNext     bool
	CanPrevious bool
	CanSeek     bool
	Shuffle     bool
}

func (s State) sameMetadata(other State) bool {
	return s.VideoID == other.VideoID && s.Title == other.Title &&
		s.Artist == other.Artist && s.ArtworkURL == other.ArtworkURL && s.Duration == other.Duration
}

func durationDistance(a, b time.Duration) time.Duration {
	if a > b {
		return a - b
	}
	return b - a
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
	SetLoop    func(Loop)
}

// Session publishes state and receives commands from the operating system.
type Session interface {
	// Update publishes the latest player state. A failed update means the
	// operating-system media session is no longer usable.
	Update(State) error
	Close() error
}
