package player

import (
	"io"
	"sync"
	"time"
)

const (
	nullChunkDuration = 100 * time.Millisecond
	nullChunkInterval = 5 * time.Millisecond
	nullPausedPoll    = 2 * time.Millisecond
)

func WithNullAudio() Option {
	return withAudio(&sharedAudio{open: func() (device, error) { return nullDevice{}, nil }})
}

type nullDevice struct{}

func (nullDevice) Err() error                   { return nil }
func (nullDevice) newPlayer(r io.Reader) output { return &nullOutput{r: r} }

type nullOutput struct {
	r       io.Reader
	mu      sync.Mutex
	started bool
	paused  bool
	done    bool
	quit    bool
}

func (o *nullOutput) Play() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.paused = false
	if o.started {
		return
	}
	o.started = true
	go o.listen()
}

func (o *nullOutput) listen() {
	chunk := make([]byte, bytesPerSecond*int(nullChunkDuration)/int(time.Second))
	for {
		o.mu.Lock()
		paused, quit := o.paused, o.quit
		o.mu.Unlock()
		if quit {
			return
		}
		if paused {
			time.Sleep(nullPausedPoll)
			continue
		}
		if _, err := io.ReadFull(o.r, chunk); err != nil {
			o.mu.Lock()
			o.done = true
			o.mu.Unlock()
			return
		}
		time.Sleep(nullChunkInterval)
	}
}

func (o *nullOutput) Pause()               { o.mu.Lock(); o.paused = true; o.mu.Unlock() }
func (o *nullOutput) SetVolume(float64)    {}
func (o *nullOutput) BufferedSize() int    { return 0 }
func (o *nullOutput) Err() error           { return nil }
func (o *nullOutput) PauseAndStopReading() { o.mu.Lock(); o.quit = true; o.mu.Unlock() }
func (o *nullOutput) IsPlaying() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.started && !o.paused && !o.done
}
