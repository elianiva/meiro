package systemmedia

import "time"

type Status string

const (
	Stopped Status = "Stopped"
	Paused  Status = "Paused"
	Playing Status = "Playing"
)

type Loop string

const (
	LoopNone     Loop = "None"
	LoopTrack    Loop = "Track"
	LoopPlaylist Loop = "Playlist"
)

type State struct {
	VideoID     string
	Title       string
	Artist      string
	Album       string
	ArtworkURL  string
	Duration    time.Duration
	Position    time.Duration
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
		s.Artist == other.Artist && s.Album == other.Album &&
		s.ArtworkURL == other.ArtworkURL && s.Duration == other.Duration
}

func durationDistance(a, b time.Duration) time.Duration {
	if a > b {
		return a - b
	}
	return b - a
}

type Controls struct {
	Window     uintptr
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

type Session interface {
	Update(State) error
	Close() error
}
