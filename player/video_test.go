package player

import (
	"context"
	"image"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// clipFPS is the frame rate of most test clips.
const clipFPS = 30

// numberedClip makes a clip of seconds at fps whose every frame is one
// flat colour: its red and green channels are the frame's number, so a
// decoded frame says where in the clip it comes from.
func numberedClip(t *testing.T, seconds, fps int) string {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	clip := filepath.Join(t.TempDir(), "clip.mkv")
	out, err := exec.Command(ffmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "color=c=black:s=64x36:r="+strconv.Itoa(fps)+":d="+strconv.Itoa(seconds),
		"-vf", "format=rgb24,geq=r='mod(N,256)':g='floor(N/256)':b=0",
		"-c:v", "ffv1", "-pix_fmt", "gbrp", clip).CombinedOutput()
	if err != nil {
		t.Fatalf("making a clip: %v: %s", err, out)
	}
	return clip
}

// frameNumber is the number of the numberedClip frame img shows, or -1 for
// no frame. It reads the middle of the frame: scaling on the GPU darkens
// the edges by a shade.
func frameNumber(img *image.RGBA) int {
	if img == nil {
		return -1
	}
	b := img.Bounds()
	middle := img.PixOffset(b.Dx()/2, b.Dy()/2)
	return int(img.Pix[middle]) + int(img.Pix[middle+1])<<8
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
	clip := numberedClip(t, 4, clipFPS)
	p := New(WithNullAudio())
	ffmpeg, err := p.resolveFFmpeg(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.decodeVideo(ffmpeg, clip, 36, clipFPS, false, 0)
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
	if got := frameAt(t, v, 10*time.Second); got != 4*clipFPS-1 {
		t.Fatalf("frame %d past the end, want %d", got, 4*clipFPS-1)
	}
	if p.VideoFailure() != "" {
		t.Fatalf("VideoFailure = %q at a clean end", p.VideoFailure())
	}
}

func TestADecodeFromWithinTheStreamIsDueFromThere(t *testing.T) {
	clip := numberedClip(t, 4, clipFPS)
	p := New(WithNullAudio())
	ffmpeg, err := p.resolveFFmpeg(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.decodeVideo(ffmpeg, clip, 36, clipFPS, false, 2*time.Second)
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
	// moves with the sound, not that it keeps up. The clip is as long as the
	// tone: the sound is seconds in by the time the picture starts, after
	// the check of whether ffmpeg can decode on the GPU.
	p, wav := newTestPlayerWithDuration(t, 120)
	clip := numberedClip(t, 120, clipFPS)
	if err := p.Play(wav); err != nil {
		t.Fatal(err)
	}
	if err := p.SetVideo(t.Context(), clip, 36, clipFPS); err != nil {
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
		return frameNumber(img) >= 4*clipFPS
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
	clip := numberedClip(t, 3, clipFPS)
	if err := p.Play(wav); err != nil {
		t.Fatal(err)
	}
	if err := p.SetVideo(t.Context(), clip, 36, clipFPS); err != nil {
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
	// A request superseded before it reached the player sets nothing.
	superseded, cancel := context.WithCancel(t.Context())
	cancel()
	if err := p.SetVideo(superseded, clip, 36, clipFPS); err == nil || p.VideoURL() != "" {
		t.Fatalf("a superseded request: err %v, VideoURL %q", err, p.VideoURL())
	}
	p.Stop()
	if err := p.SetVideo(t.Context(), clip, 36, clipFPS); err != nil {
		t.Fatal(err)
	}
	if p.VideoURL() != "" {
		t.Fatal("a picture was set with no stream loaded")
	}
}

func TestAnUnreadablePictureFails(t *testing.T) {
	// Long enough that the sound still plays once the picture has failed on
	// the GPU, where there is one, and then on the CPU.
	p, wav := newTestPlayerWithDuration(t, 60)
	if err := p.Play(wav); err != nil {
		t.Fatal(err)
	}
	if err := p.SetVideo(t.Context(), filepath.Join(t.TempDir(), "missing.mp4"), 36, clipFPS); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the failure", func() bool {
		p.Frame() // which tries a picture that failed on the GPU on the CPU
		return p.VideoFailure() != ""
	})
	if img, _ := p.Frame(); img != nil {
		t.Fatal("a frame from a missing file")
	}
	if !p.Playing() {
		t.Fatal("a failed picture stopped the sound")
	}
}

func TestVideoArgs(t *testing.T) {
	args := videoArgs("https://media.test/video", 1080, 60, false, 2*time.Second, true)
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
	if i := slices.Index(args, "-vf"); i < 0 || !strings.HasPrefix(args[i+1], "scale=1920:1080:") || !strings.HasSuffix(args[i+1], ",fps=60") {
		t.Errorf("videoArgs does not decode onto a 1920x1080 canvas at 60 fps: %v", args)
	}
}

func TestVideoArgsOnTheGPU(t *testing.T) {
	args := videoArgs("https://media.test/video", 720, 24, true, 0, true)
	input := slices.Index(args, "-i")
	if i := slices.Index(args, "-hwaccel"); i < 0 || i > input || args[i+1] != "vulkan" {
		t.Errorf("videoArgs does not decode with Vulkan: %v", args)
	}
	if i := slices.Index(args, "-init_hw_device"); i < 0 || i > input {
		t.Errorf("videoArgs opens no Vulkan device before the input: %v", args)
	}
	i := slices.Index(args, "-vf")
	if i < 0 || !strings.HasPrefix(args[i+1], "libplacebo=w=1280:h=720:format=rgba:normalize_sar=1,hwdownload,format=rgba,") ||
		!strings.HasSuffix(args[i+1], ",fps=24") {
		t.Errorf("videoArgs does not turn the frames into 1280x720 RGBA at 24 fps on the GPU: %v", args)
	}
}

func TestAPictureThatFailsOnTheGPUShowsFromTheCPU(t *testing.T) {
	p, wav := newTestPlayerWithDuration(t, 120)
	clip := numberedClip(t, 120, clipFPS)
	if err := p.Play(wav); err != nil {
		t.Fatal(err)
	}
	// Taken to decode on the GPU whatever the machine. Without Vulkan the
	// decode fails at once; with it, ffmpeg's Vulkan FFV1 decoder ends
	// without a frame. Either way the picture must still show, from the
	// CPU, never ahead of the sound.
	p.ctl.Lock()
	p.gpuChecked, p.gpuVideo = true, true
	p.ctl.Unlock()
	if err := p.SetVideo(t.Context(), clip, 36, clipFPS); err != nil {
		t.Fatal(err)
	}
	var img *image.RGBA
	waitFor(t, "a frame", func() bool {
		img, _ = p.Frame()
		return img != nil
	})
	if got, at := frameNumber(img), int(p.Position()*clipFPS/time.Second); got < 0 || got > at {
		t.Errorf("frame %d shown with the sound at frame %d", got, at)
	}
	if failure := p.VideoFailure(); failure != "" {
		t.Errorf("VideoFailure = %q", failure)
	}
}

func TestAPictureDecodesAtItsOwnRate(t *testing.T) {
	clip := numberedClip(t, 4, 60)
	p := New(WithNullAudio())
	ffmpeg, err := p.resolveFFmpeg(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.decodeVideo(ffmpeg, clip, 36, 60, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	p.video = v
	t.Cleanup(p.Stop)
	// Every frame of a 60 fps picture comes, each at its own sixtieth of a
	// second: at 30 fps the odd ones would be dropped.
	if got := frameAt(t, v, 500*time.Millisecond); got != 30 {
		t.Fatalf("frame %d at 0.5 s, want 30", got)
	}
	if got := frameAt(t, v, 500*time.Millisecond+time.Second/60); got != 31 {
		t.Fatalf("frame %d a sixtieth of a second after 0.5 s, want 31", got)
	}
	if p.VideoFPS() != 60 {
		t.Fatalf("VideoFPS = %g, want 60", p.VideoFPS())
	}
}

func TestASharperPictureTakesOverWithoutAGap(t *testing.T) {
	p, wav := newTestPlayerWithDuration(t, 120)
	clip := numberedClip(t, 120, clipFPS)
	if err := p.Play(wav); err != nil {
		t.Fatal(err)
	}
	if err := p.SetVideo(t.Context(), clip, 36, clipFPS); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first frame", func() bool {
		img, _ := p.Frame()
		return img != nil
	})
	if err := p.SetVideo(t.Context(), clip, 72, clipFPS); err != nil {
		t.Fatal(err)
	}
	// Until the sharper picture's first frame is due, the one shown stays:
	// every frame asked for is a picture, never none, and the sizes go from
	// the old to the new once, at a frame that falls where the sound is.
	deadline := time.Now().Add(10 * time.Second)
	for {
		img, _ := p.Frame()
		if img == nil {
			t.Fatal("no picture while the sharper one was on its way")
		}
		if w := img.Bounds().Dx(); w == 128 {
			if got, at := frameNumber(img), int(p.Position()*clipFPS/time.Second); got < at-clipFPS {
				t.Fatalf("the sharper picture took over at frame %d, a second behind the sound at frame %d", got, at)
			}
			break
		} else if w != 64 {
			t.Fatalf("a frame %d wide, want 64 or 128", w)
		}
		if time.Now().After(deadline) {
			t.Fatal("the sharper picture never took over")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if p.VideoURL() != clip {
		t.Fatalf("VideoURL = %q, want the clip", p.VideoURL())
	}
	// Asking again for the picture shown starts nothing.
	if err := p.SetVideo(t.Context(), clip, 72, clipFPS); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	next := p.nextVideo
	p.mu.Unlock()
	if next != nil {
		t.Fatal("asking for the picture shown started another")
	}
	if img, _ := p.Frame(); img == nil || img.Bounds().Dx() != 128 {
		t.Fatal("the sharper picture did not stay")
	}
}

func TestAPictureThatFallsBehindCatchesUpWhileTheSoundWaits(t *testing.T) {
	// The null device plays twenty times faster than real time, so the
	// picture falls behind the sound as a stalled one does.
	p, wav := newTestPlayerWithDuration(t, 120)
	clip := numberedClip(t, 120, clipFPS)
	if err := p.Play(wav); err != nil {
		t.Fatal(err)
	}
	if err := p.SetVideo(t.Context(), clip, 36, clipFPS); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the picture to fall behind", func() bool {
		p.Frame()
		return p.VideoLag() > time.Second
	})
	// Held, the sound stays where it is, and the decoder reaches it. A
	// seek starts the decode again, which is behind until its first frame.
	p.Pause()
	for _, seek := range []bool{false, true} {
		if seek {
			p.Seek(p.Position() + 10*time.Second)
			if lag := p.VideoLag(); lag <= 0 {
				t.Fatalf("VideoLag = %s just after a seek, before any frame", lag)
			}
		}
		waitFor(t, "the picture to catch up", func() bool {
			p.Frame()
			return p.VideoLag() <= 0
		})
		img, _ := p.Frame()
		if got, at := frameNumber(img), int(p.Position()*clipFPS/time.Second); got < at-1 || got > at {
			t.Fatalf("frame %d shown with the paused sound at frame %d (seek %v)", got, at, seek)
		}
	}
	p.StopVideo()
	if lag := p.VideoLag(); lag != 0 {
		t.Fatalf("VideoLag = %s with no picture", lag)
	}
}

func TestAPictureThatEndedIsNotBehind(t *testing.T) {
	// A picture shorter than its sound shows its last frame to the end: no
	// frame is on its way, so nothing is worth waiting for.
	p, wav := newTestPlayerWithDuration(t, 120)
	clip := numberedClip(t, 1, clipFPS)
	// On the CPU, so no GPU failure starts the decode again past the end.
	p.ctl.Lock()
	p.gpuChecked, p.gpuVideo = true, false
	p.ctl.Unlock()
	if err := p.Play(wav); err != nil {
		t.Fatal(err)
	}
	if err := p.SetVideo(t.Context(), clip, 36, clipFPS); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the sound to pass the end of the picture", func() bool {
		p.Frame()
		return p.Position() > 5*time.Second && p.VideoLag() == 0
	})
	for range 20 {
		p.Frame()
		if lag := p.VideoLag(); lag != 0 {
			t.Fatalf("VideoLag = %s after the picture ended, at %s", lag, p.Position())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
