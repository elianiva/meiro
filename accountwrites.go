package main

import (
	"context"
	"slices"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

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

func likeLabel(item youtube.MusicItem) (label string, icon *ui.SVG) {
	if item.Like == youtube.Liked {
		return "Unlike", m3.IconFavorite
	}
	return "Like", m3.IconFavoriteBorder
}

func (a *app) canLike(item youtube.MusicItem) bool {
	return a.signedIn() && item.VideoID != ""
}

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
	a.rateTrack(item, after, func(err error) {
		if err == nil {
			return
		}
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
	if after == youtube.Disliked && id == a.current.VideoID && a.player.Active() {
		a.advance()
	}
}

func (a *app) rateTrack(item youtube.MusicItem, rating youtube.LikeStatus, done func(error)) {
	id := item.VideoID
	a.setLike(id, rating, false)
	a.writeBehind("like:"+id, func(ctx context.Context, client *youtube.Client) error {
		return client.RateTrack(ctx, id, rating)
	}, func(err error) {
		if err != nil {
			a.setLike(id, item.Like, false)
		}
		done(err)
	})
}

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

func (a *app) learnLikes(items []youtube.MusicItem) {
	for _, item := range items {
		if item.Like != youtube.LikeUnknown && !a.writing["like:"+item.VideoID] {
			a.setLike(item.VideoID, item.Like, true)
		}
	}
}

func (a *app) forgetAccountState() {
	clear(a.writing)
	a.page.library, a.page.subscription = youtube.LibraryState{}, youtube.Subscription{}
	a.eachItem(func(item *youtube.MusicItem) { item.Like, item.Library = youtube.LikeUnknown, nil })
}

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

func (a *app) canSave(library youtube.LibraryState) bool {
	return a.signedIn() && library.ID != ""
}

func libraryLabel(library youtube.LibraryState) (label string, icon *ui.SVG) {
	if library.Saved {
		return "Remove from library", m3.IconCheck
	}
	return "Save to library", m3.IconAdd
}

func named(title string) string {
	if title == "" {
		return "this album or playlist"
	}
	return "“" + title + "”"
}

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

func (a *app) canSubscribe(s youtube.Subscription) bool {
	return a.signedIn() && s.ChannelID != ""
}

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

func (a *app) setSubscribed(channelID string, subscribed bool) {
	if a.page.subscription.ChannelID == channelID {
		a.page.subscription.Subscribed = subscribed
	}
	a.pages.Clear()
}

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
