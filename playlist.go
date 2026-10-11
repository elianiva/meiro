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
// being typed, whether the write is out, and what it answered. attempt numbers
// the openings, so an answer that lands after the dialog moved on is dropped.
type newPlaylistState struct {
	open    bool
	name    string
	busy    bool
	err     string
	attempt uint64
}

// openNewPlaylist asks for the name of a playlist to make.
func (a *app) openNewPlaylist() {
	a.newPlaylist = newPlaylistState{open: true, attempt: a.newPlaylist.attempt + 1}
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
	attempt := a.newPlaylist.attempt
	a.newPlaylist.busy, a.newPlaylist.err = true, ""
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := client.CreatePlaylist(ctx, name)
		a.update(func() {
			if err == nil && a.router.Path() == "/library" {
				// The playlist belongs in the library, whether or not the
				// dialog that named it is still open.
				a.reloadPage()
			}
			if !a.newPlaylist.open || a.newPlaylist.attempt != attempt {
				return // the dialog moved on while the playlist was being made
			}
			if err != nil {
				a.newPlaylist.busy, a.newPlaylist.err = false, err.Error()
				return
			}
			a.newPlaylist = newPlaylistState{attempt: attempt}
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
