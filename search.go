package main

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

const (
	// suggestDelay is how long typing pauses before the app asks YouTube
	// Music for completions, and suggestTimeout bounds one such request.
	suggestDelay   = 180 * time.Millisecond
	suggestTimeout = 5 * time.Second
	// searchTimeout bounds a search.
	searchTimeout = time.Minute
)

// searchKinds are the filters of the search page, in the order its chips show
// them.
var searchKinds = []struct {
	name string
	kind youtube.SearchType
}{
	{"All", youtube.SearchAll},
	{"Songs", youtube.SearchSongs},
	{"Albums", youtube.SearchAlbums},
	{"Artists", youtube.SearchArtists},
	{"Playlists", youtube.SearchPlaylists},
	{"Videos", youtube.SearchVideos},
}

// searchState is the search page. Typing only edits query; a search runs when
// the user submits it, and submitted is what the results are for.
type searchState struct {
	pageState
	query     string
	submitted string
	// kind is the filter chosen, a place in searchKinds.
	kind int
	load task
	// suggestions are the completions of the text being typed, which suggest
	// asks for once typing pauses.
	suggestions []string
	suggest     task
}

// runSearch searches for what the user submitted, with the chosen filter. It
// is the only way a search starts: typing in the field never does.
func (a *app) runSearch(query string) {
	query = strings.TrimSpace(query)
	a.cancelSuggestions()
	// A "show more" in flight belongs to the results being replaced.
	a.pageMore.stop()
	s := &a.search
	s.load.stop()
	if query == "" {
		s.query, s.submitted, s.pageState = "", "", pageState{}
		a.page.rowsDirty = true
		return
	}
	s.query, s.submitted = query, query
	a.settings.Remember(query)
	a.saveSettings()
	s.pageState = pageState{loading: true}
	a.page.rowsDirty = true
	a.list.ScrollTo(0, ui.Start)
	client, kind := a.client(), searchKinds[s.kind].kind
	startTask(a, &s.load, searchTimeout, func(ctx context.Context) (*youtube.SearchResult, error) {
		return client.Search(ctx, query, youtube.SearchOptions{Type: kind})
	}, func(result *youtube.SearchResult, err error) {
		a.page.rowsDirty = true
		s.loading = false
		if err != nil {
			s.err = err.Error()
			return
		}
		s.items, s.more = result.Items, result.ContinuationToken
	})
}

// suggestSearches asks for the completions of what is being typed once typing
// pauses. Each call replaces the request before it, so only the last query's
// answer is kept.
func (a *app) suggestSearches(query string) {
	query = strings.TrimSpace(query)
	a.cancelSuggestions()
	if query == "" || query == a.search.submitted {
		return
	}
	client := a.client()
	startTask(a, &a.search.suggest, suggestDelay+suggestTimeout, func(ctx context.Context) ([]string, error) {
		if err := a.sleep(ctx, suggestDelay); err != nil {
			return nil, err
		}
		return client.GetSearchSuggestions(ctx, query)
	}, func(suggestions []string, err error) {
		if err != nil {
			suggestions = nil
		}
		a.search.suggestions = suggestions
	})
}

// cancelSuggestions stops a pending completion request and forgets what it
// found, as when the query is submitted or the page is left.
func (a *app) cancelSuggestions() {
	a.search.suggest.stop()
	a.search.suggestions = nil
}

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
				a.settings.Forget(query)
				a.saveSettings()
			case row.Clicked():
				a.search.query = query
				a.runSearch(query)
			}
		}
	})
}
