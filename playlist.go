package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

const newPlaylistID = "new-playlist"

var newPlaylistItem = youtube.MusicItem{ID: newPlaylistID, Title: "New playlist"}

func isNewPlaylist(item youtube.MusicItem) bool { return item.ID == newPlaylistID }

type newPlaylistState struct {
	open bool
	name string
	song youtube.MusicItem
	busy bool
	err  string
}

const writeTimeout = time.Minute

func (a *app) openNewPlaylist(song youtube.MusicItem) {
	a.newPlaylist = &newPlaylistState{open: true, song: song}
}

func (a *app) createNewPlaylist() {
	d := a.newPlaylist
	if d == nil {
		return
	}
	name := strings.TrimSpace(d.name)
	if d.busy || name == "" {
		return
	}
	client, song := a.client(), d.song
	d.busy, d.err = true, ""
	type created struct {
		id  string
		err error
	}
	spawn(a, func() created {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		id, err := client.CreatePlaylist(ctx, name, song.VideoID)
		return created{id, err}
	}, func(r created) {
		d.busy = false
		open := a.newPlaylist == d
		switch {
		case r.err != nil && open:
			d.err = r.err.Error()
		case r.err != nil:
			a.notice = "Could not create “" + name + "”: " + firstLine(r.err.Error())
		default:
			a.showPlaylist(r.id, name)
			if open {
				a.newPlaylist = nil
			}
			a.notice = "Created “" + name + "”"
			if song.VideoID != "" {
				a.notice = "Added “" + song.Title + "” to “" + name + "”"
			}
		}
	})
}

func (a *app) newPlaylistDialog(c *ui.Context) {
	d := a.newPlaylist
	if d == nil {
		return
	}
	if !d.open {
		a.newPlaylist = nil
		return
	}
	sc := m3.Of(c).Scheme
	m3.Dialog(c, &d.open, 480, func() {
		m3.EmphasizedText(c, m3.HeadlineSmall, "New playlist")
		if song := d.song; song.VideoID != "" {
			m3.Text(c, m3.BodyMedium, "It will hold “"+song.Title+"”.").TextColor(sc.OnSurfaceVariant).SingleLine()
		}
		m3.Text(c, m3.BodyMedium, "Meiro makes it private to your account.").TextColor(sc.OnSurfaceVariant)
		box := ui.Column(c.Key("playlist-name")).Padding(12, 16).Radius(m3.Large).Background(sc.SurfaceContainerHighest)
		if box.FocusWithin() {
			box.Border(2, sc.Primary)
		}
		box.Children(func() {
			ui.TextInputBase(c.Key("playlist-name-field"), &d.name).
				Placeholder("Playlist name").Label("Name").TextColor(sc.OnSurface).FontSize(16).AutoFocus().
				OnSubmit(a.createNewPlaylist)
		})
		if d.err != "" {
			m3.Text(c, m3.BodyMedium, d.err).TextColor(sc.Error).MaxLines(4)
		}
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if d.busy {
				m3.LoadingIndicator(c, 40, false)
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Cancel", Kind: m3.TextOnly, Key: "cancel-new-playlist"}).Clicked() {
				a.newPlaylist = nil
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Create", Disabled: d.busy || strings.TrimSpace(d.name) == "", Key: "create-playlist"}).Clicked() {
				a.createNewPlaylist()
			}
		})
	})
}

func (a *app) showPlaylist(id, name string) {
	if r, _ := a.route(); id == "" || r.id != routeLibrary || name == "" {
		return
	}
	item := youtube.MusicItem{ID: "VL" + id, BrowseID: "VL" + id, Title: name, Subtitle: "Playlist"}
	feed := &a.page.feed
	if len(feed.sections) == 0 {
		feed.items = append([]youtube.MusicItem{item}, feed.items...)
	} else {
		feed.sections[0].Items = append([]youtube.MusicItem{item}, feed.sections[0].Items...)
	}
	a.page.rowsDirty = true
}

type deletePlaylistState struct {
	open  bool
	id    string
	title string
	busy  bool
	err   string
}

func (a *app) askDeletePlaylist(id, title string) {
	if id == "" {
		return
	}
	a.deletePlaylist = &deletePlaylistState{open: true, id: id, title: title}
}

func (a *app) deletePlaylistDialog(c *ui.Context) {
	d := a.deletePlaylist
	if d == nil {
		return
	}
	if !d.open {
		a.deletePlaylist = nil
		return
	}
	sc := m3.Of(c).Scheme
	m3.Dialog(c, &d.open, 480, func() {
		m3.EmphasizedText(c, m3.HeadlineSmall, "Delete playlist")
		m3.Text(c, m3.BodyMedium, playlistName(d.title)+" is deleted from your account, and this cannot be undone.").TextColor(sc.OnSurfaceVariant)
		if d.err != "" {
			m3.Text(c, m3.BodyMedium, d.err).TextColor(sc.Error).MaxLines(4)
		}
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if d.busy {
				m3.LoadingIndicator(c, 40, false)
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Cancel", Kind: m3.TextOnly, Key: "cancel-delete-playlist"}).Clicked() {
				a.deletePlaylist = nil
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Delete", Kind: m3.Filled, Disabled: d.busy, Key: "confirm-delete-playlist"}).Clicked() {
				a.confirmDeletePlaylist()
			}
		})
	})
}

func (a *app) confirmDeletePlaylist() {
	d := a.deletePlaylist
	if d == nil || d.busy {
		return
	}
	client, id, title := a.client(), d.id, d.title
	d.busy, d.err = true, ""
	spawn(a, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		return client.DeletePlaylist(ctx, id)
	}, func(err error) {
		open := a.deletePlaylist == d
		d.busy = false
		switch {
		case err != nil && open:
			d.err = writeFailure(err)
		case err != nil:
			a.notice = "Could not delete " + playlistName(title) + ": " + writeFailure(err)
		default:
			if open {
				a.deletePlaylist = nil
			}
			a.forgetPlaylist(id)
			switch strings.TrimSpace(title) {
			case "":
				a.notice = "Playlist deleted"
			default:
				a.notice = "Deleted “" + title + "”"
			}
		}
	})
}

func playlistName(title string) string {
	if title = strings.TrimSpace(title); title == "" {
		return "This playlist"
	}
	return "“" + title + "”"
}

func writeFailure(err error) string {
	line := firstLine(err.Error())
	line, _, _ = strings.Cut(line, ": {")
	return line
}

func (a *app) forgetPlaylist(id string) {
	gone := strings.TrimPrefix(id, "VL")
	same := func(item youtube.MusicItem) bool {
		return strings.TrimPrefix(item.Target().ID, "VL") == gone
	}
	if r, current := a.route(); r.id == routePlaylist && strings.TrimPrefix(current, "VL") == gone {
		a.navigate(routeLibrary)
		return
	}
	feed := &a.page.feed
	feed.items = slices.DeleteFunc(feed.items, same)
	for i := range feed.sections {
		feed.sections[i].Items = slices.DeleteFunc(feed.sections[i].Items, same)
	}
	a.knownPlaylists.items = slices.DeleteFunc(a.knownPlaylists.items, same)
	a.page.rowsDirty = true
}

type addToPlaylistState struct {
	open      bool
	item      youtube.MusicItem
	playlists []youtube.MusicItem
	load      task
	busy      bool
	err       string
	list      ui.ListState
}

const likedMusicID = "LM"

func (a *app) openAddToPlaylist(item youtube.MusicItem) {
	d := &addToPlaylistState{open: true, item: item}
	a.addToPlaylist = d
	client := a.client()
	if known := a.knownPlaylists; known.client == client {
		d.playlists = known.items
	}
	startTask(a, &d.load, writeTimeout, func(ctx context.Context) (*youtube.BrowseResult, error) {
		return client.StreamLibrary(ctx, nil)
	}, func(library *youtube.BrowseResult, err error) {
		if err != nil {
			if len(d.playlists) == 0 {
				d.err = err.Error()
			}
			return
		}
		d.playlists = libraryPlaylists(library)
		a.knownPlaylists = knownPlaylists{client: client, items: d.playlists}
	})
}

type knownPlaylists struct {
	client *youtube.Client
	items  []youtube.MusicItem
}

func (a *app) dismissAddToPlaylist() {
	if d := a.addToPlaylist; d != nil {
		d.load.stop()
		a.addToPlaylist = nil
	}
}

func libraryPlaylists(library *youtube.BrowseResult) []youtube.MusicItem {
	if library == nil {
		return nil
	}
	groups := make([][]youtube.MusicItem, 0, len(library.Sections)+1)
	for _, section := range library.Sections {
		groups = append(groups, section.Items)
	}
	if len(groups) == 0 && len(library.Items) > 0 {
		groups = append(groups, library.Items)
	}
	var playlists []youtube.MusicItem
	seen := make(map[string]bool)
	for _, items := range groups {
		for _, item := range items {
			target := item.Target()
			if target.Kind != youtube.TargetPlaylist || strings.TrimPrefix(target.ID, "VL") == likedMusicID || seen[target.ID] {
				continue
			}
			seen[target.ID] = true
			playlists = append(playlists, item)
		}
	}
	return playlists
}

func (a *app) removeFromPlaylist(playlistID string, item youtube.MusicItem) {
	client := a.client()
	spawn(a, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		return client.RemoveFromPlaylist(ctx, playlistID, item.PlaylistSetID)
	}, func(err error) {
		if err != nil {
			a.notice = "Could not remove “" + item.Title + "” from playlist: " + firstLine(err.Error())
			return
		}
		a.notice = "Removed “" + item.Title + "” from playlist"
		if route, currentID := a.route(); route.id == routePlaylist && currentID == playlistID {
			a.loadPage(route, playlistID)
		}
	})
}

func (a *app) choosePlaylist(playlist youtube.MusicItem) {
	d := a.addToPlaylist
	target := playlist.Target()
	if d == nil || d.busy || d.item.VideoID == "" || target.ID == "" {
		return
	}
	client, item := a.client(), d.item
	d.busy, d.err = true, ""
	spawn(a, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		return client.AddToPlaylist(ctx, target.ID, item.VideoID)
	}, func(err error) {
		d.busy = false
		if a.client() != client {
			return
		}
		open := a.addToPlaylist == d
		switch {
		case errors.Is(err, youtube.ErrAlreadyInPlaylist):
			if open {
				a.dismissAddToPlaylist()
			}
			a.notice = "“" + item.Title + "” is already in “" + playlist.Title + "”"
		case err != nil && open:
			d.err = err.Error()
		case err != nil:
			a.notice = "Could not add “" + item.Title + "” to “" + playlist.Title + "”: " + firstLine(err.Error())
		default:
			if open {
				a.dismissAddToPlaylist()
			}
			a.pages.Clear()
			if r, id := a.route(); r.id == routePlaylist && strings.TrimPrefix(id, "VL") == strings.TrimPrefix(target.ID, "VL") {
				a.loadPage(r, id)
			}
			a.notice = "Added “" + item.Title + "” to “" + playlist.Title + "”"
		}
	})
}

func (a *app) addToPlaylistDialog(c *ui.Context) {
	d := a.addToPlaylist
	if d == nil {
		return
	}
	if !d.open {
		a.dismissAddToPlaylist()
		return
	}
	sc := m3.Of(c).Scheme
	m3.Dialog(c, &d.open, 480, func() {
		m3.EmphasizedText(c, m3.HeadlineSmall, "Add to playlist")
		if title := d.item.Title; title != "" {
			m3.Text(c, m3.BodyMedium, title).TextColor(sc.OnSurfaceVariant).SingleLine()
		}
		switch {
		case len(d.playlists) == 0 && d.load.busy():
			ui.Row(c).Height(72).Center().Children(func() { m3.LoadingIndicator(c, 40, true) })
		case d.err != "":
			m3.Text(c, m3.BodyMedium, d.err).TextColor(sc.Error).MaxLines(4)
		case len(d.playlists) == 0:
			m3.Text(c, m3.BodyMedium, "You have no playlists yet.").TextColor(sc.OnSurfaceVariant)
		default:
			playlists := d.playlists
			list := &d.list
			list.Key = func(i int) any {
				if i >= len(playlists) {
					return nil
				}
				return itemKey("add-to", playlists[i])
			}
			ui.List(c, list, len(playlists), func(i int) {
				a.playlistChoice(c, playlists[i])
			}).Height(min(float32(len(playlists))*playlistChoiceHeight, 280))
		}
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if d.busy {
				m3.LoadingIndicator(c, 40, false)
			}
			if m3.Button(c, m3.ButtonSpec{Label: "New playlist", Kind: m3.TextOnly, Key: "new-playlist-from-picker"}).Clicked() {
				song := d.item
				a.dismissAddToPlaylist()
				a.openNewPlaylist(song)
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Cancel", Kind: m3.TextOnly, Key: "cancel-add-to-playlist"}).Clicked() {
				a.dismissAddToPlaylist()
			}
		})
	})
}

const playlistChoiceHeight = 58

func (a *app) playlistChoice(c *ui.Context, playlist youtube.MusicItem) {
	sc := m3.Of(c).Scheme
	row := ui.ButtonBase(c.Key(itemKey("add-to", playlist)))
	row.Height(56).Margin(0, 0, 2).PaddingX(8).Gap(12).AlignItems(ui.Center).Radius(m3.Large).Cursor(ui.CursorPointer).
		Label("Add to " + playlist.Title).
		Background(m3.StateFill(ui.Transparent, sc.OnSurface, row.Hovered(), row.Pressed(), row.FocusVisible()))
	row.Children(func() {
		a.art(c, playlist.Thumbnail, 96, 40, m3.Small)
		m3.Text(c, m3.BodyLarge, playlist.Title).Grow(1).SingleLine().TextColor(sc.OnSurface)
	})
	if row.Clicked() {
		a.choosePlaylist(playlist)
	}
}

const detailSearchWidth = 520

const detailSearchButton = 40

func (a *app) playlistSearch(c *ui.Context) {
	control := ui.Row(c.Key("playlist-search")).MinWidth(0).Grow(1).Shrink(1)
	target := float32(0)
	if a.detailSearchOpen {
		target = 1
	}
	grown := m3.Animate(control, "open", target, m3.SpatialFast)
	if !a.detailSearchOpen && grown < 0.02 {
		control.MaxWidth(detailSearchButton)
		control.Children(func() {
			button := m3.IconButton(c, m3.IconButtonSpec{
				Icon: m3.IconSearch, Label: "Search in this playlist",
				Kind: m3.StandardIcon, Size: m3.Small40, Key: "playlist-search-open",
			})
			if button.Clicked() {
				a.detailSearchOpen = true
				a.detailSearchField.Focus()
				a.detailSearchHadFocus = false
			}
		})
		return
	}
	maxWidth := float32(detailSearchWidth)
	if a.listWidthLastFrame > 0 {
		maxWidth = min(maxWidth, a.listWidthLastFrame-48)
	}
	control.MaxWidth(detailSearchButton + (maxWidth-detailSearchButton)*min(max(grown, 0), 1))
	control.Children(func() {
		result, _, _ := m3.SearchBar(c, m3.SearchSpec{
			Value:       &a.detailFilter,
			Placeholder: "Search in this playlist",
			Key:         "playlist-search-bar",
			Focus:       &a.detailSearchField,
			MaxWidth:    maxWidth,
			Dismiss:     true,
			Underline:   true,
			NoClear:     true,
		})
		if result.Dismissed {
			a.closePlaylistSearch()
		} else {
			if a.detailSearchHadFocus && !result.Focused && a.detailFilter == "" {
				a.closePlaylistSearch()
			}
			a.detailSearchHadFocus = result.Focused
		}
	})
}

func (a *app) closePlaylistSearch() {
	a.detailSearchOpen, a.detailSearchHadFocus = false, false
	a.detailFilter = ""
}
