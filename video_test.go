package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/youtube"
)

// videoApp is a playbackApp whose tracks play a tone and whose pictures are a
// short clip, both made with ffmpeg.
func videoApp(t *testing.T) (*app, string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	tone := filepath.Join(dir, "tone.wav")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=duration=60", tone).CombinedOutput(); err != nil {
		t.Fatalf("making a tone: %v: %s", err, out)
	}
	// The clip is as long as the tone: the null device plays twenty times
	// faster than real time, so the sound is well along by the time a test
	// asks for the picture, and a shorter clip would have ended by then.
	clip := filepath.Join(dir, "clip.mp4")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=320x180:rate=10:duration=60",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", clip).CombinedOutput(); err != nil {
		t.Fatalf("making a clip: %v: %s", err, out)
	}
	a, r := playbackApp(t)
	r.file = tone
	a.video.streams = audio.NewURLs(func(context.Context, string) (string, time.Duration, error) {
		return clip, 0, nil
	})
	return a, clip
}

// pumpFrames runs the frame pump until a frame of the picture has been
// drawn, as the ticker would.
func pumpFrames(t *testing.T, a *app) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !a.video.hasFrame {
		if time.Now().After(deadline) {
			t.Fatalf("no frame came; video error %q, player failure %q", a.video.err, a.player.VideoFailure())
		}
		a.pumpVideo()
		time.Sleep(10 * time.Millisecond)
	}
}

func TestThePlayerShowsAVideoWhenAsked(t *testing.T) {
	a, clip := videoApp(t)
	q := []youtube.MusicItem{{VideoID: "v1", Title: "A music video", Kind: youtube.KindVideo}, {VideoID: "s1", Title: "A song"}}
	a.play(q[0], q)
	if !a.player.Active() {
		t.Fatal("the track never reached the player")
	}
	tt := ui.NewTester(a.view, 1200, 800)
	a.openPlayer(tabQueue, false)
	afterAnimations(tt)
	// Audio is the default: the choice shows, and nothing decodes.
	if _, ok := tt.Find("Audio"); !ok {
		t.Fatalf("a music video offers no Audio/Video choice: %q", tt.Texts())
	}
	if a.player.VideoURL() != "" {
		t.Fatal("the picture decodes before it is asked for")
	}

	if err := tt.Click("Video"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.settings.ShowVideo {
		t.Fatal("choosing Video did not keep the choice")
	}
	if a.player.VideoURL() != clip {
		t.Fatalf("the player's picture is %q, want the clip", a.player.VideoURL())
	}
	pumpFrames(t, a)
	tt.Frame()
	if a.video.err != "" {
		t.Fatalf("video error %q", a.video.err)
	}

	// Back to the artwork: the picture stops, the sound goes on.
	if err := tt.Click("Audio"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.ShowVideo || a.player.VideoURL() != "" || a.video.hasFrame {
		t.Fatalf("choosing Audio left the picture: setting %v, url %q, frame %v", a.settings.ShowVideo, a.player.VideoURL(), a.video.hasFrame)
	}
	if !a.player.Playing() {
		t.Fatal("stopping the picture stopped the sound")
	}

	// A song has no picture to choose.
	a.startTrack(q[1], false)
	tt.Frame()
	if _, ok := tt.Find("Audio"); ok {
		t.Fatalf("a song offers an Audio/Video choice: %q", tt.Texts())
	}
}

func TestThePictureFillsTheWindowWhenAsked(t *testing.T) {
	a, _ := videoApp(t)
	a.settings.ShowVideo = true
	q := []youtube.MusicItem{{VideoID: "v1", Title: "A music video", Kind: youtube.KindVideo}, {VideoID: "s1", Title: "A song"}}
	a.play(q[0], q)
	tt := ui.NewTester(a.view, 1200, 800)
	a.openPlayer(tabQueue, false)
	afterAnimations(tt)
	pumpFrames(t, a)
	tt.Frame()

	if err := tt.Click("Full screen"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.nowPlaying.fullscreen {
		t.Fatal("Full screen did not fill the window")
	}
	// The player and the shell are gone; the controls lie over the picture.
	if tt.HasText("Now playing") || tt.HasText("Up next") {
		t.Fatalf("the player still shows behind the full-screen picture: %q", tt.Texts())
	}
	for _, label := range []string{"Leave full screen", "A music video", "Position", "Pause"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("no %q over the full-screen picture: %q", label, tt.Texts())
		}
	}

	// The controls go once the pointer has rested, and the pointer with
	// them; a move brings them back.
	tt.Advance(controlsLinger)
	tt.Frame()
	if _, ok := tt.Find("Leave full screen"); ok {
		t.Fatalf("the controls stay over the picture after the pointer rested: %q", tt.Texts())
	}
	if tt.Cursor() != ui.CursorNone {
		t.Fatalf("the pointer shows over the picture while the controls are gone: %v", tt.Cursor())
	}
	tt.Move(600, 400)
	tt.Frame()
	if _, ok := tt.Find("Leave full screen"); !ok {
		t.Fatalf("moving the pointer did not bring the controls back: %q", tt.Texts())
	}

	// Escape puts the picture back in the player, which stays open; only
	// the next Escape closes it.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.nowPlaying.fullscreen || !a.nowPlaying.open || a.nowPlaying.closing {
		t.Fatalf("Escape did not put the picture back: fullscreen %v, open %v, closing %v", a.nowPlaying.fullscreen, a.nowPlaying.open, a.nowPlaying.closing)
	}
	if !tt.HasText("Now playing") {
		t.Fatalf("the player did not come back: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !a.nowPlaying.closing {
		t.Fatal("the second Escape did not close the player")
	}

	// A track without a picture takes the window back.
	afterAnimations(tt)
	a.openPlayer(tabQueue, false)
	afterAnimations(tt)
	if err := tt.Click("Full screen"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	a.startTrack(q[1], false)
	a.poll()
	tt.Frame()
	if a.nowPlaying.fullscreen || !tt.HasText("Now playing") {
		t.Fatalf("a song left the window to the picture: fullscreen %v, texts %q", a.nowPlaying.fullscreen, tt.Texts())
	}
}

func TestClosingThePlayerStopsThePicture(t *testing.T) {
	a, clip := videoApp(t)
	a.settings.ShowVideo = true
	q := []youtube.MusicItem{{VideoID: "v1", Title: "A music video", Kind: youtube.KindVideo}}
	a.play(q[0], q)
	// With the player closed nothing decodes, even though the picture is
	// chosen: there is nowhere to show it.
	a.poll()
	if a.player.VideoURL() != "" {
		t.Fatal("the picture decodes with the player closed")
	}
	tt := ui.NewTester(a.view, 1200, 800)
	a.openPlayer(tabQueue, false)
	afterAnimations(tt)
	if a.player.VideoURL() != clip {
		t.Fatalf("opening the player did not start the picture: %q", a.player.VideoURL())
	}
	pumpFrames(t, a)

	a.nowPlaying.closing = true
	afterAnimations(tt)
	if a.nowPlaying.open {
		t.Fatal("the player did not close")
	}
	a.pumpVideo()
	if a.player.VideoURL() != "" || a.video.hasFrame {
		t.Fatalf("closing the player left the picture: url %q, frame %v", a.player.VideoURL(), a.video.hasFrame)
	}
	if !a.player.Playing() {
		t.Fatal("closing the player stopped the sound")
	}
}

func TestAnUnavailableVideoSaysSo(t *testing.T) {
	a, _ := videoApp(t)
	a.settings.ShowVideo = true
	a.video.streams = audio.NewURLs(func(context.Context, string) (string, time.Duration, error) {
		return filepath.Join(t.TempDir(), "missing.mp4"), 0, nil
	})
	q := []youtube.MusicItem{{VideoID: "v1", Title: "A music video", Kind: youtube.KindVideo}}
	a.play(q[0], q)
	tt := ui.NewTester(a.view, 1200, 800)
	a.openPlayer(tabQueue, false)
	afterAnimations(tt)
	waitFor(t, func() bool { return a.player.VideoFailure() != "" })
	a.pumpVideo()
	tt.Frame()
	if !tt.HasText("The video stopped playing.") {
		t.Fatalf("the failed picture is not explained: %q", tt.Texts())
	}
	if !a.player.Playing() {
		t.Fatal("a failed picture stopped the sound")
	}
}
