package main

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

// suggestDelay is how long typing pauses before the app asks YouTube Music
// for completions, and suggestTimeout bounds one such request.
const (
	suggestDelay   = 180 * time.Millisecond
	suggestTimeout = 5 * time.Second
)

// searchPage is a search bar over the filters and the results. Typing only
// edits the text: a search runs when the user presses Enter, or picks another
// filter for what they last searched. Typing also asks for completions, which
// choosing one searches.
func (a *app) searchPage(c *ui.Context) {
	ui.Column(c).Fill().Children(func() {
		ui.Column(c).Padding(0, pageGutter, 12).Gap(16).Shrink(0).Children(func() {
			ui.Row(c).Children(func() {
				result := m3.SearchBar(c, m3.SearchSpec{
					Value:       &a.search.query,
					Placeholder: "Search songs, albums, artists",
					Key:         "search",
					AutoFocus:   a.focusSearch,
					MaxWidth:    760,
				})
				switch {
				case result.Submitted:
					a.runSearch(a.search.query)
				case result.Cleared:
					a.search.query = ""
					a.cancelSuggestions()
				case result.Changed:
					a.suggestSearches(a.search.query)
				}
			})
			a.focusSearch = false
			a.suggestionList(c)
			ui.Row(c).Gap(8).Wrap().Children(func() {
				for i, kind := range searchKinds {
					if m3.Chip(c, kind.name, i == a.search.kind, "chip-"+kind.name).Clicked() && i != a.search.kind {
						a.search.kind = i
						if a.search.submitted != "" {
							a.runSearch(a.search.submitted)
						}
					}
				}
			})
		})
		if a.search.submitted == "" {
			a.searchLanding(c)
			return
		}
		a.pageList(c)
	})
}

// suggestSearches asks for the completions of what is being typed once typing
// pauses. Each call replaces the request before it, so only the last query's
// answer is kept. A request that lands after the query moved on is dropped.
func (a *app) suggestSearches(query string) {
	query = strings.TrimSpace(query)
	a.cancelSuggestions()
	client := a.client()
	if query == "" || query == a.search.submitted || client == nil {
		return
	}
	seq := a.search.suggestSeq
	a.search.suggestCancel = a.schedule(suggestDelay, func() {
		ctx, cancel := context.WithTimeout(context.Background(), suggestTimeout)
		defer cancel()
		suggestions, err := client.GetSearchSuggestions(ctx, query)
		a.update(func() {
			if seq != a.search.suggestSeq {
				return // the query moved on while this request was in flight
			}
			if err != nil {
				a.search.suggestions = nil
				return
			}
			a.search.suggestions = suggestions
		})
	})
}

// cancelSuggestions stops a pending completion request and forgets what it
// found, as when the query is submitted or the page is left.
func (a *app) cancelSuggestions() {
	if a.search.suggestCancel != nil {
		a.search.suggestCancel()
		a.search.suggestCancel = nil
	}
	a.search.suggestSeq++
	a.search.suggestions = nil
}

// suggestionList shows the completions of the text being typed, above the
// results. It is hidden once the query is the one the results are for.
func (a *app) suggestionList(c *ui.Context) {
	if len(a.search.suggestions) == 0 || a.search.query == a.search.submitted {
		return
	}
	sc := m3.Of(c).Scheme
	ui.Column(c).Shrink(0).Children(func() {
		for _, suggestion := range a.search.suggestions {
			row := ui.ButtonBase(c.Key("suggestion-" + suggestion))
			row.Height(44).Padding(0, 12).Gap(12).AlignItems(ui.Center).Radius(m3.Small).
				Cursor(ui.CursorPointer).Label(suggestion).
				Background(m3.StateFill(ui.Transparent, sc.OnSurface, row.Hovered(), row.Pressed(), row.FocusVisible()))
			row.Children(func() {
				ui.Icon(c, m3.IconSearch).FontSize(20).TextColor(sc.OnSurfaceVariant)
				m3.Text(c, m3.BodyLarge, suggestion).Grow(1).SingleLine().TextColor(sc.OnSurface)
			})
			if row.Clicked() {
				a.search.query = suggestion
				a.runSearch(suggestion)
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
				a.forget(query)
			case row.Clicked():
				a.search.query = query
				a.runSearch(query)
			}
		}
	})
}

// forget takes a search out of the recent ones.
func (a *app) forget(query string) {
	a.settings.Recent = slices.DeleteFunc(a.settings.Recent, func(q string) bool { return q == query })
	a.saveSettings()
}
