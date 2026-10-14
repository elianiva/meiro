package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

const (
	// suggestDelay is how long typing pauses before the app asks YouTube
	// Music for what it offers, and suggestTimeout bounds one such request.
	suggestDelay   = 180 * time.Millisecond
	suggestTimeout = 5 * time.Second
	// searchTimeout bounds a search.
	searchTimeout = time.Minute
	// suggestQueries and suggestItems bound what the panel under the search
	// bar lists, as YouTube Music's own panel does.
	suggestQueries = 6
	suggestItems   = 5
	// searchPlaceholder names what a search finds.
	searchPlaceholder = "Search songs, albums, artists, podcasts"
)

// searchState is the search page. Typing only edits query; a search runs when
// the user submits it, and submitted is what the results are for.
type searchState struct {
	pageState
	query     string
	submitted string
	// params is the filter the results are for, as the filters of the search
	// carry it; empty searches every category.
	params string
	// filters are the categories YouTube Music offers for this query, in its
	// order. The one that clears them all has no parameters.
	filters []youtube.SearchFilter
	// top is the card the results lead with, if they have one.
	top  youtube.MusicItem
	load task
	// suggestions are what YouTube Music offers for the text being typed:
	// the completed queries, and the items it already matches. suggestOpen
	// hides them again once one is chosen, until typing resumes.
	suggestions youtube.SearchSuggestions
	suggest     task
	suggestOpen bool
	// barWidth is the width the search bar had in the last frame, which the
	// panel of suggestions takes its width from.
	barWidth float32
}

// runSearch searches for query with the filter given. It is the only way a
// search starts: typing in the field never does. An empty query clears the
// page.
func (a *app) runSearch(query, params string) {
	query = strings.TrimSpace(query)
	a.cancelSuggestions()
	// A "show more" in flight belongs to the results being replaced.
	a.pageMore.stop()
	s := &a.search
	s.load.stop()
	if query == "" {
		s.query, s.submitted, s.params, s.pageState = "", "", "", pageState{}
		s.filters, s.top = nil, youtube.MusicItem{}
		a.page.rowsDirty = true
		return
	}
	s.query, s.submitted, s.params = query, query, params
	// The filters of the search being replaced stay until the answer carries
	// its own: the row of chips holds its place while the results load, and
	// the filter chosen shows as chosen at once.
	a.settings.Remember(query)
	a.saveSettings()
	s.pageState = pageState{loading: true}
	a.page.rowsDirty = true
	a.list.ScrollTo(0, ui.Start)
	client := a.client()
	startTask(a, &s.load, searchTimeout, func(ctx context.Context) (*youtube.SearchResult, error) {
		return client.Search(ctx, query, youtube.SearchOptions{Params: params})
	}, func(result *youtube.SearchResult, err error) {
		a.page.rowsDirty = true
		s.loading = false
		if err != nil {
			s.err, s.offline = failure(err)
			return
		}
		s.items, s.more = result.Items, result.ContinuationToken
		s.filters, s.top = result.Filters, result.TopResult
	})
}

// runSearchFrom runs a search from wherever the user is: submitting the search
// bar shows the search page with what it ran.
func (a *app) runSearchFrom(query string) {
	if strings.TrimSpace(query) == "" {
		return
	}
	if r, _ := a.route(); r.id != routeSearch {
		a.navigate(routeSearch)
	}
	a.runSearch(query, "")
}

// suggestSearches asks YouTube Music what it offers for the text being typed
// once typing pauses. Each call replaces the request before it, so only the
// last query's answer is kept.
func (a *app) suggestSearches(query string) {
	query = strings.TrimSpace(query)
	a.cancelSuggestions()
	if query == "" {
		return
	}
	a.search.suggestOpen = true
	client := a.client()
	startTask(a, &a.search.suggest, suggestDelay+suggestTimeout, func(ctx context.Context) (*youtube.SearchSuggestions, error) {
		if err := a.sleep(ctx, suggestDelay); err != nil {
			return nil, err
		}
		return client.GetSearchSuggestions(ctx, query)
	}, func(suggestions *youtube.SearchSuggestions, err error) {
		if err != nil || suggestions == nil {
			suggestions = &youtube.SearchSuggestions{}
		}
		a.search.suggestions = *suggestions
	})
}

// cancelSuggestions stops a pending request for suggestions, forgets what it
// found, and closes the panel they open: what a search, a navigation and a
// cleared field do.
func (a *app) cancelSuggestions() {
	a.search.suggest.stop()
	a.search.suggestions = youtube.SearchSuggestions{}
	a.search.suggestOpen = false
}

// searchBar is the field every page searches from, in the top bar. Submitting
// it shows the search page; typing asks YouTube Music for what it offers,
// which the panel under the bar shows.
func (a *app) searchBar(c *ui.Context) ui.Element {
	result := m3.SearchBar(c, m3.SearchSpec{
		Value:       &a.search.query,
		Placeholder: searchPlaceholder,
		Key:         "search",
		AutoFocus:   a.focusSearch,
		Height:      48,
		MaxWidth:    520,
	})
	a.focusSearch = false
	a.search.barWidth = result.Element.Bounds().W
	switch {
	case result.Submitted:
		a.runSearchFrom(a.search.query)
	case result.Cleared:
		a.search.query = ""
		a.cancelSuggestions()
	case result.Changed:
		a.suggestSearches(a.search.query)
	}
	return result.Element
}

// searchSuggestions shows under the bar what YouTube Music offers for the text
// being typed: the completed queries, and the items it already matches.
// Choosing either searches what it names.
func (a *app) searchSuggestions(c *ui.Context, bar ui.Element) {
	s := &a.search
	if !s.suggestOpen || strings.TrimSpace(s.query) == "" {
		return
	}
	queries := s.suggestions.Queries
	if len(queries) > suggestQueries {
		queries = queries[:suggestQueries]
	}
	items := s.suggestions.Items
	if len(items) > suggestItems {
		items = items[:suggestItems]
	}
	if len(queries) == 0 && len(items) == 0 {
		return
	}
	width := s.barWidth
	if width < 320 {
		width = 320
	}
	ui.PopoverBase(c, bar, &s.suggestOpen, func(panel ui.Element) {
		sc := m3.Of(c).Scheme
		panel.Width(width).Padding(8).Gap(2).Radius(m3.Large).Margin(8, 0, 0, 0).
			Background(sc.SurfaceContainerHigh).TextColor(sc.OnSurface)
		m3.Elevation(c, panel, 3)
		panel.Children(func() {
			for _, query := range queries {
				a.suggestionRow(c, query)
			}
			if len(queries) > 0 && len(items) > 0 {
				ui.Box(c).Height(1).FillWidth().Margin(8, 8).Background(sc.OutlineVariant)
			}
			var tracks []youtube.MusicItem
			for _, item := range items {
				if item.IsTrack() {
					tracks = append(tracks, item)
				}
			}
			for i, item := range items {
				a.suggestionItem(c, item, tracks, i)
			}
		})
	})
}

// suggestionRow is one completed query of the panel under the search bar.
func (a *app) suggestionRow(c *ui.Context, query string) {
	sc := m3.Of(c).Scheme
	row := ui.ButtonBase(c.Key("suggestion-" + query))
	row.Height(44).PaddingX(12).Gap(12).AlignItems(ui.Center).Radius(m3.Medium).
		Cursor(ui.CursorPointer).Label(query).
		Background(m3.StateFill(ui.Transparent, sc.OnSurface, row.Hovered(), row.Pressed(), row.FocusVisible()))
	row.Children(func() {
		ui.Icon(c, m3.IconSearch).FontSize(20).TextColor(sc.OnSurfaceVariant)
		m3.Text(c, m3.BodyLarge, query).Grow(1).MinWidth(0).SingleLine().TextColor(sc.OnSurface)
	})
	if row.Clicked() {
		a.runSearch(query, "")
	}
}

// suggestionItem is one item of the panel under the search bar: what YouTube
// Music already matches the text being typed with.
func (a *app) suggestionItem(c *ui.Context, item youtube.MusicItem, tracks []youtube.MusicItem, position int) {
	sc := m3.Of(c).Scheme
	row := ui.ButtonBase(c.Key("suggestion-item-" + strconv.Itoa(position)))
	row.Height(56).Padding(8, 8).Gap(12).Radius(m3.Medium).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label(item.Title).
		Background(m3.StateFill(ui.Transparent, sc.OnSurface, row.Hovered(), row.Pressed(), row.FocusVisible()))
	row.Children(func() {
		a.art(c, item.Thumbnail, 128, 40, artRadius(item, m3.Small))
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			m3.Text(c, m3.BodyLarge, item.Title).SingleLine().TextColor(sc.OnSurface)
			if item.Subtitle != "" {
				m3.Text(c, m3.BodySmall, item.Subtitle).SingleLine().TextColor(sc.OnSurfaceVariant)
			}
		})
	})
	if row.Clicked() {
		a.cancelSuggestions()
		a.activate(item, tracks)
	}
}

// searchPage is the filters of the search over what it found. The field
// itself is in the top bar, which every page searches from.
func (a *app) searchPage(c *ui.Context) {
	ui.Column(c).Fill().Children(func() {
		a.searchFilters(c)
		if a.search.submitted == "" {
			a.searchLanding(c)
			return
		}
		a.pageList(c)
	})
}

// searchFilters are the filters YouTube Music offers for the search shown, with
// the chip that clears them when one of them is on. Which one is on is the
// parameters the search ran with rather than a place in the row: the row is
// YouTube's own, and it differs from query to query.
func (a *app) searchFilters(c *ui.Context) {
	if len(a.search.filters) == 0 {
		return
	}
	ui.Row(c).Gap(8).Wrap().Shrink(0).Padding(4, pageGutter, 12).Children(func() {
		for _, filter := range a.search.filters {
			key := "filter-" + filter.Label + filter.Params
			if filter.Params == "" {
				// The chip that clears the filters, which YouTube draws only
				// while one of them is on.
				if a.search.params == "" {
					continue
				}
				if m3.IconChip(c, m3.IconClose, "Clear filters", key).Clicked() {
					a.runSearch(a.search.submitted, "")
				}
				continue
			}
			selected := filter.Params == a.search.params
			if m3.Chip(c, filter.Label, selected, key).Clicked() && !selected {
				a.runSearch(a.search.submitted, filter.Params)
			}
		}
	})
}

// searchLanding is the page before the first search: the searches the user
// made before, or an invitation to make one.
func (a *app) searchLanding(c *ui.Context) {
	sc := m3.Of(c).Scheme
	if len(a.settings.Recent) == 0 {
		ui.Column(c).Grow(1).Center().Children(func() {
			a.message(c, m3.IconSearch, "Find your next favourite",
				"Search for songs, albums, artists and playlists, then press Enter.", "", nil)
		})
		return
	}
	ui.Scroll(c).Grow(1).Padding(8, 0, a.clearance()).Children(func() {
		m3.EmphasizedText(c, m3.TitleMedium, "Recent searches").Padding(8, pageGutter, 8).TextColor(sc.OnSurfaceVariant)
		for _, query := range a.settings.Recent {
			row := ui.ButtonBase(c.Key("recent-" + query))
			row.Height(56).Margin(0, pageGutter-8).Padding(0, 4, 0, 16).Gap(16).AlignItems(ui.Center).Radius(m3.Large).
				Cursor(ui.CursorPointer).Label(query).
				Background(m3.StateFill(ui.Transparent, sc.OnSurface, row.Hovered(), row.Pressed(), row.FocusVisible()))
			removed := false
			row.Children(func() {
				ui.Icon(c, m3.IconHistory).FontSize(24).TextColor(sc.OnSurfaceVariant)
				m3.Text(c, m3.BodyLarge, query).Grow(1).SingleLine().TextColor(sc.OnSurface)
				if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconClose, Label: "Remove " + query, Size: m3.ExtraSmall32, Key: "forget-" + query}).Clicked() {
					removed = true
				}
			})
			switch {
			case removed:
				a.settings.Forget(query)
				a.saveSettings()
			case row.Clicked():
				a.runSearch(query, "")
			}
		}
	})
}
