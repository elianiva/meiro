package player

import (
	"image"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"
)

// numberedClip makes a clip of seconds at VideoFPS whose every frame is one
// flat colour: its red and green channels are the frame's number, so a
// decoded frame says where in the clip it comes from.
func numberedClip(t *testing.T, seconds int) string {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	clip := filepath.Join(t.TempDir(), "clip.mkv")
	out, err := exec.Command(ffmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "color=c=black:s=64x36:r="+strconv.Itoa(VideoFPS)+":d="+strconv.Itoa(seconds),
		"-vf", "format=rgb24,geq=r='mod(N,256)':g='floor(N/256)':b=0",
		"-c:v", "ffv1", "-pix_fmt", "gbrp", clip).CombinedOutput()
	if err != nil {
		t.Fatalf("making a clip: %v: %s", err, out)
	}
	return clip
}

// frameNumber is the number of the numberedClip frame img shows, or -1 for
// no frame.
func frameNumber(img *image.RGBA) int {
	if img == nil {
		return -1
	}
	return int(img.Pix[0]) + int(img.Pix[1])<<8
}

// frameAt asks v for the frame due at at until it stops changing, and
// returns the number of the frame it settles on. The decoder keeps only a
// few frames ahead, so catching up to a position takes a few asks.
func frameAt(t *testing.T, v *videoSession, at time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	settled := 0
	for settled < 20 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the frame due at %s", at)
		}
		if _, changed := v.frame(at); changed {
			settled = 0
			continue
		}
		settled++
		time.Sleep(5 * time.Millisecond)
	}
	img, _ := v.frame(at)
	return frameNumber(img)
}

func TestFramesAreDueAtTheirTimeInTheStream(t *testing.T) {
	clip := numberedClip(t, 4)
	p := New(WithNullAudio())
	ffmpeg, err := p.resolveFFmpeg(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.decodeVideo(ffmpeg, clip, 0)
	if err != nil {
		t.Fatal(err)
	}
	p.video = v
	t.Cleanup(p.Stop)

	// The frame shown is the newest due: at a position the one that falls
	// on it, and never one past it, however many have been decoded.
	if got := frameAt(t, v, 0); got != 0 {
		t.Fatalf("frame %d at the start, want 0", got)
	}
	if got := frameAt(t, v, 500*time.Millisecond); got != 15 {
		t.Fatalf("frame %d at 0.5 s, want 15", got)
	}
	// The same position asks for no new frame.
	if _, changed := v.frame(500 * time.Millisecond); changed {
		t.Fatal("the frame changed with the position still")
	}
	// Moving on skips what fell behind: the frames between are dropped, not
	// shown late.
	if got := frameAt(t, v, 2*time.Second); got != 60 {
		t.Fatalf("frame %d at 2 s, want 60", got)
	}
	// Past the end of the clip, the last frame stays.
	if got := frameAt(t, v, 10*time.Second); got != 4*VideoFPS-1 {
		t.Fatalf("frame %d past the end, want %d", got, 4*VideoFPS-1)
	}
	if p.VideoFailure() != "" {
		t.Fatalf("VideoFailure = %q at a clean end", p.VideoFailure())
	}
}

func TestADecodeFromWithinTheStreamIsDueFromThere(t *testing.T) {
	clip := numberedClip(t, 4)
	p := New(WithNullAudio())
	ffmpeg, err := p.resolveFFmpeg(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.decodeVideo(ffmpeg, clip, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	p.video = v
	t.Cleanup(p.Stop)
	// Before where the decode began nothing is due, so nothing shows: the
	// first frame belongs to 2 s.
	time.Sleep(300 * time.Millisecond)
	if img, changed := v.frame(time.Second); img != nil || changed {
		t.Fatalf("frame %d shown at 1 s, before the decode began", frameNumber(img))
	}
	if got := frameAt(t, v, 2*time.Second); got != 60 {
		t.Fatalf("frame %d at 2 s, want 60", got)
	}
	if got := frameAt(t, v, 2500*time.Millisecond); got != 75 {
		t.Fatalf("frame %d at 2.5 s, want 75", got)
	}
}

func TestThePictureFollowsTheSound(t *testing.T) {
	// The null device plays twenty times faster than real time, so the tone
	// is long and the picture lags it; what is checked is that the picture
	// moves with the sound, not that it keeps up.
	p, wav := newTestPlayerWithDuration(t, 120)
	clip := numberedClip(t, 6)
	if err := p.Play(wav); err != nil {
		t.Fatal(err)
	}
	if err := p.SetVideo(clip); err != nil {
		t.Fatal(err)
	}
	if p.VideoURL() != clip {
		t.Fatalf("VideoURL = %q, want the clip", p.VideoURL())
	}
	waitFor(t, "the first frame", func() bool {
		img, _ := p.Frame()
		return img != nil
	})
	// A seek keeps the picture, decoding it again from the new position: the
	// frames from then on are those from there.
	p.Seek(4 * time.Second)
	if p.VideoURL() != clip {
		t.Fatal("a seek dropped the picture")
	}
	waitFor(t, "a frame from after the seek", func() bool {
		img, _ := p.Frame()
		return frameNumber(img) >= 4*VideoFPS
	})
	if p.VideoFailure() != "" {
		t.Fatalf("VideoFailure = %q", p.VideoFailure())
	}

	// Stopping the picture leaves the sound.
	p.StopVideo()
	if p.VideoURL() != "" {
		t.Fatalf("VideoURL = %q after StopVideo", p.VideoURL())
	}
	if img, _ := p.Frame(); img != nil {
		t.Fatal("a frame after StopVideo")
	}
	if !p.Playing() {
		t.Fatal("StopVideo stopped the sound")
	}
}

func TestANewStreamDropsThePicture(t *testing.T) {
	p, wav := newTestPlayerWithDuration(t, 3)
	clip := numberedClip(t, 3)
	if err := p.Play(wav); err != nil {
		t.Fatal(err)
	}
	if err := p.SetVideo(clip); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(filepath.Dir(wav), "other.wav")
	if out, err := exec.Command(p.ffmpegPath, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=220:duration=3", other).CombinedOutput(); err != nil {
		t.Fatalf("making a tone: %v: %s", err, out)
	}
	if err := p.Play(other); err != nil {
		t.Fatal(err)
	}
	if p.VideoURL() != "" {
		t.Fatalf("VideoURL = %q after a new stream, want none", p.VideoURL())
	}
	p.Stop()
	if err := p.SetVideo(clip); err != nil {
		t.Fatal(err)
	}
	if p.VideoURL() != "" {
		t.Fatal("a picture was set with no stream loaded")
	}
}

func TestAnUnreadablePictureFails(t *testing.T) {
	p, wav := newTestPlayerWithDuration(t, 3)
	if err := p.Play(wav); err != nil {
		t.Fatal(err)
	}
	if err := p.SetVideo(filepath.Join(t.TempDir(), "missing.mp4")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the failure", func() bool { return p.VideoFailure() != "" })
	if img, _ := p.Frame(); img != nil {
		t.Fatal("a frame from a missing file")
	}
	if !p.Playing() {
		t.Fatal("a failed picture stopped the sound")
	}
}

func TestVideoArgs(t *testing.T) {
	args := videoArgs("https://media.test/video", 2*time.Second, true)
	for _, want := range []string{"-an", "rawvideo", "rgba", "-request_size", "-user_agent"} {
		if !slices.Contains(args, want) {
			t.Errorf("videoArgs lacks %q: %v", want, args)
		}
	}
	if i := slices.Index(args, "-ss"); i < 0 || args[i+1] != "2.000" || slices.Index(args, "-i") < i {
		t.Errorf("videoArgs does not seek the input to 2 s: %v", args)
	}
	if slices.Contains(args, "-vn") {
		t.Errorf("videoArgs drops the picture: %v", args)
	}
}
