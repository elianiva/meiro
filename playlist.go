package main

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// newPlaylistID marks the tile that makes a playlist among the items of a
// page. No playlist, album, artist or track ID is one, so it cannot collide.
const newPlaylistID = "new-playlist"

// newPlaylistItem is the tile the library leads with: a plus where a playlist
// shows its artwork, which asks for a name instead of opening a page.
var newPlaylistItem = youtube.MusicItem{ID: newPlaylistID, Title: "New playlist"}

// isNewPlaylist says whether an item is that tile.
func isNewPlaylist(item youtube.MusicItem) bool { return item.ID == newPlaylistID }

// newPlaylistState is what the dialog that makes a playlist holds: the name
// being typed, the song it is asked to hold, whether the write is out, and what
// it answered. attempt numbers the openings, so an answer that lands after the
// dialog moved on is dropped.
type newPlaylistState struct {
	open    bool
	name    string
	song    youtube.MusicItem
	busy    bool
	err     string
	attempt uint64
}

// openNewPlaylist asks for the name of a playlist to make, and holds a song in
// it when one is given.
func (a *app) openNewPlaylist(song youtube.MusicItem) {
	a.newPlaylist = newPlaylistState{open: true, song: song, attempt: a.newPlaylist.attempt + 1}
}

// dismissNewPlaylist drops the name being typed.
func (a *app) dismissNewPlaylist() {
	a.newPlaylist = newPlaylistState{attempt: a.newPlaylist.attempt + 1}
}

// createNewPlaylist makes the playlist the dialog names, and shows it in the
// library. It runs off the main thread.
func (a *app) createNewPlaylist() {
	name := strings.TrimSpace(a.newPlaylist.name)
	if a.newPlaylist.busy || name == "" {
		return
	}
	client := a.client()
	if client == nil {
		return
	}
	song, attempt := a.newPlaylist.song, a.newPlaylist.attempt
	a.newPlaylist.busy, a.newPlaylist.err = true, ""
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		id, err := client.CreatePlaylist(ctx, name, song.VideoID)
		a.update(func() {
			if err == nil {
				// YouTube's library does not hold a new playlist the moment it
				// answers, so the playlist is put in the library the app shows
				// rather than read back from YouTube and not found.
				a.showPlaylist(id, name)
			}
			if !a.newPlaylist.open || a.newPlaylist.attempt != attempt {
				return // the dialog moved on while the playlist was being made
			}
			if err != nil {
				a.newPlaylist.busy, a.newPlaylist.err = false, err.Error()
				return
			}
			a.newPlaylist = newPlaylistState{attempt: attempt}
			if song.VideoID != "" {
				a.notice = "Added “" + song.Title + "” to “" + name + "”"
				return
			}
			a.notice = "Created “" + name + "”"
		})
	})
}

// newPlaylistDialog asks for a name, and shows what YouTube answered.
func (a *app) newPlaylistDialog(c *ui.Context) {
	if !a.newPlaylist.open {
		return
	}
	sc := m3.Of(c).Scheme
	m3.Dialog(c, &a.newPlaylist.open, 480, func() {
		m3.EmphasizedText(c, m3.HeadlineSmall, "New playlist")
		if song := a.newPlaylist.song; song.VideoID != "" {
			m3.Text(c, m3.BodyMedium, "It will hold “"+song.Title+"”.").TextColor(sc.OnSurfaceVariant).SingleLine()
		}
		m3.Text(c, m3.BodyMedium, "Meiro makes it private to your account.").TextColor(sc.OnSurfaceVariant)
		box := ui.Column(c.Key("playlist-name")).Padding(12, 16).Radius(m3.Large).Background(sc.SurfaceContainerHighest)
		if box.FocusWithin() {
			box.Border(2, sc.Primary)
		}
		box.Children(func() {
			ui.TextInputBase(c.Key("playlist-name-field"), &a.newPlaylist.name).
				Placeholder("Playlist name").Label("Name").TextColor(sc.OnSurface).FontSize(16).AutoFocus().
				OnSubmit(a.createNewPlaylist)
		})
		if a.newPlaylist.err != "" {
			m3.Text(c, m3.BodyMedium, a.newPlaylist.err).TextColor(sc.Error).MaxLines(4)
		}
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if a.newPlaylist.busy {
				m3.LoadingIndicator(c, 40, false)
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Cancel", Kind: m3.TextOnly, Key: "cancel-new-playlist"}).Clicked() {
				a.dismissNewPlaylist()
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Create", Disabled: a.newPlaylist.busy || strings.TrimSpace(a.newPlaylist.name) == "", Key: "create-playlist"}).Clicked() {
				a.createNewPlaylist()
			}
		})
	})
}

// showPlaylist puts a playlist the app has just made at the front of the
// library, which is where YouTube lists a new one. YouTube answers the write
// before its library holds the playlist, so reading the library back loses it;
// the next load of the page replaces what is shown here.
func (a *app) showPlaylist(id, name string) {
	if id == "" || a.router.Path() != "/library" || name == "" {
		return
	}
	item := youtube.MusicItem{ID: "VL" + id, BrowseID: "VL" + id, Title: name, Subtitle: "Playlist"}
	if len(a.feed.sections) == 0 {
		a.feed.items = append([]youtube.MusicItem{item}, a.feed.items...)
	} else {
		a.feed.sections[0].Items = append([]youtube.MusicItem{item}, a.feed.sections[0].Items...)
	}
	a.rowsDirty = true
}

// addToPlaylistState is what the dialog that puts a song in a playlist holds:
// the song, the playlists to choose from, and what a choice answered. attempt
// numbers the openings, so an answer that lands after the dialog moved on is
// dropped.
type addToPlaylistState struct {
	open      bool
	item      youtube.MusicItem
	playlists []youtube.MusicItem
	loading   bool
	busy      bool
	err       string
	attempt   uint64
}

// likedMusicID is the playlist a library holds thumbs-up songs in. It is not
// offered to put a song in: the thumb is what does that.
const likedMusicID = "LM"

// openAddToPlaylist asks which playlist a song goes into, and reads the
// playlists to choose from.
func (a *app) openAddToPlaylist(item youtube.MusicItem) {
	a.addToPlaylist = addToPlaylistState{
		open: true, item: item, loading: true, attempt: a.addToPlaylist.attempt + 1,
	}
	attempt := a.addToPlaylist.attempt
	client := a.client()
	if client == nil {
		return
	}
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		library, err := client.GetAllLibrary(ctx)
		a.update(func() {
			if !a.addToPlaylist.open || a.addToPlaylist.attempt != attempt {
				return // the dialog moved on while the playlists were being read
			}
			a.addToPlaylist.loading = false
			if err != nil {
				a.addToPlaylist.err = err.Error()
				return
			}
			a.addToPlaylist.playlists = libraryPlaylists(library)
		})
	})
}

// dismissAddToPlaylist drops the choice being made.
func (a *app) dismissAddToPlaylist() {
	a.addToPlaylist = addToPlaylistState{attempt: a.addToPlaylist.attempt + 1}
}

// libraryPlaylists picks the playlists out of a library page, in the order the
// page holds them, once each, leaving out the one a thumb fills.
func libraryPlaylists(library *youtube.BrowseResult) []youtube.MusicItem {
	if library == nil {
		return nil
	}
	// A page's sections hold its items as well, so the sections are what to
	// read: the loose items are only for a page without any.
	groups := make([][]youtube.MusicItem, 0, len(library.Sections)+1)
	for _, section := range library.Sections {
		groups = append(groups, section.Items)
	}
	if len(groups) == 0 && len(library.Items) > 0 {
		groups = append(groups, library.Items)
	}
	var playlists []youtube.MusicItem
	// A library page lists a playlist in more than one shelf, and the picker
	// gives each row a key of its playlist: two rows with one key would share
	// one state, and the click on one would land on the other.
	seen := make(map[string]bool)
	for _, items := range groups {
		for _, item := range items {
			kind, id := targetOf(item)
			if kind != pagePlaylist || strings.TrimPrefix(id, "VL") == likedMusicID || seen[id] {
				continue
			}
			seen[id] = true
			playlists = append(playlists, item)
		}
	}
	return playlists
}

// choosePlaylist puts the song the dialog holds into the playlist chosen.
func (a *app) choosePlaylist(playlist youtube.MusicItem) {
	if a.addToPlaylist.busy || a.addToPlaylist.item.VideoID == "" {
		return
	}
	client := a.client()
	if client == nil {
		return
	}
	_, id := targetOf(playlist)
	if id == "" {
		return
	}
	item, attempt := a.addToPlaylist.item, a.addToPlaylist.attempt
	a.addToPlaylist.busy, a.addToPlaylist.err = true, ""
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		err := client.AddToPlaylist(ctx, id, item.VideoID)
		a.update(func() {
			if !a.addToPlaylist.open || a.addToPlaylist.attempt != attempt {
				return // the dialog moved on while the write was out
			}
			if err != nil {
				a.addToPlaylist.busy, a.addToPlaylist.err = false, err.Error()
				return
			}
			a.addToPlaylist = addToPlaylistState{attempt: attempt}
			a.notice = "Added “" + item.Title + "” to “" + playlist.Title + "”"
		})
	})
}

// addToPlaylistDialog asks which playlist a song goes into.
func (a *app) addToPlaylistDialog(c *ui.Context) {
	if !a.addToPlaylist.open {
		return
	}
	sc := m3.Of(c).Scheme
	m3.Dialog(c, &a.addToPlaylist.open, 480, func() {
		m3.EmphasizedText(c, m3.HeadlineSmall, "Add to playlist")
		if title := a.addToPlaylist.item.Title; title != "" {
			m3.Text(c, m3.BodyMedium, title).TextColor(sc.OnSurfaceVariant).SingleLine()
		}
		switch {
		case a.addToPlaylist.loading:
			ui.Row(c).Height(72).Center().Children(func() { m3.LoadingIndicator(c, 40, true) })
		case a.addToPlaylist.err != "":
			m3.Text(c, m3.BodyMedium, a.addToPlaylist.err).TextColor(sc.Error).MaxLines(4)
		case len(a.addToPlaylist.playlists) == 0:
			m3.Text(c, m3.BodyMedium, "You have no playlists yet.").TextColor(sc.OnSurfaceVariant)
		default:
			ui.Scroll(c).MaxHeight(280).Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					for _, playlist := range a.addToPlaylist.playlists {
						a.playlistChoice(c, playlist)
					}
				})
			})
		}
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if a.addToPlaylist.busy {
				m3.LoadingIndicator(c, 40, false)
			}
			if m3.Button(c, m3.ButtonSpec{Label: "New playlist", Kind: m3.TextOnly, Key: "new-playlist-from-picker"}).Clicked() {
				song := a.addToPlaylist.item
				a.dismissAddToPlaylist()
				a.openNewPlaylist(song)
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Cancel", Kind: m3.TextOnly, Key: "cancel-add-to-playlist"}).Clicked() {
				a.dismissAddToPlaylist()
			}
		})
	})
}

// playlistChoice is one row of the picker: a playlist the song can go into.
func (a *app) playlistChoice(c *ui.Context, playlist youtube.MusicItem) {
	sc := m3.Of(c).Scheme
	row := ui.ButtonBase(c.Key(itemKey("add-to", playlist)))
	row.Height(56).PaddingX(8).Gap(12).AlignItems(ui.Center).Radius(m3.Large).Cursor(ui.CursorPointer).
		Label("Add to " + playlist.Title).
		Background(m3.StateFill(ui.Transparent, sc.OnSurface, row.Hovered(), row.Pressed(), row.FocusVisible()))
	row.Children(func() {
		m3.Art(c, a.thumbs.bitmapIf(playlist.Thumbnail, 96, true), 40, m3.Small)
		m3.Text(c, m3.BodyLarge, playlist.Title).Grow(1).SingleLine().TextColor(sc.OnSurface)
	})
	if row.Clicked() {
		a.choosePlaylist(playlist)
	}
}
