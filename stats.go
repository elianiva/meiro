package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// maxHistoryEntries caps the listening history: about a year of heavy
// listening. Old entries go first when it overflows.
const maxHistoryEntries = 5000

// historyEntry is one played track.
type historyEntry struct {
	VideoID  string    `json:"videoId"`
	Title    string    `json:"title,omitempty"`
	Artist   string    `json:"artist,omitempty"`
	PlayedAt time.Time `json:"playedAt"`
}

// recordHistory remembers a started track, once it is loaded. It runs on the
// main thread from start; the file write goes through the settings saver so
// a burst of skips costs one write.
func (a *app) recordHistory(item youtube.MusicItem) {
	if item.VideoID == "" {
		return
	}
	a.loadHistory()
	a.history = append(a.history, historyEntry{
		VideoID:  item.VideoID,
		Title:    item.Title,
		Artist:   item.Subtitle,
		PlayedAt: time.Now(),
	})
	if len(a.history) > maxHistoryEntries {
		a.history = append([]historyEntry(nil), a.history[len(a.history)-maxHistoryEntries:]...)
	}
	a.saveHistory()
}

// historyFile returns where the listening history lives, creating the
// directory on first use. Without a window and without an explicit path it
// returns nothing: tests keep history in memory unless they point it at a
// temporary file.
func (a *app) historyFile() string {
	if a.historyPath != "" {
		return a.historyPath
	}
	if a.win == nil {
		return ""
	}
	directory, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return ""
	}
	a.historyPath = filepath.Join(directory, "history.json")
	return a.historyPath
}

// loadHistory reads the history once; a missing or unreadable file means no
// history rather than an error the user must see.
func (a *app) loadHistory() {
	if a.historyLoaded {
		return
	}
	a.historyLoaded = true
	path := a.historyFile()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("reading listening history: %v", err)
		}
		return
	}
	var entries []historyEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		log.Printf("listening history in %s is not readable, starting fresh: %v", path, err)
		return
	}
	a.history = entries
}

// saveHistory writes the history off the main thread, one write at a time.
// It keeps its own copy: the caller may keep appending while it writes.
func (a *app) saveHistory() {
	path := a.historyFile()
	if path == "" {
		return
	}
	history := append([]historyEntry(nil), a.history...)
	a.run(func() {
		data, err := json.MarshalIndent(history, "", "  ")
		if err != nil {
			log.Printf("saving listening history: %v", err)
			return
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			log.Printf("saving listening history: %v", err)
			return
		}
		if err := writeFileAtomic(path, data, 0o600); err != nil {
			log.Printf("saving listening history: %v", err)
		}
	})
}

// dayCount is one day of listening.
type dayCount struct {
	day   time.Time
	plays int
}

// statsSummary is what the stats page shows.
type statsSummary struct {
	totalPlays   int
	totalArtists int
	byDay        []dayCount
	topTracks    []trackCount
	topArtists   []trackCount
}

// trackCount counts the plays of one track or artist.
type trackCount struct {
	title  string
	artist string
	plays  int
}

// summarizeHistory folds the history into totals, daily counts for the last
// days days, and the most played tracks and artists.
func summarizeHistory(entries []historyEntry, now time.Time, days int) statsSummary {
	var summary statsSummary
	seenArtists := make(map[string]struct{})
	tracks := make(map[string]*trackCount)
	artists := make(map[string]*trackCount)
	dayIndex := make(map[string]int)
	cutoff := startOfDay(now).AddDate(0, 0, -days+1)
	for d := 0; d < days; d++ {
		day := cutoff.AddDate(0, 0, d)
		dayIndex[day.Format("2006-01-02")] = d
		summary.byDay = append(summary.byDay, dayCount{day: day})
	}
	for _, e := range entries {
		if e.PlayedAt.Before(cutoff) {
			continue
		}
		summary.totalPlays++
		key := e.VideoID
		if key == "" {
			key = e.Title
		}
		tc := tracks[key]
		if tc == nil {
			tc = &trackCount{title: e.Title, artist: e.Artist}
			tracks[key] = tc
		}
		tc.plays++
		if e.Artist != "" {
			seenArtists[e.Artist] = struct{}{}
			ac := artists[e.Artist]
			if ac == nil {
				ac = &trackCount{title: e.Artist}
				artists[e.Artist] = ac
			}
			ac.plays++
		}
		if i, ok := dayIndex[startOfDay(e.PlayedAt).Format("2006-01-02")]; ok {
			summary.byDay[i].plays++
		}
	}
	summary.totalArtists = len(seenArtists)
	summary.topTracks = topCounts(tracks, 5)
	summary.topArtists = topCounts(artists, 5)
	return summary
}

// topCounts keeps the n highest counts, most plays first.
func topCounts(counts map[string]*trackCount, n int) []trackCount {
	all := make([]trackCount, 0, len(counts))
	for _, c := range counts {
		all = append(all, *c)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].plays != all[j].plays {
			return all[i].plays > all[j].plays
		}
		return all[i].title < all[j].title
	})
	return all[:min(len(all), n)]
}

// startOfDay is midnight of the day t falls on, in its own zone.
func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// statsRanges are the day counts the stats page offers.
var statsRanges = []int{7, 14, 30}

// statsPage shows listening totals, a chart of plays per day, and the most
// played tracks and artists, all from the local history.
func (a *app) statsPage(c *ui.Context) {
	a.loadHistory()
	if a.statsRange == 0 {
		a.statsRange = 14
	}
	summary := summarizeHistory(a.history, c.Now(), a.statsRange)
	ui.Scroll(c).Grow(1).Padding(0, pageGutter, a.clearance()).Children(func() {
		ui.Column(c).MaxWidth(780).Gap(16).Children(func() {
			ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
				settingLabel(c, "Listening stats", "From plays on this device.").Grow(1)
				labels := []string{"7 days", "14 days", "30 days"}
				picked := 1
				for i, days := range statsRanges {
					if a.statsRange == days {
						picked = i
					}
				}
				if m3.ButtonGroup(c, "stats-range", &picked, labels, nil) {
					a.statsRange = statsRanges[picked]
				}
			})
			a.statsTotals(c, summary)
			a.playsChart(c, summary)
			a.topList(c, "Most played", summary.topTracks, true)
			a.topList(c, "Top artists", summary.topArtists, false)
		})
	})
}

// statsTotals shows the headline numbers.
func (a *app) statsTotals(c *ui.Context, summary statsSummary) {
	sc := m3.Of(c).Scheme
	ui.Row(c).Gap(16).Children(func() {
		for _, total := range []struct{ value, label string }{
			{strconv.Itoa(summary.totalPlays), "plays"},
			{strconv.Itoa(summary.totalArtists), "artists"},
		} {
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Padding(20).Gap(4).Radius(m3.ExtraLarge).
				Background(sc.SurfaceContainerLow).Children(func() {
				m3.EmphasizedText(c, m3.DisplaySmall, total.value)
				m3.Text(c, m3.BodyMedium, total.label).TextColor(sc.OnSurfaceVariant)
			})
		}
	})
}

// playsChart draws the plays of each day as bars, using ui/chart scales.
func (a *app) playsChart(c *ui.Context, summary statsSummary) {
	sc := m3.Of(c).Scheme
	a.settingsCard(c, "Plays per day", m3.IconTrending, func() {
		max := 1
		for _, d := range summary.byDay {
			max = maxInt(max, d.plays)
		}
		labels := make([]string, len(summary.byDay))
		for i, d := range summary.byDay {
			labels[i] = d.day.Format("Jan 2")
		}
		x := chart.NewBand(labels...).Padding(0.35, 0.2)
		y := chart.NewLinear(0, float64(max)).Nice(4)
		chart.Plot(c, chart.Axes{Left: y.Ticks(4)}, func(area ui.Element) {
			area.Height(180)
			for i, d := range summary.byDay {
				span := x.Band(i)
				height := y.Span(0, float64(d.plays))
				bar := chart.Rect(c, span, height)
				fill := sc.Primary
				if d.plays == 0 {
					fill = sc.SurfaceContainerHighest
				}
				bar.Background(fill).Radius(m3.Small)
			}
		}).Label("Plays per day").Gap(8)
	})
}

// topList names the most played tracks or artists with their counts.
func (a *app) topList(c *ui.Context, title string, counts []trackCount, showArtist bool) {
	if len(counts) == 0 {
		return
	}
	a.settingsCard(c, title, m3.IconMusicNote, func() {
		ui.Column(c).Gap(4).Children(func() {
			for i, tc := range counts {
				ui.Row(c).Gap(12).AlignItems(ui.Center).Padding(8, 4).Children(func() {
					m3.Text(c, m3.TitleMedium, strconv.Itoa(i+1)+".").Width(28).Shrink(0).TextColor(m3.Of(c).Scheme.OnSurfaceVariant)
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						name := tc.title
						if name == "" {
							name = "Unknown track"
						}
						m3.EmphasizedText(c, m3.BodyLarge, name).SingleLine()
						if showArtist && tc.artist != "" {
							m3.Text(c, m3.BodySmall, tc.artist).SingleLine().TextColor(m3.Of(c).Scheme.OnSurfaceVariant)
						}
					})
					m3.Text(c, m3.LabelLarge, pluralPlays(tc.plays)).TextColor(m3.Of(c).Scheme.OnSurfaceVariant)
				})
			}
		})
	})
}

// maxInt is max for ints, where the builtin needs identical types.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// pluralPlays names a play count.
func pluralPlays(n int) string {
	if n == 1 {
		return "1 play"
	}
	return strconv.Itoa(n) + " plays"
}
