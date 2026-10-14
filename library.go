package main

import (
	"context"
	"slices"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// canSave says whether an album or a playlist can be saved to the library, or
// taken out of it, now: it needs an account, and a page that offered a way.
func (a *app) canSave(library youtube.LibraryState) bool {
	return a.signedIn() && library.ID != ""
}

// libraryLabel is what the page's button and the card's menu offer for an
// album or a playlist: to save it, or to take it out.
func libraryLabel(library youtube.LibraryState) (label string, icon *ui.SVG) {
	if library.Saved {
		return "Remove from library", m3.IconCheck
	}
	return "Save to library", m3.IconAdd
}

// named is how a notice calls an album or a playlist: by its title in quotes,
// or, for a page that has not named itself yet, as what it is.
func named(title string) string {
	if title == "" {
		return "this album or playlist"
	}
	return "“" + title + "”"
}

// toggleLibrary saves an album or a playlist to the library, or takes it out,
// as it shows. The change shows at once and is written behind it; a write
// YouTube refuses is taken back, and the user is told. Like a track's like, a
// press while the last change is out is ignored.
func (a *app) toggleLibrary(library youtube.LibraryState, title string) {
	id := library.ID
	key := "library:" + id
	if !a.canSave(library) || a.writing[key] {
		return
	}
	if a.writing == nil {
		a.writing = make(map[string]bool)
	}
	a.writing[key] = true
	save := !library.Saved
	a.setSaved(id, save)
	client := a.client()
	spawn(a, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), playlistTimeout)
		defer cancel()
		if save {
			return client.AddToLibrary(ctx, id)
		}
		return client.RemoveFromLibrary(ctx, id)
	}, func(err error) {
		if a.client() != client {
			return // another account is in use now: its pages say their own
		}
		delete(a.writing, key)
		switch {
		case err != nil && save:
			a.setSaved(id, false)
			a.notice = "Could not save " + named(title) + ": " + firstLine(err.Error())
		case err != nil:
			a.setSaved(id, true)
			a.notice = "Could not remove " + named(title) + ": " + firstLine(err.Error())
		case save:
			a.notice = "Saved " + named(title) + " to your library"
		default:
			a.notice = "Removed " + named(title) + " from your library"
			a.dropFromLibraryPage(id)
		}
	})
}

// setSaved gives everything the app holds about a playlist the state. The
// library and the playlists it offers to put a song in are read again the next
// time they are asked for, so they hold what YouTube says of the change.
func (a *app) setSaved(id string, saved bool) {
	a.eachItem(func(item *youtube.MusicItem) {
		if item.Library != nil && item.Library.ID == id {
			item.Library = &youtube.LibraryState{ID: id, Saved: saved}
		}
	})
	if a.page.library.ID == id {
		a.page.library.Saved = saved
	}
	a.pages.Clear()
	a.knownPlaylists = knownPlaylists{}
}

// dropFromLibraryPage takes a playlist that has just left the library off the
// library page, if that is the page shown: reading the page back would show
// what YouTube still holds before it has taken the change.
func (a *app) dropFromLibraryPage(id string) {
	if r, _ := a.route(); r.id != routeLibrary {
		return
	}
	gone := func(item youtube.MusicItem) bool { return item.InLibrary().ID == id }
	feed := &a.page.feed
	feed.items = slices.DeleteFunc(feed.items, gone)
	for i := range feed.sections {
		feed.sections[i].Items = slices.DeleteFunc(feed.sections[i].Items, gone)
	}
	a.page.rowsDirty = true
}

// libraryButton is the button of an album's or a playlist's page that saves it
// to the library or takes it out. It is not built for a signed-out user, nor
// for a page that offers no way to save it.
func (a *app) libraryButton(c *ui.Context) {
	library := a.page.library
	if !a.canSave(library) {
		return
	}
	label, icon := libraryLabel(library)
	kind := m3.Tonal
	if library.Saved {
		kind = m3.Outlined
	}
	if m3.Button(c, m3.ButtonSpec{Label: label, Icon: icon, Kind: kind, Size: m3.Medium56, Key: "hero-library"}).Clicked() {
		a.toggleLibrary(library, a.page.detail.title)
	}
}
