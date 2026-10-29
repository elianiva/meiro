package player

import (
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// captureDevice plays like the null device but keeps the PCM the player
// handed to the speaker.
type captureDevice struct {
	mu  *sync.Mutex
	pcm *[]byte
}

func (captureDevice) Err() error { return nil }

func (d captureDevice) newPlayer(r io.Reader) output {
	return &nullOutput{r: readerFunc(func(p []byte) (int, error) {
		n, err := r.Read(p)
		d.mu.Lock()
		*d.pcm = append(*d.pcm, p[:n]...)
		d.mu.Unlock()
		return n, err
	})}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func tone(t *testing.T, hz, seconds int) string {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	path := filepath.Join(t.TempDir(), strconv.Itoa(hz)+".wav")
	source := "sine=frequency=" + strconv.Itoa(hz) + ":sample_rate=48000:duration=" + strconv.Itoa(seconds)
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", source, "-ac", "2", path).CombinedOutput(); err != nil {
		t.Fatalf("making a tone: %v: %s", err, out)
	}
	return path
}

// level is how loud hz is in the stereo PCM window, by the Goertzel filter.
func level(window []byte, hz float64) float64 {
	var s1, s2 float64
	k := 2 * math.Cos(2*math.Pi*hz/sampleRate)
	for i := 0; i+3 < len(window); i += 4 {
		x := float64(int16(uint16(window[i])|uint16(window[i+1])<<8)) / 32768
		s1, s2 = x+k*s1-s2, s1
	}
	frames := float64(len(window) / 4)
	return math.Sqrt(s1*s1+s2*s2-k*s1*s2) / frames * 2
}

func TestCrossfadeMixesTheEndOfATrackWithTheStartOfTheNext(t *testing.T) {
	const length, fade = 20, 4
	low, high := tone(t, 440, length), tone(t, 1000, length)
	var mu sync.Mutex
	var pcm []byte
	p := New(WithFFmpeg("ffmpeg"), withAudio(&sharedAudio{open: func() (device, error) { return captureDevice{&mu, &pcm}, nil }}))
	t.Cleanup(p.Stop)
	p.SetCrossfade(fade * time.Second)
	if err := p.Play(low); err != nil {
		t.Fatal(err)
	}
	if err := p.Preload("next", high); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the next track to take over", func() bool { _, ok := p.Advanced(); return ok })
	waitFor(t, "the next track to end", p.Ended)

	mu.Lock()
	defer mu.Unlock()
	// The two tracks overlap by the fade.
	if got, want := time.Duration(len(pcm))*time.Second/bytesPerSecond, (2*length-fade)*time.Second; got < want-50*time.Millisecond || got > want+50*time.Millisecond {
		t.Errorf("played %v of sound, want %v", got, want)
	}
	at := func(seconds float64) []byte {
		start := int(seconds*sampleRate) * 4
		return pcm[start : start+sampleRate/10*4]
	}
	for _, c := range []struct {
		name        string
		seconds     float64
		lowW, highW bool
	}{
		{"before the fade", 10, true, false},
		{"as the fade starts", length - fade + 0.1, true, false},
		{"mid fade", length - fade/2, true, true},
		{"after the fade", length + 2, false, true},
	} {
		w := at(c.seconds)
		l, h := level(w, 440), level(w, 1000)
		if (l > 0.03) != c.lowW || (h > 0.03) != c.highW {
			t.Errorf("%s: 440 Hz at %.2f, 1000 Hz at %.2f; want the first %t and the second %t", c.name, l, h, c.lowW, c.highW)
		}
	}
	// Equal-power fade: halfway, each track is at about 0.7 of its full level.
	full := level(at(10), 440)
	mid := at(length - fade/2)
	if l, h := level(mid, 440), level(mid, 1000); math.Abs(l-0.707*full) > 0.1*full || math.Abs(h-0.707*full) > 0.1*full {
		t.Errorf("mid fade 440 Hz %.3f and 1000 Hz %.3f, want both about %.3f", l, h, 0.707*full)
	}
	// The player reports the second track's own position.
	if got := p.Position(); got < (length-1)*time.Second || got > (length+1)*time.Second {
		t.Errorf("position after the fade = %v, want about %ds", got, length)
	}
}

func TestWithoutCrossfadeTracksKeepTheirGapless(t *testing.T) {
	low, high := tone(t, 440, 6), tone(t, 1000, 6)
	var mu sync.Mutex
	var pcm []byte
	p := New(WithFFmpeg("ffmpeg"), withAudio(&sharedAudio{open: func() (device, error) { return captureDevice{&mu, &pcm}, nil }}))
	t.Cleanup(p.Stop)
	if err := p.Play(low); err != nil {
		t.Fatal(err)
	}
	if err := p.Preload("next", high); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the next track to take over", func() bool { _, ok := p.Advanced(); return ok })
	waitFor(t, "the next track to end", p.Ended)
	mu.Lock()
	defer mu.Unlock()
	if got, want := time.Duration(len(pcm))*time.Second/bytesPerSecond, 12*time.Second; got < want-50*time.Millisecond || got > want+50*time.Millisecond {
		t.Errorf("played %v of sound, want %v", got, want)
	}
}
