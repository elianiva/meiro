package main

import (
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"

	"github.com/elianiva/meiro/internal/history"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

func (a *app) recordHistory(item youtube.MusicItem) {
	if item.VideoID == "" {
		return
	}
	a.loadHistory()
	a.history = history.Append(a.history, history.Entry{
		VideoID:   item.VideoID,
		Title:     item.Title,
		Artist:    shownArtist(item),
		Thumbnail: item.Thumbnail,
		Duration:  item.Duration,
		Kind:      string(item.Kind),
		PlayedAt:  time.Now(),
	})
	a.historyRev++
	a.saveHistory()
}

func (a *app) loadHistory() {
	if a.historyLoaded || a.historyLoading {
		return
	}
	a.historyLoading = true
	path := a.historyPath
	spawn(a, func() []history.Entry { return history.Read(path) }, func(entries []history.Entry) {
		pending := len(a.history) > 0
		if a.historyDiscard {
			entries, pending, a.historyDiscard = nil, true, false
		}
		a.history = history.Append(entries, a.history...)
		a.historyLoaded, a.historyLoading = true, false
		a.historyRev++
		if pending {
			a.saveHistory()
		}
	})
}

func (a *app) saveHistory() {
	if a.historyPath == "" || !a.historyLoaded {
		return
	}
	a.historyWriter.Save(a.historyPath, a.history)
}

func (a *app) clearHistory() {
	a.history = nil
	a.historyRev++
	if !a.historyLoaded {
		a.historyDiscard = true
		a.loadHistory()
		return
	}
	a.saveHistory()
}

type statsMemo struct {
	summary history.Summary
	entries int
	last    time.Time
	days    int
	today   int
	valid   bool
}

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

var statsRanges = []int{7, 14, 30}

func (a *app) statsPage(c *ui.Context) {
	summary := a.statsSummary(c.Now(), a.statsRangeDays)
	ui.Scroll(c).Grow(1).Padding(0, pageGutter, a.clearance()).Children(func() {
		ui.Column(c).MaxWidth(780).Gap(16).Children(func() {
			ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
				settingLabel(c, "Listening stats", "From plays on this device.").Grow(1)
				labels := []string{"7 days", "14 days", "30 days"}
				picked := 1
				for i, days := range statsRanges {
					if a.statsRangeDays == days {
						picked = i
					}
				}
				if m3.ButtonGroup(c, "stats-range", &picked, labels, nil) {
					a.statsRangeDays = statsRanges[picked]
				}
			})
			if summary.Plays == 0 && a.historyLoaded {
				a.message(c, m3.IconTrending, "No plays yet", "Songs you listen to in Meiro are counted here, for the last "+strconv.Itoa(a.statsRangeDays)+" days.", "", nil)
				return
			}
			a.statsTotals(c, summary)
			a.playsChart(c, summary)
			a.topList(c, "Most played", summary.TopTracks, true)
			a.topList(c, "Top artists", summary.TopArtists, false)
		})
	})
}

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

func pluralPlays(n int) string {
	if n == 1 {
		return "1 play"
	}
	return strconv.Itoa(n) + " plays"
}
