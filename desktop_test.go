package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/youtube"
)

// newSettingsTester builds the app view at settings size, settled.
func newSettingsTester(a *app) *ui.Tester {
	tt := ui.NewTester(a.view, 1000, 900)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	tt.Frame()
	return tt
}

// queueOf makes a queue of numbered tracks.
func queueOf(ids ...string) []youtube.MusicItem {
	queue := make([]youtube.MusicItem, len(ids))
	for i, id := range ids {
		queue[i] = youtube.MusicItem{VideoID: id, Title: "Track " + id}
	}
	return queue
}

// idsOf names the queue order.
func idsOf(queue []youtube.MusicItem) []string {
	ids := make([]string, len(queue))
	for i, item := range queue {
		ids[i] = item.VideoID
	}
	return ids
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestReorderQueueMovesOneTrackDown(t *testing.T) {
	moved, index := reorderQueue(queueOf("a", "b", "c", "d"), []int{1}, 3, "a")
	if got, want := idsOf(moved), []string{"a", "c", "b", "d"}; !equalIDs(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if index != 0 {
		t.Fatalf("current index = %d, want 0", index)
	}
}

func TestReorderQueueMovesBlockUp(t *testing.T) {
	moved, index := reorderQueue(queueOf("a", "b", "c", "d"), []int{2, 3}, 0, "c")
	if got, want := idsOf(moved), []string{"c", "d", "a", "b"}; !equalIDs(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if index != 0 {
		t.Fatalf("current index = %d, want 0", index)
	}
}

func TestReorderQueueFollowsTheCurrentTrack(t *testing.T) {
	moved, index := reorderQueue(queueOf("a", "b", "c"), []int{0}, 3, "a")
	if got, want := idsOf(moved), []string{"b", "c", "a"}; !equalIDs(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if index != 2 {
		t.Fatalf("current index = %d, want 2", index)
	}
}

func TestReorderQueueIgnoresBadRows(t *testing.T) {
	moved, index := reorderQueue(queueOf("a", "b"), []int{-1, 7, 0, 0}, 2, "b")
	if got, want := idsOf(moved), []string{"b", "a"}; !equalIDs(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if index != 0 {
		t.Fatalf("current index = %d, want 0", index)
	}
}

func TestReorderQueueReportsMissingCurrent(t *testing.T) {
	_, index := reorderQueue(queueOf("a", "b"), []int{0}, 1, "gone")
	if index != -1 {
		t.Fatalf("current index = %d, want -1", index)
	}
}

func TestMoveQueueKeepsPlaybackState(t *testing.T) {
	a := newTestApp()
	a.queue = queueOf("a", "b", "c", "d")
	a.index = 1
	a.current = youtube.MusicItem{VideoID: "b", Title: "Track b"}
	a.recommendationStart = 3
	a.recommendationIDs = map[string]struct{}{"d": {}}
	a.moveQueue([]int{0}, 2)
	if got, want := idsOf(a.queue), []string{"b", "a", "c", "d"}; !equalIDs(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if a.index != 0 {
		t.Fatalf("current index = %d, want 0", a.index)
	}
	if a.current.VideoID != "b" {
		t.Fatalf("current = %q, want b", a.current.VideoID)
	}
	if a.recommendationStart != 3 {
		t.Fatalf("recommendation start = %d, want 3", a.recommendationStart)
	}
}

func TestMoveQueueSkipsTheRecommendationDivider(t *testing.T) {
	a := newTestApp()
	a.queue = queueOf("a", "b", "c")
	a.index = 0
	a.current = youtube.MusicItem{VideoID: "a"}
	a.recommendationStart = 2
	a.recommendationIDs = map[string]struct{}{"c": {}}
	// Rows 0, 1 and 3 are tracks; row 2 is the divider.
	a.moveQueue([]int{0, 2}, 4)
	if got, want := idsOf(a.queue), []string{"b", "c", "a"}; !equalIDs(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if a.recommendationStart != 1 {
		t.Fatalf("recommendation start = %d, want 1", a.recommendationStart)
	}
}

func TestParseAppURL(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		action string
		value  string
		ok     bool
	}{
		{"meiro://play?v=abc123", "play", "abc123", true},
		{"meiro://search?q=ambient", "search", "ambient", true},
		{"meiro://search?q=", "", "", false},
		{"meiro://play", "", "", false},
		{"meiro://open?x=1", "", "", false},
		{"https://music.youtube.com/watch?v=abc", "", "", false},
		{"not a url at all", "", "", false},
		{"MEIRO://play?v=xyz", "play", "xyz", true},
	} {
		link, ok := parseAppURL(tc.raw)
		if ok != tc.ok || link.action != tc.action || link.value != tc.value {
			t.Errorf("parseAppURL(%q) = %+v, %v; want action %q value %q ok %v", tc.raw, link, ok, tc.action, tc.value, tc.ok)
		}
	}
}

func TestSummarizeHistory(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	entries := []historyEntry{
		{VideoID: "a", Title: "Alpha", Artist: "Ann", PlayedAt: now.Add(-time.Hour)},
		{VideoID: "a", Title: "Alpha", Artist: "Ann", PlayedAt: now.Add(-2 * time.Hour)},
		{VideoID: "b", Title: "Beta", Artist: "Bob", PlayedAt: now.AddDate(0, 0, -3)},
		{VideoID: "c", Title: "Gamma", Artist: "Ann", PlayedAt: now.AddDate(0, 0, -40)},
	}
	summary := summarizeHistory(entries, now, 14)
	if summary.totalPlays != 3 {
		t.Fatalf("total plays = %d, want 3", summary.totalPlays)
	}
	if summary.totalArtists != 2 {
		t.Fatalf("artists = %d, want 2", summary.totalArtists)
	}
	if len(summary.byDay) != 14 {
		t.Fatalf("days = %d, want 14", len(summary.byDay))
	}
	if summary.byDay[13].plays != 2 || summary.byDay[10].plays != 1 {
		t.Fatalf("daily counts wrong: today=%d, three-days-ago=%d", summary.byDay[13].plays, summary.byDay[10].plays)
	}
	if len(summary.topTracks) == 0 || summary.topTracks[0].title != "Alpha" || summary.topTracks[0].plays != 2 {
		t.Fatalf("top tracks = %+v", summary.topTracks)
	}
	if len(summary.topArtists) == 0 || summary.topArtists[0].title != "Ann" || summary.topArtists[0].plays != 2 {
		t.Fatalf("top artists = %+v", summary.topArtists)
	}
}

func TestSettingsKeepsDesktopChoices(t *testing.T) {
	s := defaultSettings()
	if !s.NotifyTrack || !s.ShowTray {
		t.Fatalf("defaults = %+v, want notifications and tray on", s)
	}
	s.GlobalShortcut = true
	s.OpenAtLogin = true
	clone := s.clone()
	if !clone.GlobalShortcut || !clone.OpenAtLogin {
		t.Fatalf("clone lost desktop choices: %+v", clone)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := s.save(path); err != nil {
		t.Fatal(err)
	}
	roundTripped := loadSettings(path)
	if !roundTripped.GlobalShortcut || !roundTripped.OpenAtLogin || !roundTripped.NotifyTrack || !roundTripped.ShowTray {
		t.Fatalf("round trip lost desktop choices: %+v", roundTripped)
	}
}

func TestHistoryPersistsAcrossLoads(t *testing.T) {
	a := newTestApp()
	a.historyPath = filepath.Join(t.TempDir(), "history.json")
	a.recordHistory(youtube.MusicItem{VideoID: "a", Title: "Alpha", Subtitle: "Ann"})
	a.recordHistory(youtube.MusicItem{VideoID: "b", Title: "Beta", Subtitle: "Bob"})
	if len(a.history) != 2 {
		t.Fatalf("history = %d entries, want 2", len(a.history))
	}
	b := newTestApp()
	b.historyPath = a.historyPath
	b.loadHistory()
	if len(b.history) != 2 || b.history[0].VideoID != "a" || b.history[1].Artist != "Bob" {
		t.Fatalf("reloaded history = %+v", b.history)
	}
	// Without a path, tests keep history in memory and touch no files.
	c := newTestApp()
	if c.historyFile() != "" {
		t.Fatalf("history file = %q, want empty without a window", c.historyFile())
	}
}

func TestSettingsPageShowsDesktopAndUpdates(t *testing.T) {
	a := newTestApp()
	a.router.Push("/settings")
	tt := newSettingsTester(a)
	for _, text := range []string{
		"Desktop", "Track notifications", "Menu bar icon", "Global shortcut", "Open at login",
		"Updates", "Listening stats",
	} {
		if !tt.HasText(text) {
			t.Errorf("settings page does not show %q: %q", text, tt.Texts())
		}
	}
}

func TestStatsPageShowsChartAndTops(t *testing.T) {
	a := newTestApp()
	a.router.Push("/stats")
	now := time.Now()
	a.history = []historyEntry{
		{VideoID: "a", Title: "Alpha", Artist: "Ann", PlayedAt: now.Add(-time.Hour)},
		{VideoID: "a", Title: "Alpha", Artist: "Ann", PlayedAt: now.Add(-2 * time.Hour)},
		{VideoID: "b", Title: "Beta", Artist: "Bob", PlayedAt: now.AddDate(0, 0, -2)},
	}
	a.historyLoaded = true
	tt := newSettingsTester(a)
	for _, text := range []string{
		"Listening stats", "Plays per day", "Most played", "Top artists", "Alpha", "2 plays",
	} {
		if !tt.HasText(text) {
			t.Errorf("stats page does not show %q: %q", text, tt.Texts())
		}
	}
}
