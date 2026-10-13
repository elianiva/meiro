package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/elianiva/meiro/internal/playstate"
	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/youtube"
)

func TestTheQueueComesBackPausedAfterARestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "playback.json")

	first, _ := playbackApp(t)
	first.playSave.writer.Path = path
	first.saver.Path = filepath.Join(dir, "settings.json")
	var q []youtube.MusicItem
	for i := range 300 {
		q = append(q, youtube.MusicItem{VideoID: fmt.Sprintf("v%d", i), Title: fmt.Sprintf("Track %d", i), Duration: "3:00"})
	}
	first.playFrom(q[250], q, 250, youtube.UpNextOptions{}, "My mix")
	first.queue.insert(youtube.MusicItem{VideoID: "next-up", Title: "Next up"}, true, false)
	first.setVolume(35)
	first.saveSettings()
	first.persistPlayback()
	first.playSave.writer.Wait()
	first.saver.Wait()

	second, r := playbackApp(t)
	second.settings = settings.Load(filepath.Join(dir, "settings.json"))
	second.restorePlayback(path)

	if second.settings.Volume != 35 {
		t.Errorf("volume came back as %v", second.settings.Volume)
	}
	if n := second.queue.len(); n != playstate.MaxItems {
		t.Errorf("%d tracks came back, want the cap of %d", n, playstate.MaxItems)
	}
	if second.current.VideoID != "v250" || second.queue.current().VideoID != "v250" {
		t.Errorf("current track is %q", second.current.VideoID)
	}
	if next, ok := second.queue.next(false, repeatOff); !ok || second.queue.at(next).VideoID != "next-up" {
		t.Errorf("the play-next track did not come first: %v", ids(second.queue.items[second.queue.index:min(second.queue.index+3, second.queue.len())]))
	}
	if second.queue.source != "My mix" || second.total != 3*time.Minute {
		t.Errorf("source %q, total %v", second.queue.source, second.total)
	}
	if second.player.Active() || second.stream.busy() || r.asked("v250") {
		t.Error("restoring started playing")
	}

	// Pressing play plays the restored track.
	second.togglePlay()
	if !r.asked("v250") || second.current.VideoID != "v250" {
		t.Errorf("play did not start the restored track: %v", r.ids)
	}
}

func TestPlaybackResumesWhereItWasLeft(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	tone := filepath.Join(t.TempDir(), "tone.wav")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=duration=30", tone).CombinedOutput(); err != nil {
		t.Fatalf("making a tone: %v: %s", err, out)
	}
	path := filepath.Join(t.TempDir(), "playback.json")
	q := []youtube.MusicItem{{VideoID: "a", Duration: "0:30"}, {VideoID: "b", Duration: "0:30"}}

	first, r := playbackApp(t)
	r.file = tone
	first.playSave.writer.Path = path
	first.play(q[1], q)
	first.seek(12 * time.Second)
	first.persistPlayback()
	first.playSave.writer.Wait()

	second, r := playbackApp(t)
	r.file = tone
	second.restorePlayback(path)
	if got := second.scrub; got < 12 || got > 14 {
		t.Fatalf("the slider restored at %vs, want about 12s", got)
	}
	second.togglePlay()
	deadline := time.Now().Add(10 * time.Second)
	for !second.player.Playing() && time.Now().Before(deadline) {
		second.poll()
		time.Sleep(10 * time.Millisecond)
	}
	if !second.player.Playing() {
		t.Fatal("the restored track did not play")
	}
	if got := second.player.Position(); got < 12*time.Second {
		t.Errorf("playing from %v, want from 12s on", got)
	}
}

func TestPersistingSkipsWhatIsSavedAndNoticesWhatChanged(t *testing.T) {
	a, _ := playbackApp(t)
	a.playSave.writer.Path = filepath.Join(t.TempDir(), "playback.json")
	q := tracks("a", "b", "c")
	a.play(q[0], q)
	a.persistPlayback()
	a.playSave.writer.Wait()
	before := a.playSave.last

	a.persistPlayback() // nothing changed: no new write is asked for
	a.queue.shift(2, -1)
	a.persistPlayback()
	if a.playSave.last == before {
		t.Error("a reordered queue was not noticed")
	}
	a.playSave.writer.Wait()
	got := playstate.Load(a.playSave.writer.Path)
	if want := []string{"a", "c", "b"}; fmt.Sprint(ids(got.Items)) != fmt.Sprint(want) {
		t.Errorf("saved %v, want %v", ids(got.Items), want)
	}
}

func TestNothingIsSavedForAnEmptyQueue(t *testing.T) {
	a, _ := playbackApp(t)
	path := filepath.Join(t.TempDir(), "playback.json")
	a.playSave.writer.Path = path
	a.savePlayback(0)
	a.playSave.writer.Wait()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("an empty queue wrote a file: %v", err)
	}
	a.restorePlayback(path) // a missing file leaves the app as it was
	if a.queue.len() != 0 || a.current.VideoID != "" {
		t.Errorf("restored %v from nothing", ids(a.queue.items))
	}
}

func TestARestoredTrackThatCannotPlayAnyMoreIsSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playback.json")
	first, _ := playbackApp(t)
	first.playSave.writer.Path = path
	q := tracks("gone", "b")
	first.play(q[0], q)
	first.savePlayback(0)
	first.playSave.writer.Wait()

	second, r := playbackApp(t)
	r.err["gone"] = errors.New("Video unavailable")
	second.restorePlayback(path)
	if second.current.VideoID != "gone" || second.playErr != "" {
		t.Fatalf("restoring: current %q, error %q", second.current.VideoID, second.playErr)
	}
	second.togglePlay()
	// Playback skips what cannot play, as for any track, and goes on.
	if second.current.VideoID != "b" || !second.player.Active() {
		t.Errorf("playing a removed track: current %q, active %v, error %q", second.current.VideoID, second.player.Active(), second.playErr)
	}
	if second.queue.len() != 2 {
		t.Errorf("the queue lost tracks: %v", ids(second.queue.items))
	}
}

func TestExitKeepsTheLastChangeWithoutWaitingOutTheDebounce(t *testing.T) {
	a, _ := playbackApp(t)
	path := filepath.Join(t.TempDir(), "playback.json")
	a.playSave.writer.Path = path
	q := tracks("a", "b", "c")
	a.play(q[0], q)
	a.persistPlayback() // handed to the debounced writer, not yet on disk
	a.queue.insert(youtube.MusicItem{VideoID: "late"}, false, false)

	start := time.Now()
	a.savePlayback(0) // what shutdown does
	a.playSave.writer.Wait()
	if took := time.Since(start); took > time.Second {
		t.Errorf("flushing at exit took %v; it waited out the debounce", took)
	}
	if got := playstate.Load(path); fmt.Sprint(ids(got.Items)) != "[a b c late]" {
		t.Errorf("the last change was lost: %v", ids(got.Items))
	}
}
