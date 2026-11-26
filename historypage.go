package main

import (
	"context"
	"log"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/elianiva/meiro/internal/history"
	"github.com/elianiva/meiro/youtube"
)

const (
	historyRemoteTTL     = 5 * time.Minute
	historyRemoteTimeout = time.Minute
)

// remoteShelf is one shelf of the YouTube Music account history. YouTube names
// the shelf ("Today", "Yesterday", "This week", ...) but does not say when
// inside it a play happened.
type remoteShelf struct {
	title string
	items []youtube.MusicItem
}

type remoteHistoryState struct {
	shelves []remoteShelf
	at      time.Time
	failed  bool
	task    task
}

func (s *remoteHistoryState) reset() {
	s.task.stop()
	s.shelves, s.at, s.failed = nil, time.Time{}, false
}

func (a *app) openHistory() {
	a.loadHistory()
	a.loadRemoteHistory()
}

// loadRemoteHistory reads the account history in the background. It keeps the
// shelves for a while, so opening History again, or going back to it, does not
// ask YouTube every time.
func (a *app) loadRemoteHistory() {
	h := &a.historyRemote
	if !a.signedIn() {
		if len(h.shelves) > 0 || !h.at.IsZero() {
			h.reset()
			a.historyRev++
		}
		return
	}
	fresh := !h.at.IsZero() && time.Since(h.at) < historyRemoteTTL &&
		history.CalendarDay(h.at) == history.CalendarDay(time.Now())
	if h.task.busy() || fresh {
		return
	}
	client := a.client()
	startTask(a, &h.task, historyRemoteTimeout, func(ctx context.Context) ([]remoteShelf, error) {
		result, err := client.GetHistory(ctx)
		if err != nil {
			return nil, err
		}
		return accountShelves(result), nil
	}, func(shelves []remoteShelf, err error) {
		a.historyRev++
		if err != nil {
			log.Printf("loading YouTube Music history: %v", err)
			h.failed = true
			return
		}
		h.shelves, h.at, h.failed = shelves, time.Now(), false
	})
}

func accountShelves(result *youtube.BrowseResult) []remoteShelf {
	shelves := make([]remoteShelf, 0, len(result.Sections))
	for _, section := range result.Sections {
		items := make([]youtube.MusicItem, 0, len(section.Items))
		for _, item := range section.Items {
			if item.VideoID != "" {
				items = append(items, item)
			}
		}
		if len(items) > 0 {
			shelves = append(shelves, remoteShelf{title: section.Title, items: items})
		}
	}
	return shelves
}

// syncHistoryPage rebuilds the rows when a play, a clear, the account history
// or the calendar day changes. It runs on every frame the page is shown.
func (a *app) syncHistoryPage() {
	p := a.page
	now := time.Now()
	day := history.CalendarDay(now)
	if !p.loadedAt.IsZero() && p.historyRev == a.historyRev && p.historyDay == day {
		return
	}
	p.historyRev, p.historyDay, p.loadedAt, p.rowsDirty = a.historyRev, day, now, true
	if !a.historyLoaded {
		p.feed = pageState{loading: true}
		return
	}
	merged := mergeHistory(a.history, a.historyRemote.shelves, now)
	if len(merged) == 0 && a.signedIn() && a.historyRemote.failed {
		p.feed = pageState{err: "Could not load your YouTube Music history."}
		return
	}
	sections := make([]youtube.MusicSection, len(merged))
	for i, section := range merged {
		sections[i] = youtube.MusicSection{Title: section.title, Items: section.items}
	}
	p.feed = pageState{sections: sections, partial: a.historyRemote.task.busy()}
}

type mergedHistorySection struct {
	title string
	items []youtube.MusicItem
}

// mergeHistory combines plays made in Meiro with the account history from
// YouTube. YouTube only says which shelf a play belongs to, so a local day is
// folded into the first shelf whose date range holds it. Plays of one song in
// the same section are shown once.
func mergeHistory(local []history.Entry, remote []remoteShelf, now time.Time) []mergedHistorySection {
	type section struct {
		title  string
		start  time.Time
		end    time.Time
		known  bool
		order  int
		local  []history.Entry
		remote []youtube.MusicItem
	}
	sections := make([]*section, 0, len(remote)+len(local))
	for i, shelf := range remote {
		s := &section{title: shelf.title, remote: shelf.items, order: i}
		if start, end, ok := historyBucket(shelf.title, now); ok {
			s.start, s.end, s.known = start, end, true
		}
		sections = append(sections, s)
	}
	remoteCount := len(sections)
	for _, group := range history.GroupByDay(local, now.Location()) {
		var target *section
		for _, s := range sections[:remoteCount] {
			if s.known && !group.Day.Before(s.start) && !group.Day.After(s.end) {
				target = s
				break
			}
		}
		if target != nil {
			target.local = append(target.local, group.Entries...)
			continue
		}
		sections = append(sections, &section{
			title: historyDayLabel(group.Day, now),
			start: group.Day, end: group.Day, known: true,
			order: len(remote) + len(sections),
			local: group.Entries,
		})
	}

	// A shelf with a title we do not know has no date. Keep it in YouTube's
	// order by placing it after the oldest day of the shelf above it.
	previousStart := dayStart(now)
	for _, s := range sections {
		if s.known {
			previousStart = s.start
			continue
		}
		s.end = previousStart.AddDate(0, 0, -1)
		s.start = s.end.AddDate(0, 0, -29)
		previousStart = s.start
	}
	sort.SliceStable(sections, func(i, j int) bool {
		if !sections[i].end.Equal(sections[j].end) {
			return sections[i].end.After(sections[j].end)
		}
		return sections[i].order < sections[j].order
	})

	out := make([]mergedHistorySection, 0, len(sections))
	for _, s := range sections {
		items := make([]youtube.MusicItem, 0, len(s.local)+len(s.remote))
		seen := make(map[string]struct{}, len(items))
		add := func(key string, item youtube.MusicItem) {
			if key == "" {
				return
			}
			if _, ok := seen[key]; ok {
				return
			}
			seen[key] = struct{}{}
			items = append(items, item)
		}
		sort.SliceStable(s.local, func(i, j int) bool {
			return s.local[i].PlayedAt.After(s.local[j].PlayedAt)
		})
		for _, entry := range s.local {
			add(entry.VideoID, historyItem(entry))
		}
		for _, item := range s.remote {
			add(item.VideoID, item)
		}
		if len(items) == 0 {
			continue
		}
		title := s.title
		if title == "" {
			title = "Earlier"
		}
		out = append(out, mergedHistorySection{title: title, items: items})
	}
	return out
}

// historyBucket turns a YouTube shelf title into the days it covers. It returns
// false for a title it cannot place, such as a language other than English.
func historyBucket(label string, now time.Time) (time.Time, time.Time, bool) {
	today := dayStart(now)
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "today":
		return today, today, true
	case "yesterday":
		day := today.AddDate(0, 0, -1)
		return day, day, true
	case "this week":
		return today.AddDate(0, 0, -6), today.AddDate(0, 0, -2), true
	case "last week":
		return today.AddDate(0, 0, -13), today.AddDate(0, 0, -7), true
	case "this month":
		start := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
		end := today.AddDate(0, 0, -2)
		return start, end, !end.Before(start)
	case "last month":
		thisMonth := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
		return thisMonth.AddDate(0, -1, 0), thisMonth.AddDate(0, 0, -1), true
	case "this year":
		start := time.Date(today.Year(), 1, 1, 0, 0, 0, 0, today.Location())
		end := today.AddDate(0, 0, -2)
		return start, end, !end.Before(start)
	case "last year":
		return time.Date(today.Year()-1, 1, 1, 0, 0, 0, 0, today.Location()),
			time.Date(today.Year()-1, 12, 31, 0, 0, 0, 0, today.Location()), true
	}
	if start, ok := historyMonth(strings.TrimSpace(label), now); ok {
		return start, start.AddDate(0, 1, -1), true
	}
	return time.Time{}, time.Time{}, false
}

func historyMonth(label string, now time.Time) (time.Time, bool) {
	loc := now.Location()
	for _, layout := range []string{"January 2006", "Jan 2006", "January", "Jan"} {
		parsed, err := time.ParseInLocation(layout, label, loc)
		if err != nil {
			continue
		}
		if layout == "January" || layout == "Jan" {
			year := now.Year()
			if parsed.Month() > now.Month() {
				year--
			}
			parsed = time.Date(year, parsed.Month(), 1, 0, 0, 0, 0, loc)
		}
		return parsed, true
	}
	return time.Time{}, false
}

func historyDayLabel(day, now time.Time) string {
	switch history.CalendarDay(now) - history.CalendarDay(day) {
	case 0:
		return "Today"
	case 1:
		return "Yesterday"
	}
	if day.Year() == now.Year() {
		return day.Format("Monday, 2 January")
	}
	return day.Format("Monday, 2 January 2006")
}

func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func historyItem(entry history.Entry) youtube.MusicItem {
	item := youtube.MusicItem{
		VideoID:   entry.VideoID,
		Title:     entry.Title,
		Subtitle:  entry.Artist,
		Artist:    entry.Artist,
		Thumbnail: entry.Thumbnail,
		Duration:  entry.Duration,
		Kind:      youtube.ItemKind(entry.Kind),
	}
	if item.Title == "" {
		item.Title = "Unknown track"
	}
	if item.Thumbnail == "" {
		item.Thumbnail = videoThumbnail(entry.VideoID)
	}
	return item
}

// videoThumbnail is the picture YouTube keeps for a video. Plays recorded
// before Meiro saved covers still get one.
func videoThumbnail(videoID string) string {
	if videoID == "" {
		return ""
	}
	return "https://i.ytimg.com/vi/" + url.PathEscape(videoID) + "/hqdefault.jpg"
}
