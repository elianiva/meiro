package main

// The writes a signed-in user makes to their account from the pages: liking a
// track, saving an album or a playlist to the library, and following an
// artist. Each shows at once, is written behind, and is taken back with a
// notice if YouTube refuses it.

import (
	"context"
	"slices"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// writeBehind writes a change the app already shows, off the main thread, and
// runs done with the result on it. key names what the write changes: callers
// ignore a press while a write of the same key is out, so the answers cannot
// arrive in another order than the presses. done does not run once another
// account is in use: its pages say their own.
func (a *app) writeBehind(key string, write func(ctx context.Context, client *youtube.Client) error, done func(err error)) {
	a.writing[key] = true
	client := a.client()
	spawn(a, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		return write(ctx, client)
	}, func(err error) {
		if a.client() != client {
			return
		}
		delete(a.writing, key)
		done(err)
	})
}

// likeLabel is what a track's menu offers for its like: to give one, or to
// take it back.
func likeLabel(item youtube.MusicItem) (label string, icon *ui.SVG) {
	if item.Like == youtube.Liked {
		return "Unlike", m3.IconFavorite
	}
	return "Like", m3.IconFavoriteBorder
}

// canLike says whether a track can be liked now: it needs an account, and a
// track.
func (a *app) canLike(item youtube.MusicItem) bool {
	return a.signedIn() && item.VideoID != ""
}

// toggleLike likes a track, or takes its like back, as the track shows it. The
// change shows at once and is written behind it; a write YouTube refuses is
// taken back, and the user is told. A track is written one change at a time:
// a press while its last change is out is ignored, so the answers cannot
// arrive in another order than the presses.
func (a *app) toggleLike(item youtube.MusicItem) {
	a.toggleRating(item, youtube.Liked)
}

func (a *app) toggleRating(item youtube.MusicItem, rating youtube.LikeStatus) {
	id := item.VideoID
	if !a.canLike(item) || a.writing["like:"+id] {
		return
	}
	before, after := item.Like, rating
	if before == rating {
		after = youtube.Indifferent
	}
	a.setLike(id, after, false)
	a.writeBehind("like:"+id, func(ctx context.Context, client *youtube.Client) error {
		return client.RateTrack(ctx, id, after)
	}, func(err error) {
		if err == nil {
			return
		}
		a.setLike(id, before, false)
		var verb string
		switch [2]youtube.LikeStatus{rating, after} {
		case [2]youtube.LikeStatus{youtube.Liked, youtube.Indifferent}:
			verb = "unlike"
		case [2]youtube.LikeStatus{youtube.Disliked, youtube.Indifferent}:
			verb = "remove dislike from"
		case [2]youtube.LikeStatus{youtube.Liked, youtube.Liked}:
			verb = "like"
		case [2]youtube.LikeStatus{youtube.Disliked, youtube.Disliked}:
			verb = "dislike"
		}
		a.notice = "Could not " + verb + " “" + item.Title + "”: " + firstLine(err.Error())
	})
}

// eachItem calls fn with every copy of a track or an item the app holds: the
// one playing, the queue, the page shown and the related songs. A kept page is
// not among them: callers that change what they hold drop the cache instead,
// as a page it holds is read again from YouTube.
func (a *app) eachItem(fn func(*youtube.MusicItem)) {
	fn(&a.current)
	a.queue.each(fn)
	each := func(items []youtube.MusicItem) {
		for i := range items {
			fn(&items[i])
		}
	}
	for _, feed := range []*pageState{&a.page.feed, &a.search.pageState} {
		each(feed.items)
		for i := range feed.sections {
			each(feed.sections[i].Items)
		}
	}
	each(a.nowPlaying.related.items)
	a.page.rowsDirty = true
}

// setLike gives every copy of a track the status. With unknownOnly, only
// copies that do not know their status take it, which is how a status
// YouTube sent is learned.
func (a *app) setLike(videoID string, status youtube.LikeStatus, unknownOnly bool) {
	a.eachItem(func(item *youtube.MusicItem) {
		if item.VideoID == videoID && (!unknownOnly || item.Like == youtube.LikeUnknown) {
			item.Like = status
		}
	})
	if !unknownOnly {
		a.pages.Clear()
	}
}

// learnLikes takes the statuses YouTube sent with a list of tracks for the
// copies of them the app holds without one.
func (a *app) learnLikes(items []youtube.MusicItem) {
	for _, item := range items {
		if item.Like != youtube.LikeUnknown && !a.writing["like:"+item.VideoID] {
			a.setLike(item.VideoID, item.Like, true)
		}
	}
}

// forgetAccountState drops what the items held say about the account that has just
// gone: another account has its own.
func (a *app) forgetAccountState() {
	clear(a.writing)
	a.page.library, a.page.subscription = youtube.LibraryState{}, youtube.Subscription{}
	a.eachItem(func(item *youtube.MusicItem) { item.Like, item.Library = youtube.LikeUnknown, nil })
}

// likeButton is the heart of the full player: filled while the track is liked.
// It is not built for a signed-out user, who cannot like.
func (a *app) likeButton(c *ui.Context) {
	if !a.canLike(a.current) {
		return
	}
	liked := a.current.Like == youtube.Liked
	label, icon := "Like "+a.current.Title, m3.IconFavoriteBorder
	if liked {
		label, icon = "Unlike "+a.current.Title, m3.IconFavorite
	}
	if m3.IconButton(c, m3.IconButtonSpec{
		Icon: icon, Label: label, Key: "like", Kind: m3.TonalIcon, Toggle: true, Selected: liked,
	}).Clicked() {
		a.toggleLike(a.current)
	}
}

func (a *app) dislikeButton(c *ui.Context) {
	if !a.canLike(a.current) {
		return
	}
	disliked := a.current.Like == youtube.Disliked
	label, icon := "Dislike "+a.current.Title, m3.IconThumbDownOutline
	if disliked {
		label, icon = "Remove dislike from "+a.current.Title, m3.IconThumbDown
	}
	if m3.IconButton(c, m3.IconButtonSpec{
		Icon: icon, Label: label, Key: "dislike", Toggle: true, Selected: disliked,
	}).Clicked() {
		a.toggleRating(a.current, youtube.Disliked)
	}
}

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
	save := !library.Saved
	a.setSaved(id, save)
	a.writeBehind(key, func(ctx context.Context, client *youtube.Client) error {
		if save {
			return client.AddToLibrary(ctx, id)
		}
		return client.RemoveFromLibrary(ctx, id)
	}, func(err error) {
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
	if m3.Button(c, m3.ButtonSpec{Label: label, Icon: icon, Kind: kind, Size: a.heroButtonSize(), Key: "hero-library"}).Clicked() {
		a.toggleLibrary(library, a.page.detail.title)
	}
}

// canSubscribe says whether an artist can be followed, or unfollowed, now: it
// needs an account, and a page that offered a button.
func (a *app) canSubscribe(s youtube.Subscription) bool {
	return a.signedIn() && s.ChannelID != ""
}

// toggleSubscription follows the artist of the page shown, or stops following
// them, as the button shows. Like a like, the change shows at once and is
// written behind it, taken back if YouTube refuses it, and a press while the
// last change is out is ignored.
func (a *app) toggleSubscription() {
	s, name := a.page.subscription, a.page.detail.title
	if name == "" {
		name = "this artist"
	}
	key := "subscribe:" + s.ChannelID
	if !a.canSubscribe(s) || a.writing[key] {
		return
	}
	follow := !s.Subscribed
	a.setSubscribed(s.ChannelID, follow)
	a.writeBehind(key, func(ctx context.Context, client *youtube.Client) error {
		if follow {
			return client.Subscribe(ctx, s.ChannelID)
		}
		return client.Unsubscribe(ctx, s.ChannelID)
	}, func(err error) {
		switch {
		case err != nil && follow:
			a.setSubscribed(s.ChannelID, false)
			a.notice = "Could not subscribe to " + name + ": " + firstLine(err.Error())
		case err != nil:
			a.setSubscribed(s.ChannelID, true)
			a.notice = "Could not unsubscribe from " + name + ": " + firstLine(err.Error())
		case follow:
			a.notice = "Subscribed to " + name
		default:
			a.notice = "Unsubscribed from " + name
		}
	})
}

// setSubscribed gives the page shown the state, if it is the artist's. The
// pages kept in the cache are dropped, so the library, which lists the artists
// followed, is read again the next time it is shown.
func (a *app) setSubscribed(channelID string, subscribed bool) {
	if a.page.subscription.ChannelID == channelID {
		a.page.subscription.Subscribed = subscribed
	}
	a.pages.Clear()
}

// subscribeButton is the button of an artist's page that follows them or stops
// following them. It is not built for a signed-out user, nor for a page that
// offers none.
func (a *app) subscribeButton(c *ui.Context) {
	s := a.page.subscription
	if !a.canSubscribe(s) {
		return
	}
	label, icon, kind := "Subscribe", m3.IconNotifications, m3.Tonal
	if s.Subscribed {
		label, icon, kind = "Subscribed", m3.IconCheck, m3.Outlined
	}
	if m3.Button(c, m3.ButtonSpec{Label: label, Icon: icon, Kind: kind, Size: a.heroButtonSize(), Key: "hero-subscribe"}).Clicked() {
		a.toggleSubscription()
	}
}
