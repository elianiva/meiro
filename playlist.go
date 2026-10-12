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

// newPlaylistState is the dialog that makes a playlist: the name being
// typed, the song it is asked to hold, whether the write is out, and what it
// answered. The app holds one while the dialog is open, and nil otherwise, so
// a write that lands after its dialog closed knows it.
type newPlaylistState struct {
	open bool
	name string
	song youtube.MusicItem
	busy bool
	err  string
}

// playlistTimeout bounds a playlist read or write.
const playlistTimeout = time.Minute

// openNewPlaylist asks for the name of a playlist to make, and holds a song in
// it when one is given.
func (a *app) openNewPlaylist(song youtube.MusicItem) {
	a.newPlaylist = &newPlaylistState{open: true, song: song}
}

// createNewPlaylist makes the playlist the dialog names, and shows it in the
// library.
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
		ctx, cancel := context.WithTimeout(context.Background(), playlistTimeout)
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
			// YouTube's library does not hold a new playlist the moment it
			// answers, so the playlist is put in the library the app shows
			// rather than read back from YouTube and not found.
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

// newPlaylistDialog asks for a name, and shows what YouTube answered.
func (a *app) newPlaylistDialog(c *ui.Context) {
	d := a.newPlaylist
	if d == nil {
		return
	}
	if !d.open {
		a.newPlaylist = nil // closed by its scrim or the Escape key
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

// showPlaylist puts a playlist the app has just made at the front of the
// library, which is where YouTube lists a new one. YouTube answers the write
// before its library holds the playlist, so reading the library back loses it;
// the next load of the page replaces what is shown here.
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

// addToPlaylistState is the dialog that puts a song in a playlist: the song,
// the playlists to choose from, and what a choice answered. The app holds one
// while the dialog is open, and nil otherwise.
type addToPlaylistState struct {
	open      bool
	item      youtube.MusicItem
	playlists []youtube.MusicItem
	// load reads the library's playlists; busy is set while a choice is
	// being written.
	load task
	busy bool
	err  string
	list ui.ListState
}

// likedMusicID is the playlist a library holds thumbs-up songs in. It is not
// offered to put a song in: the thumb is what does that.
const likedMusicID = "LM"

// openAddToPlaylist asks which playlist a song goes into, and reads the
// playlists to choose from.
func (a *app) openAddToPlaylist(item youtube.MusicItem) {
	d := &addToPlaylistState{open: true, item: item}
	a.addToPlaylist = d
	client := a.client()
	// The playlists the library last showed are offered at once, while the
	// library is read again to catch up with what changed since.
	if known := a.knownPlaylists; known.client == client {
		d.playlists = known.items
	}
	startTask(a, &d.load, playlistTimeout, func(ctx context.Context) (*youtube.BrowseResult, error) {
		return client.GetAllLibrary(ctx)
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

// knownPlaylists are the playlists a library last held, kept so the picker
// need not wait for the whole library to be read again. They belong to the
// client that read them: another account has its own.
type knownPlaylists struct {
	client *youtube.Client
	items  []youtube.MusicItem
}

// dismissAddToPlaylist closes the picker and stops its read.
func (a *app) dismissAddToPlaylist() {
	if d := a.addToPlaylist; d != nil {
		d.load.stop()
		a.addToPlaylist = nil
	}
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

// choosePlaylist puts the song the dialog holds into the playlist chosen.
func (a *app) choosePlaylist(playlist youtube.MusicItem) {
	d := a.addToPlaylist
	target := playlist.Target()
	if d == nil || d.busy || d.item.VideoID == "" || target.ID == "" {
		return
	}
	client, item := a.client(), d.item
	d.busy, d.err = true, ""
	spawn(a, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), playlistTimeout)
		defer cancel()
		return client.AddToPlaylist(ctx, target.ID, item.VideoID)
	}, func(err error) {
		d.busy = false
		open := a.addToPlaylist == d
		switch {
		case err != nil && open:
			d.err = err.Error()
		case err != nil:
			a.notice = "Could not add “" + item.Title + "” to “" + playlist.Title + "”: " + firstLine(err.Error())
		default:
			if open {
				a.dismissAddToPlaylist()
			}
			a.notice = "Added “" + item.Title + "” to “" + playlist.Title + "”"
		}
	})
}

// addToPlaylistDialog asks which playlist a song goes into.
func (a *app) addToPlaylistDialog(c *ui.Context) {
	d := a.addToPlaylist
	if d == nil {
		return
	}
	if !d.open {
		a.dismissAddToPlaylist() // closed by its scrim or the Escape key
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
			// A library can hold hundreds of playlists: the list builds, and
			// asks for the artwork of, only those in view.
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

// playlistChoiceHeight is the room a row of the picker takes, with the gap
// below it.
const playlistChoiceHeight = 58

// playlistChoice is one row of the picker: a playlist the song can go into.
func (a *app) playlistChoice(c *ui.Context, playlist youtube.MusicItem) {
	sc := m3.Of(c).Scheme
	row := ui.ButtonBase(c.Key(itemKey("add-to", playlist)))
	row.Height(56).Margin(0, 0, 2).PaddingX(8).Gap(12).AlignItems(ui.Center).Radius(m3.Large).Cursor(ui.CursorPointer).
		Label("Add to " + playlist.Title).
		Background(m3.StateFill(ui.Transparent, sc.OnSurface, row.Hovered(), row.Pressed(), row.FocusVisible()))
	row.Children(func() {
		m3.Art(c, a.thumbs.Bitmap(playlist.Thumbnail, 96), 40, m3.Small)
		m3.Text(c, m3.BodyLarge, playlist.Title).Grow(1).SingleLine().TextColor(sc.OnSurface)
	})
	if row.Clicked() {
		a.choosePlaylist(playlist)
	}
}
