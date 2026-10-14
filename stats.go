package main

import (
	"log"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"

	"github.com/elianiva/meiro/internal/history"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// recordHistory remembers a started track. It runs on the main thread from
// start; the write goes off it.
func (a *app) recordHistory(item youtube.MusicItem) {
	if item.VideoID == "" {
		return
	}
	a.loadHistory()
	a.history = history.Append(a.history, history.Entry{
		VideoID:  item.VideoID,
		Title:    item.Title,
		Artist:   item.Subtitle,
		PlayedAt: time.Now(),
	})
	a.saveHistory()
}

// loadHistory starts reading the history once, off the main thread: the file
// holds thousands of entries. Plays recorded before the read finishes are
// kept behind what it brings, and written with it.
func (a *app) loadHistory() {
	if a.historyLoaded || a.historyLoading {
		return
	}
	a.historyLoading = true
	path := a.historyPath
	spawn(a, func() []history.Entry { return history.Read(path) }, func(entries []history.Entry) {
		pending := len(a.history) > 0
		a.history = history.Append(entries, a.history...)
		a.historyLoaded, a.historyLoading = true, false
		if pending {
			a.saveHistory()
		}
	})
}

// saveHistory writes the history off the main thread. It writes a copy: the
// main thread may keep appending while it writes.
func (a *app) saveHistory() {
	path := a.historyPath
	if path == "" || !a.historyLoaded {
		return
	}
	entries := append([]history.Entry(nil), a.history...)
	a.run(func() {
		if err := history.Write(path, entries); err != nil {
			log.Printf("saving listening history: %v", err)
		}
	})
}

// statsMemo holds the summary the stats page last built, and what it was built
// from. The page rebuilds on every frame of an animation, but the history only
// changes when a track starts.
type statsMemo struct {
	summary history.Summary
	entries int
	last    time.Time
	days    int
	today   int
	valid   bool
}

// statsSummary returns the summary of the history over days days ending
// today, folding the history again only when it has changed.
func (a *app) statsSummary(now time.Time, days int) history.Summary {
	var last time.Time
	if n := len(a.history); n > 0 {
		last = a.history[n-1].PlayedAt
	}
	memo := &a.stats
	today := history.CalendarDay(now)
	if memo.valid && memo.entries == len(a.history) && memo.last.Equal(last) && memo.days == days && memo.today == today {
		return memo.summary
	}
	*memo = statsMemo{
		summary: history.Summarize(a.history, now, days),
		entries: len(a.history), last: last, days: days, today: today, valid: true,
	}
	return memo.summary
}

// statsRanges are the day counts the stats page offers.
var statsRanges = []int{7, 14, 30}

// statsPage shows listening totals, a chart of plays per day, and the most
// played tracks and artists, all from the local history.
func (a *app) statsPage(c *ui.Context) {
	summary := a.statsSummary(c.Now(), a.statsRange)
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
			if summary.Plays == 0 && a.historyLoaded {
				// Nothing to retry: the history is read from this device, and
				// only listening fills it.
				a.message(c, m3.IconTrending, "No plays yet", "Songs you listen to in Meiro are counted here, for the last "+strconv.Itoa(a.statsRange)+" days.", "", nil)
				return
			}
			a.statsTotals(c, summary)
			a.playsChart(c, summary)
			a.topList(c, "Most played", summary.TopTracks, true)
			a.topList(c, "Top artists", summary.TopArtists, false)
		})
	})
}

// statsTotals shows the headline numbers.
func (a *app) statsTotals(c *ui.Context, summary history.Summary) {
	sc := m3.Of(c).Scheme
	ui.Row(c).Gap(16).Children(func() {
		for _, total := range []struct{ value, label string }{
			{strconv.Itoa(summary.Plays), "plays"},
			{strconv.Itoa(summary.Artists), "artists"},
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
func (a *app) playsChart(c *ui.Context, summary history.Summary) {
	sc := m3.Of(c).Scheme
	a.settingsCard(c, "Plays per day", m3.IconTrending, func() {
		most := 1
		labels := make([]string, len(summary.ByDay))
		for i, d := range summary.ByDay {
			most = max(most, d.Plays)
			labels[i] = d.Day.Format("Jan 2")
		}
		x := chart.NewBand(labels...).Padding(0.35, 0.2)
		y := chart.NewLinear(0, float64(most)).Nice(4)
		chart.Plot(c, chart.Axes{Left: y.Ticks(4)}, func(area ui.Element) {
			area.Height(180)
			for i, d := range summary.ByDay {
				span := x.Band(i)
				height := y.Span(0, float64(d.Plays))
				bar := chart.Rect(c, span, height)
				fill := sc.Primary
				if d.Plays == 0 {
					fill = sc.SurfaceContainerHighest
				}
				bar.Background(fill).Radius(m3.Small)
			}
		}).Label("Plays per day").Gap(8)
	})
}

// topList names the most played tracks or artists with their counts.
func (a *app) topList(c *ui.Context, title string, counts []history.Count, showArtist bool) {
	if len(counts) == 0 {
		return
	}
	a.settingsCard(c, title, m3.IconMusicNote, func() {
		ui.Column(c).Gap(4).Children(func() {
			for i, tc := range counts {
				ui.Row(c).Gap(12).AlignItems(ui.Center).Padding(8, 4).Children(func() {
					m3.Text(c, m3.TitleMedium, strconv.Itoa(i+1)+".").Width(28).Shrink(0).TextColor(m3.Of(c).Scheme.OnSurfaceVariant)
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						name := tc.Title
						if name == "" {
							name = "Unknown track"
						}
						m3.EmphasizedText(c, m3.BodyLarge, name).SingleLine()
						if showArtist && tc.Artist != "" {
							m3.Text(c, m3.BodySmall, tc.Artist).SingleLine().TextColor(m3.Of(c).Scheme.OnSurfaceVariant)
						}
					})
					m3.Text(c, m3.LabelLarge, pluralPlays(tc.Plays)).TextColor(m3.Of(c).Scheme.OnSurfaceVariant)
				})
			}
		})
	})
}

// pluralPlays names a play count.
func pluralPlays(n int) string {
	if n == 1 {
		return "1 play"
	}
	return strconv.Itoa(n) + " plays"
}
