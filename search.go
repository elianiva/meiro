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
	suggestDelay      = 180 * time.Millisecond
	suggestTimeout    = 5 * time.Second
	searchTimeout     = time.Minute
	suggestQueries    = 6
	suggestItems      = 5
	searchPlaceholder = "Search songs, albums, artists, podcasts"

	highlightTypedText = 0
)

type searchState struct {
	pageState
	query       string
	submitted   string
	params      string
	filters     []youtube.SearchFilter
	top         youtube.MusicItem
	load        task
	suggestions youtube.SearchSuggestions
	suggest     task
	suggestOpen bool
	highlight   int
}

func (a *app) runSearch(query, params string) {
	query = strings.TrimSpace(query)
	a.cancelSuggestions()
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

func (a *app) runSearchFrom(query string) {
	if strings.TrimSpace(query) == "" {
		return
	}
	if r, _ := a.route(); r.id != routeSearch {
		a.navigate(routeSearch)
	}
	a.runSearch(query, "")
}

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
		a.search.suggestions, a.search.highlight = *suggestions, highlightTypedText
	})
}

func (a *app) cancelSuggestions() {
	a.search.suggest.stop()
	a.search.suggestions = youtube.SearchSuggestions{}
	a.search.suggestOpen = false
	a.search.highlight = highlightTypedText
}

func (a *app) shownSuggestions() (queries []string, items []youtube.MusicItem) {
	s := &a.search
	if !s.suggestOpen || strings.TrimSpace(s.query) == "" {
		return nil, nil
	}
	queries = s.suggestions.Queries
	if len(queries) > suggestQueries {
		queries = queries[:suggestQueries]
	}
	items = a.visibleItems(s.suggestions.Items)
	if len(items) > suggestItems {
		items = items[:suggestItems]
	}
	return queries, items
}

func (a *app) highlightSuggestion(step, n int) {
	h := a.search.highlight
	if h < 0 || h > n {
		h = 0
	}
	a.search.highlight = (h + step + n + 1) % (n + 1)
}

func (a *app) chooseSuggestion(index int, queries []string, items []youtube.MusicItem) {
	if index < len(queries) {
		a.runSearchFrom(queries[index])
		return
	}
	position := index - len(queries)
	a.openSuggestion(items[position], suggestionKey(position))
}

func suggestionKey(position int) string {
	return "suggestion-item-" + strconv.Itoa(position)
}

func searchTint(c *ui.Context) ui.Color {
	sc := m3.Of(c).Scheme
	return sc.PrimaryContainer.Mix(sc.Surface, 0.72)
}

func (a *app) searchBar(c *ui.Context) (bar, field ui.Element) {
	trailingPadding := float32(0)
	if a.keyboard.hints {
		trailingPadding = 40
	}
	result, bar, field := m3.SearchBar(c, m3.SearchSpec{
		Value:           &a.search.query,
		Placeholder:     searchPlaceholder,
		Key:             "search",
		Focus:           &a.searchField,
		Height:          48,
		MaxWidth:        520,
		Container:       searchTint(c),
		TrailingPadding: trailingPadding,
	})
	field.AutoComplete(ui.AutoCompleteList)
	queries, items := a.shownSuggestions()
	n := len(queries) + len(items)
	if a.search.highlight > n {
		a.search.highlight = highlightTypedText
	}
	if n > 0 {
		a.searchField.OnShortcut(c, 0, ui.KeyDown, func() { a.highlightSuggestion(1, n) })
		a.searchField.OnShortcut(c, 0, ui.KeyUp, func() { a.highlightSuggestion(-1, n) })
	}
	switch {
	case result.Submitted && a.search.highlight > highlightTypedText:
		a.chooseSuggestion(a.search.highlight-1, queries, items)
	case result.Submitted:
		a.runSearchFrom(a.search.query)
	case result.Cleared:
		a.search.query = ""
		a.cancelSuggestions()
	case result.Changed:
		a.suggestSearches(a.search.query)
	}
	return bar, field
}

func (a *app) searchSuggestions(c *ui.Context, bar, field ui.Element) {
	s := &a.search
	queries, items := a.shownSuggestions()
	if len(queries) == 0 && len(items) == 0 {
		s.highlight = highlightTypedText
		return
	}
	width := bar.Bounds().W
	if width < 320 {
		width = 320
	}
	n := len(queries) + len(items)
	ui.PopoverBase(c, bar, &s.suggestOpen, func(panel ui.Element) {
		sc := m3.Of(c).Scheme
		panel.Key("search-suggestions").Width(width).Padding(8).Gap(2).Radius(m3.Large).Margin(8, 0, 0, 0).
			Background(sc.SurfaceContainerHigh).TextColor(sc.OnSurface).Role(ui.RoleListBox).Label("Search suggestions")
		m3.Elevation(c, panel, 3)
		panel.Children(func() {
			for i, query := range queries {
				row, chosen := a.suggestionRow(c, query, i+1, n, s.highlight == i+1)
				if s.highlight == i+1 {
					field.ActiveDescendant(row)
				}
				if chosen {
					a.chooseSuggestion(i, queries, items)
				}
			}
			if len(queries) > 0 && len(items) > 0 {
				ui.Box(c).Height(1).FillWidth().Margin(8, 8).Background(sc.OutlineVariant).AccessHidden(true)
			}
			for i, item := range items {
				slot := len(queries) + i + 1
				row, chosen := a.suggestionItem(c, item, i, slot, n, s.highlight == slot)
				if s.highlight == slot {
					field.ActiveDescendant(row)
				}
				if chosen {
					a.chooseSuggestion(slot-1, queries, items)
				}
			}
		})
	})
}

func (a *app) suggestionRow(c *ui.Context, query string, slot, total int, highlighted bool) (ui.Element, bool) {
	sc := m3.Of(c).Scheme
	row := ui.ButtonBase(c.Key("suggestion-" + query))
	row.Height(m3.MinTarget).PaddingX(12).Gap(12).AlignItems(ui.Center).Radius(m3.Medium).
		Cursor(ui.CursorPointer).Label(query).Role(ui.RoleOption).AccessSelected(highlighted).PositionInSet(slot, total).
		Background(m3.StateFill(ui.Transparent, sc.OnSurface, row.Hovered(), row.Pressed(), highlighted || row.FocusVisible()))
	row.Children(func() {
		ui.Icon(c, m3.IconSearch).FontSize(20).TextColor(sc.OnSurfaceVariant)
		m3.Text(c, m3.BodyLarge, query).Grow(1).MinWidth(0).SingleLine().TextColor(sc.OnSurface)
	})
	return row, row.Clicked()
}

func (a *app) suggestionItem(c *ui.Context, item youtube.MusicItem, position, slot, total int, highlighted bool) (ui.Element, bool) {
	sc := m3.Of(c).Scheme
	key := suggestionKey(position)
	row := ui.ButtonBase(c.Key(key))
	row.Height(56).Padding(8, 8).Gap(12).Radius(m3.Medium).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label(item.Title).Role(ui.RoleOption).AccessSelected(highlighted).PositionInSet(slot, total).
		Background(m3.StateFill(ui.Transparent, sc.OnSurface, row.Hovered(), row.Pressed(), highlighted || row.FocusVisible()))
	var menuAnchor ui.Element
	hasAnchor := false
	row.Children(func() {
		a.art(c, item.Thumbnail, 128, 40, artRadius(item, m3.Small))
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			m3.Text(c, m3.BodyLarge, item.Title).SingleLine().TextColor(sc.OnSurface)
			if item.Subtitle != "" {
				m3.Text(c, m3.BodySmall, item.Subtitle).SingleLine().TextColor(sc.OnSurfaceVariant)
			}
		})
		menuAnchor, hasAnchor = a.menu.pointerAnchor(c, key)
	})
	if row.RightClicked() {
		x, y, _ := row.PointerPosition()
		a.menu.show(key, true, x, y)
	}
	if row.Shortcut(ui.Shift, ui.KeyF10) {
		a.menu.show(key, false, 0, 0)
	}
	if a.menu.owns(key) {
		anchor := row
		if hasAnchor {
			anchor = menuAnchor
		}
		a.cardMenu(c, anchor, key, item, nil)
	}
	return row, row.Clicked()
}

func (a *app) openSuggestion(item youtube.MusicItem, key string) {
	a.cancelSuggestions()
	if item.Target().Kind == youtube.TargetTrack {
		a.startRadio(item, key)
		return
	}
	a.activate(item, nil)
}

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

func (a *app) searchFilters(c *ui.Context) {
	if len(a.search.filters) == 0 {
		return
	}
	ui.Row(c).Gap(8).Wrap().Shrink(0).Padding(4, pageGutter, 12).Children(func() {
		for _, filter := range a.search.filters {
			key := "filter-" + filter.Label + filter.Params
			if filter.Params == "" {
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

func (a *app) topResult(c *ui.Context, item youtube.MusicItem) {
	sc := m3.Of(c).Scheme
	target := item.Target()
	key := "top-result"
	card := ui.ButtonBase(c.Key(key))
	card.Padding(8).Margin(0, pageGutter-8).Gap(20).Radius(m3.ExtraLarge).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(item.Title).
		Background(m3.StateFill(ui.Transparent, sc.OnSurface, card.Hovered(), card.Pressed(), card.FocusVisible()))
	played, shuffled, menuClicked := false, false, false
	hovered := card.Hovered()
	var menuButton, menuAnchor ui.Element
	hasMenu, hasAnchor := false, false
	card.Children(func() {
		art := a.art(c, item.Thumbnail, 256, 132, artRadius(item, m3.LargeIncreased), func() {
			if a.isOpening(key) {
				ui.Box(c).Size(132, 132).Radius(artRadius(item, m3.LargeIncreased)).Attach(ui.AnchorTopLeft, ui.AnchorTopLeft).
					Center().Background(sc.Scrim.Alpha(0.45)).Children(func() {
					m3.LoadingIndicatorIn(c, 28, ui.RGB(255, 255, 255))
				})
				return
			}
			if target.Kind != youtube.TargetNone {
				menuButton = m3.IconButton(c, m3.IconButtonSpec{
					Icon: m3.IconMore, Label: "More options for " + item.Title, Key: key + "-menu",
					Container: sc.SurfaceContainerHighest.Alpha(0.85),
				}).Attach(ui.AnchorTopRight, ui.AnchorTopRight).Top(0).Right(0)
				hasMenu = true
				if !hovered && !card.FocusWithin() && !a.menu.opened(key+"-menu") {
					menuButton.Opacity(0).PassThrough()
				}
			}
		})
		if target.Opens() {
			art.Transition(ui.ElementTransition{Animation: &ui.Snappy, Match: coverMatch{card: key}})
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			m3.EmphasizedText(c, m3.HeadlineSmall, item.Title).MaxLines(2).TextColor(sc.OnSurface)
			if item.Subtitle != "" {
				m3.Text(c, m3.BodyMedium, item.Subtitle).SingleLine().TextColor(sc.OnSurfaceVariant)
			}
			switch target.Kind {
			case youtube.TargetTrack:
				ui.Row(c).Padding(4, 0, 0).Children(func() {
					if m3.Button(c, m3.ButtonSpec{Label: "Play", Icon: m3.IconPlay, Size: m3.Small40, Key: key + "-play"}).Clicked() {
						played = true
					}
				})
			case youtube.TargetAlbum, youtube.TargetPlaylist, youtube.TargetArtist:
				ui.Row(c).Gap(8).Padding(4, 0, 0).Children(func() {
					if m3.Button(c, m3.ButtonSpec{Label: "Play", Icon: m3.IconPlay, Size: m3.Small40, Key: key + "-play"}).Clicked() {
						played = true
					}
					if m3.Button(c, m3.ButtonSpec{Label: "Shuffle", Icon: m3.IconShuffle, Kind: m3.Tonal, Size: m3.Small40, Key: key + "-shuffle"}).Clicked() {
						shuffled = true
					}
				})
			case youtube.TargetPodcast, youtube.TargetNone:
			}
		})
		menuAnchor, hasAnchor = a.menu.pointerAnchor(c, key+"-menu")
	})
	if hasMenu {
		pointer := card.RightClicked() || menuButton.RightClicked()
		x, y, _ := card.PointerPosition()
		if menuButton.Clicked() || pointer || card.Shortcut(ui.Shift, ui.KeyF10) {
			a.menu.show(key+"-menu", pointer, x, y)
			menuClicked = true
		}
	}
	if hasMenu && a.menu.owns(key+"-menu") {
		a.cardMenu(c, a.menu.anchor(menuButton, menuAnchor, hasAnchor), key+"-menu", item, a.page.playable)
	}
	switch {
	case menuClicked:
	case played:
		a.playCollection(item, key, false)
	case shuffled:
		a.playCollection(item, key, true)
	case card.Clicked():
		a.rememberCover(key, item)
		a.activate(item, a.page.playable)
	}
}
