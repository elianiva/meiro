package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/youtube"
)

func waitForAudioEnd(t *testing.T, a *app) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !a.player.Ended() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !a.player.Ended() {
		t.Fatal("the track did not finish")
	}
}

func TestExcludeAIGeneratedSettingIsSearchableAndSaved(t *testing.T) {
	a := newTestApp(t)
	a.saver.Path = filepath.Join(t.TempDir(), "settings.json")
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()

	if err := tt.Click("Search settings"); err != nil {
		t.Fatal(err)
	}
	tt.Type("artificial intelligence")
	tt.Frame()
	if !tt.HasText("Exclude AI-generated songs") {
		t.Fatalf("the AI filter is not in Settings search: %q", tt.Texts())
	}
	if err := tt.Click("Exclude AI-generated songs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.settings.ExcludeAIGenerated {
		t.Fatal("the switch did not enable the AI filter")
	}
	a.saver.Wait()
	if !settings.Load(a.saver.Path).ExcludeAIGenerated {
		t.Error("the AI filter was not saved")
	}
}

func TestAIFilterWaitsForTheNextTrackBeforeHandlingAnotherEnd(t *testing.T) {
	resetFakes()
	a, r := tonesApp(t, 1)
	a.settings.ExcludeAIGenerated = true
	a.router.Push("/playlist/PL_songs")
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()
	if err := tt.Click("Ameiro"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	drain := deferredRunner(a)
	waitForAudioEnd(t, a)
	a.poll()
	if !a.aiCheck.busy() || a.queue.index != 1 {
		t.Fatalf("first poll: checking=%v index=%d", a.aiCheck.busy(), a.queue.index)
	}
	for range 3 {
		a.poll()
		tt.Frame()
	}
	if a.queue.index != 1 || !a.loading() {
		t.Errorf("pending lookup: index=%d loading=%v; want index 1 and loading", a.queue.index, a.loading())
	}
	drain() // finish the label lookup
	drain() // start its audio
	if a.current.VideoID != "song-two" || !r.asked("song-two") || r.asked("song-three") {
		t.Errorf("after lookup: current=%s resolved=%v; want song-two", a.current.VideoID, r.ids)
	}
	if len(a.history) != 2 || a.history[1].VideoID != "song-two" {
		t.Errorf("history=%v; want only song-one and song-two", a.history)
	}
}

func TestEnablingAIFilterCancelsAnExcludedPreload(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "decoded", true: "in flight"}[pending], func(t *testing.T) {
			resetFakes()
			a, _ := tonesApp(t, 30)
			a.saver.Path = filepath.Join(t.TempDir(), "settings.json")
			// A previous playback learned the label while the filter was on.
			a.rememberAIDisclosure("song-two", youtube.AIAlteredOrGenerated)
			a.router.Push("/playlist/PL_songs")
			tt := ui.NewTester(a.view, 1000, 900)
			tt.Frame()
			if err := tt.Click("Ameiro"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			run := a.run
			var drain func()
			if pending {
				drain = deferredRunner(a)
			}
			pollUntil(t, a, "song-one playing", func() bool { return a.player.Playing() && !a.loading() })
			if pending {
				pollUntil(t, a, "the preload to start", a.preload.busy)
			} else {
				pollUntil(t, a, "song-two preloaded", func() bool { return a.player.Preloaded() == "song-two" })
			}
			tt.Frame()
			if err := tt.Click("Pause"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			a.router.Push("/settings")
			tt.Frame()
			if err := tt.Click("Search settings"); err != nil {
				t.Fatal(err)
			}
			tt.Type("artificial intelligence")
			tt.Frame()
			if err := tt.Click("Exclude AI-generated songs"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if a.queue.find("song-two") >= 0 {
				t.Fatal("the marked future track was not removed")
			}
			if a.preload.busy() || a.player.Preloaded() != "" {
				t.Errorf("excluded preload survived: busy=%v held=%s", a.preload.busy(), a.player.Preloaded())
			}
			if pending {
				drain()
				a.run = run
			}
			if err := tt.Click("Play"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			waitForAudioEnd(t, a)
			if tag, ok := a.player.Advanced(); ok {
				t.Errorf("the player actually advanced to excluded audio: %s", tag)
			}
			pollUntil(t, a, "song-three to play", func() bool { return a.current.VideoID == "song-three" })
			if len(a.history) != 2 || a.history[1].VideoID != "song-three" {
				t.Errorf("history=%v; the excluded song must not play", a.history)
			}
		})
	}
}

func TestAIFilterDropsSupersededLookups(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(map[bool]string{false: "another song", true: "queue ends"}[stop], func(t *testing.T) {
			resetFakes()
			a, r := playbackApp(t)
			a.settings.ExcludeAIGenerated = true
			a.router.Push("/album/MPREb_ai")
			tt := ui.NewTester(a.view, 1000, 900)
			tt.Frame()
			drain := deferredRunner(a)
			first, second := "AI Track", "Human Track"
			if stop {
				first, second = "Human Track", "Next"
			}
			if err := tt.Click(first); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if !a.loading() || !tt.HasText("Loading") {
				t.Error("the selected song does not show its pending AI check")
			}
			if err := tt.Click(second); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			drain()
			drain()
			if stop {
				if a.starting() || a.player.Active() || len(r.ids) != 0 {
					t.Errorf("cancelled lookup restarted playback: starting=%v active=%v resolved=%v", a.starting(), a.player.Active(), r.ids)
				}
			} else if a.current.VideoID != "human-track" || !r.asked("human-track") || r.asked("ai-track") {
				t.Errorf("superseded lookup played: current=%s resolved=%v", a.current.VideoID, r.ids)
			}
		})
	}
}

func TestAIFilterPlaysWhenTheDisclosureRequestFails(t *testing.T) {
	resetFakes()
	a, r := playbackApp(t)
	s := &switchable{}
	a.account.public = youtube.NewClient(youtube.Options{HTTPClient: &http.Client{Transport: s}})
	a.settings.ExcludeAIGenerated = true
	a.router.Push("/album/MPREb_ai")
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()
	s.mode = "server"
	if err := tt.Click("Human Track"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.current.VideoID != "human-track" || !r.asked("human-track") || a.starting() || a.playErr != "" {
		t.Errorf("failed lookup blocked playback: current=%s resolved=%v error=%s", a.current.VideoID, r.ids, a.playErr)
	}
	if a.aiDisclosure(a.current) != youtube.AIUnknown {
		t.Error("failed lookup was remembered as a definitive label")
	}
}

func TestAIFilterBoundsOnlyNewRecommendationBatches(t *testing.T) {
	for _, humanBatch := range []int{0, 2} {
		t.Run(map[int]string{0: "all AI ends", 2: "human in second batch plays"}[humanBatch], func(t *testing.T) {
			resetFakes()
			for _, id := range []string{"ai-one", "ai-two", "ai-three", "ai-four", "human"} {
				markFakeAI(id)
			}
			a, r := playbackApp(t)
			music := &aiQueueMusic{humanBatch: humanBatch}
			a.account.public = youtube.NewClient(youtube.Options{HTTPClient: &http.Client{Transport: music}})
			a.settings.ExcludeAIGenerated, a.settings.AutoPlay = true, true
			a.router.Push("/album/MPREb_ai_run")
			tt := ui.NewTester(a.view, 1000, 900)
			tt.Frame()
			if err := tt.Click("ai-one"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if humanBatch == 0 {
				if music.batches == 0 || music.batches >= 4 || a.upNext.waiting || a.starting() || a.player.Active() || len(r.ids) != 0 {
					t.Errorf("AI-only recommendations were not bounded: batches=%d waiting=%v starting=%v active=%v resolved=%v", music.batches, a.upNext.waiting, a.starting(), a.player.Active(), r.ids)
				}
			} else if a.current.VideoID != "rec-2-4" || !r.asked("rec-2-4") {
				t.Errorf("allowed recommendation did not play: batches=%d current=%s resolved=%v", music.batches, a.current.VideoID, r.ids)
			}
		})
	}
}

func TestAIFilterScansTheQueuePastFourMarkedSongs(t *testing.T) {
	for _, test := range []struct {
		name   string
		allAI  bool
		repeat repeatMode
	}{
		{name: "human after four AI songs"},
		{name: "all AI ends", allAI: true},
		{name: "all AI with repeat queue ends", allAI: true, repeat: repeatQueue},
		{name: "all AI with repeat track ends", allAI: true, repeat: repeatTrack},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetFakes()
			for _, id := range []string{"ai-one", "ai-two", "ai-three", "ai-four"} {
				markFakeAI(id)
			}
			if test.allAI {
				markFakeAI("human")
			}
			a, r := playbackApp(t)
			a.settings.ExcludeAIGenerated = true
			a.repeat = test.repeat
			a.router.Push("/album/MPREb_ai_run")
			tt := ui.NewTester(a.view, 1000, 900)
			tt.Frame()
			if err := tt.Click("ai-one"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if got := aiChecks.Load(); got != 5 {
				t.Errorf("AI lookups=%d; want each of the five tracks checked once", got)
			}
			if test.allAI {
				if a.player.Active() || a.aiCheck.busy() || len(r.ids) != 0 {
					t.Errorf("all-AI queue kept playing: active=%v checking=%v resolved=%v", a.player.Active(), a.aiCheck.busy(), r.ids)
				}
			} else if a.current.VideoID != "human" || !r.asked("human") {
				t.Errorf("current=%s resolved=%v; want human", a.current.VideoID, r.ids)
			}
		})
	}
}

func TestExcludingAIGeneratedSongsSkipsThemBeforeAudio(t *testing.T) {
	for _, test := range []struct {
		name    string
		exclude bool
	}{
		{"off plays the track", false},
		{"on skips the track", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetFakes()
			markFakeAI("ai-track")
			a, r := playbackApp(t)
			a.settings.ExcludeAIGenerated = test.exclude
			a.router.Push("/album/MPREb_ai")
			tt := ui.NewTester(a.view, 1000, 700)
			tt.Frame()

			if err := tt.Click("AI Track"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if test.exclude {
				if a.current.VideoID != "human-track" {
					t.Fatalf("current = %q, want the unmarked track after the skip", a.current.VideoID)
				}
				if r.asked("ai-track") {
					t.Error("the labelled track's audio was resolved")
				}
				if !r.asked("human-track") {
					t.Error("the unmarked track after it was not played")
				}
				if got := ids(a.page.playable); !slices.Equal(got, []string{"human-track"}) {
					t.Errorf("page queue = %v, want only the unmarked track", got)
				}
			} else {
				if a.current.VideoID != "ai-track" || !r.asked("ai-track") {
					t.Errorf("current %q, resolver asked %v; want the labelled track to play with the filter off", a.current.VideoID, r.asked("ai-track"))
				}
				if got := aiChecks.Load(); got != 0 {
					t.Errorf("the filter off still made %d AI lookups", got)
				}
			}
		})
	}
}
