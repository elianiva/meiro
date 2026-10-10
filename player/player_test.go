package player

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCountingReaderCountsAndRemembersTheEnd(t *testing.T) {
	reader := newCountingReader(strings.NewReader("abcdef"))
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "abcdef" || reader.read.Load() != 6 {
		t.Errorf("read %q, counted %d", data, reader.read.Load())
	}
	if !reader.eof.Load() {
		t.Error("the end of the stream was not remembered")
	}
}

func TestBoundedBufferKeepsTheStart(t *testing.T) {
	buffer := &boundedBuffer{limit: 4}
	if _, err := buffer.Write([]byte("abcdefgh")); err != nil {
		t.Fatal(err)
	}
	if got := buffer.String(); got != "abcd" {
		t.Errorf("boundedBuffer = %q, want %q", got, "abcd")
	}
}

func TestIdlePlayer(t *testing.T) {
	p := New()
	if p.Active() || p.Playing() || p.Paused() || p.Ended() {
		t.Error("a new player is not idle")
	}
	if p.Position() != 0 || p.Failure() != "" {
		t.Errorf("position %v, failure %q", p.Position(), p.Failure())
	}
	p.Pause()
	p.Resume()
	p.Toggle()
	p.Seek(time.Minute)
	p.Stop()
}

func TestPlayRejectsAnEmptyURL(t *testing.T) {
	if err := New().Play("  "); err == nil {
		t.Fatal("playing an empty URL should fail")
	}
}

func TestAvailableFollowsFFmpeg(t *testing.T) {
	p := New()
	_, err := p.resolveFFmpeg()
	if got := p.Available(); got != (err == nil) {
		t.Errorf("Available = %v, but resolving ffmpeg gave %v", got, err)
	}
	if err != nil && !errors.Is(err, ErrNoFFmpeg) {
		t.Errorf("resolveFFmpeg error = %v, want ErrNoFFmpeg", err)
	}
}

func TestVolumeIsClamped(t *testing.T) {
	p := New()
	p.SetVolume(-1)
	if p.Volume() != 0 {
		t.Errorf("volume = %v after a negative value", p.Volume())
	}
	p.SetVolume(0.5)
	if p.Volume() != 0.5 {
		t.Errorf("volume = %v", p.Volume())
	}
}

func TestBoundedBufferNeverGrowsPastItsLimit(t *testing.T) {
	buffer := &boundedBuffer{limit: 8}
	for range 100 {
		_, _ = buffer.Write(bytes.Repeat([]byte("x"), 32))
	}
	if len(buffer.String()) != 8 {
		t.Errorf("buffer holds %d bytes, want 8", len(buffer.String()))
	}
}

// endOfStream decides whether a track has been played to its end. Its truth
// table is the whole contract the UI advances tracks on: a pause near the
// end holds the track, and only an unpaused stream with its data read out
// and the device idle is over.
func TestEndOfStream(t *testing.T) {
	const (
		idleDevice    = false // the device player reports not playing
		playingDevice = true
		held          = true  // the stream is paused
		following     = false // the stream is not paused
		noneRead      = int64(0)
		someRead      = int64(1024)
	)
	for _, tt := range []struct {
		name      string
		read      int64
		eof       bool
		isPlaying bool
		paused    bool
		want      bool
	}{
		{"nothing read is not the end", noneRead, true, idleDevice, following, false},
		{"no end of data is not the end", someRead, false, idleDevice, following, false},
		{"a playing stream is not over", someRead, true, playingDevice, following, false},
		{"a paused stream at its end is held, not ended", someRead, true, idleDevice, held, false},
		{"a paused stream before its end is held", someRead, false, playingDevice, held, false},
		{"a stream read out with the device idle has ended", someRead, true, idleDevice, following, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := endOfStream(tt.read, tt.eof, tt.isPlaying, tt.paused); got != tt.want {
				t.Errorf("endOfStream(read=%d, eof=%v, playing=%v, paused=%v) = %v, want %v", tt.read, tt.eof, tt.isPlaying, tt.paused, got, tt.want)
			}
		})
	}
}

func TestFailureIgnoresACleanExitAfterSound(t *testing.T) {
	done := make(chan struct{})
	close(done)
	source := newCountingReader(strings.NewReader(""))
	s := &session{source: source, stderr: &boundedBuffer{limit: 16}, done: done}
	p := &Player{session: s}

	if got := p.Failure(); got == "" {
		t.Error("ffmpeg exiting before any sound should be a failure")
	}
	source.read.Store(1024)
	if got := p.Failure(); got != "" {
		t.Errorf("a clean exit after sound is the track ending, got failure %q", got)
	}
	s.waitErr = errors.New("exit status 1")
	if got := p.Failure(); got == "" {
		t.Error("ffmpeg dying with an error is a failure")
	}
}

// ffmpeg exits while samples are still in the pipe; the reader must still get
// all of them, and then the end.
func TestSessionReadsEverythingAfterTheProcessExits(t *testing.T) {
	const size = 1 << 20
	cmd := exec.Command("head", "-c", strconv.Itoa(size), "/dev/zero")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &session{
		cmd: cmd, pipe: pipe, stderr: &boundedBuffer{limit: 16},
		source: newCountingReader(pipe), done: make(chan struct{}), quit: make(chan struct{}),
	}
	p := &Player{session: s}
	go p.reap(s)

	// Take all but the last few bytes, and let the process exit with those
	// still in the pipe, as ffmpeg does a moment before the track ends.
	head := make([]byte, size-1024)
	if _, err := io.ReadFull(s.source, head); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	rest, err := io.ReadAll(s.source)
	if err != nil || len(rest) != 1024 {
		t.Fatalf("read %d of the last 1024 bytes, err %v", len(rest), err)
	}
	if !s.source.eof.Load() {
		t.Error("the end of the stream was not reached")
	}
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the process was never reaped")
	}
	if got := p.Failure(); got != "" {
		t.Errorf("failure %q after a clean end", got)
	}
}

// Stopping must not wait for ffmpeg: a stream that has stalled keeps oto's read
// blocked on the pipe, and the user pressing next is on the main thread.
func TestStopDoesNotWaitForAStalledStream(t *testing.T) {
	script := filepath.Join(t.TempDir(), "ffmpeg")
	body := "#!/bin/sh\nhead -c 4096 /dev/zero\nexec sleep 60\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	p := New(WithFFmpeg(script))
	if _, err := p.audio.context(); err != nil {
		t.Skipf("no audio device: %v", err)
	}
	if err := p.Play("http://example.invalid/stalled"); err != nil {
		t.Fatal(err)
	}
	// Let oto take the bytes there are and block on the rest.
	time.Sleep(300 * time.Millisecond)

	stopped := make(chan struct{})
	go func() {
		p.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop is waiting for ffmpeg")
	}
}

// ffmpeg's reconnect and user-agent options belong to the HTTP protocol. A
// cached track is a local file, and ffmpeg refuses to open an input when it
// is given an option its protocol does not define, so a cached track used to
// play nothing at all.
func TestStreamArgsOnlySendHTTPOptionsToHTTPURLs(t *testing.T) {
	local := streamArgs("/Users/me/Meiro Audio Cache/abc.webm", 0)
	for _, arg := range local {
		switch arg {
		case "-reconnect", "-reconnect_streamed", "-reconnect_delay_max", "-user_agent":
			t.Errorf("a local file was given the HTTP option %s: %v", arg, local)
		}
	}
	remote := streamArgs("https://media.example/videoplayback?id=1", 0)
	joined := strings.Join(remote, " ")
	for _, want := range []string{"-reconnect 1", "-reconnect_streamed 1", "-reconnect_delay_max 5", "-user_agent"} {
		if !strings.Contains(joined, want) {
			t.Errorf("an HTTP stream did not get %q: %v", want, remote)
		}
	}
	// Seeking a local file still works: -ss is an input option, before -i.
	seeking := strings.Join(streamArgs("/tmp/track.webm", 90*time.Second), " ")
	if !strings.Contains(seeking, "-ss 90.000") || !strings.Contains(seeking, "-i /tmp/track.webm") {
		t.Errorf("a seek on a local file lost its offset or input: %s", seeking)
	}
}

// isHTTP decides which inputs the HTTP-only options may be given, so a path
// with a drive letter, a bare file name, or another protocol must not match.
func TestIsHTTPRecognisesOnlyTheStreamingSchemes(t *testing.T) {
	for _, test := range []struct {
		rawURL string
		want   bool
	}{
		{"http://media.example/a", true},
		{"https://media.example/a?dur=3", true},
		{"HTTPS://media.example/a", true},
		{"rtsp://camera/a", false},
		{"/Users/me/Meiro Audio Cache/abc.webm", false},
		{"track.webm", false},
		{"C:\\Meiro Audio Cache\\abc.webm", false},
		{"", false},
	} {
		if got := isHTTP(test.rawURL); got != test.want {
			t.Errorf("isHTTP(%q) = %v, want %v", test.rawURL, got, test.want)
		}
	}
}

type fakeOutput struct {
	err     error
	playing bool
}

func (f *fakeOutput) Play()                { f.playing = true }
func (f *fakeOutput) Pause()               { f.playing = false }
func (f *fakeOutput) SetVolume(float64)    {}
func (f *fakeOutput) BufferedSize() int    { return 0 }
func (f *fakeOutput) IsPlaying() bool      { return f.playing }
func (f *fakeOutput) PauseAndStopReading() {}
func (f *fakeOutput) Err() error           { return f.err }

type fakeDevice struct{ err error }

func (fakeDevice) newPlayer(io.Reader) output { return &fakeOutput{} }
func (d fakeDevice) Err() error               { return d.err }

func TestPlayersShareOneContext(t *testing.T) {
	opened := 0
	shared := &sharedAudio{open: func() (device, error) { opened++; return fakeDevice{}, nil }}
	a, b := &Player{audio: shared}, &Player{audio: shared}
	if _, err := a.audio.context(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.audio.context(); err != nil {
		t.Fatal(err)
	}
	if opened != 1 {
		t.Errorf("device opened %d times, want 1", opened)
	}
}

func TestOpenErrorIsReportedAndKept(t *testing.T) {
	opened := 0
	shared := &sharedAudio{open: func() (device, error) { opened++; return nil, errors.New("no device") }}
	p := &Player{audio: shared, ffmpegPath: "ffmpeg"}
	p.lookOnce.Do(func() {})
	for range 2 {
		if err := p.Play("http://example.invalid/x"); err == nil {
			t.Fatal("want an error when the device cannot open")
		}
	}
	if opened != 1 {
		t.Errorf("open attempted %d times, want 1", opened)
	}
}

func TestOutputErrorBecomesFailure(t *testing.T) {
	out := &fakeOutput{playing: true}
	source := newCountingReader(strings.NewReader(""))
	s := &session{source: source, stderr: &boundedBuffer{limit: 16}, done: make(chan struct{}), out: out}
	p := &Player{session: s, audio: &sharedAudio{open: func() (device, error) { return fakeDevice{}, nil }}}
	if got := p.Failure(); got != "" {
		t.Fatalf("unexpected failure %q", got)
	}
	out.err = errors.New("device lost")
	if got := p.Failure(); !strings.Contains(got, "device lost") {
		t.Errorf("failure = %q, want the output error", got)
	}
	if p.Playing() {
		t.Error("a player whose output failed must not report playing")
	}

	out.err = nil
	p.audio = &sharedAudio{open: func() (device, error) { return fakeDevice{err: errors.New("ctx dead")}, nil }}
	if got := p.Failure(); !strings.Contains(got, "ctx dead") {
		t.Errorf("failure = %q, want the context error", got)
	}
}
