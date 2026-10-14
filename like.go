package main

import (
	"context"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

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
	id := item.VideoID
	if !a.canLike(item) || a.rating[id] {
		return
	}
	before, after := item.Like, youtube.Liked
	if before == youtube.Liked {
		after = youtube.Indifferent
	}
	if a.rating == nil {
		a.rating = make(map[string]bool)
	}
	a.rating[id] = true
	a.setLike(id, after, false)
	client := a.client()
	spawn(a, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), playlistTimeout)
		defer cancel()
		return client.RateTrack(ctx, id, after == youtube.Liked)
	}, func(err error) {
		if a.client() != client {
			return // another account is in use now: its tracks say their own
		}
		delete(a.rating, id)
		if err == nil {
			return
		}
		a.setLike(id, before, false)
		verb := "like"
		if after != youtube.Liked {
			verb = "unlike"
		}
		a.notice = "Could not " + verb + " “" + item.Title + "”: " + firstLine(err.Error())
	})
}

// setLike gives every copy of a track the app holds the status: the track
// playing, the queue, the page shown and the related songs. A kept page is
// dropped from the cache rather than edited, as it will be read again from
// YouTube. With unknownOnly, only copies that do not know their status take
// it, which is how a status YouTube sent is learned.
func (a *app) setLike(videoID string, status youtube.LikeStatus, unknownOnly bool) {
	set := func(item *youtube.MusicItem) {
		if item.VideoID == videoID && (!unknownOnly || item.Like == "") {
			item.Like = status
		}
	}
	set(&a.current)
	a.queue.each(set)
	each := func(items []youtube.MusicItem) {
		for i := range items {
			set(&items[i])
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
	if !unknownOnly {
		a.pages.Clear()
	}
}

// learnLikes takes the statuses YouTube sent with a list of tracks for the
// copies of them the app holds without one.
func (a *app) learnLikes(items []youtube.MusicItem) {
	for _, item := range items {
		if item.Like != "" && !a.rating[item.VideoID] {
			a.setLike(item.VideoID, item.Like, true)
		}
	}
}

// forgetLikes drops what the tracks held say about the account that has just
// gone: another account has its own.
func (a *app) forgetLikes() {
	clear(a.rating)
	forget := func(item *youtube.MusicItem) { item.Like = "" }
	forget(&a.current)
	a.queue.each(forget)
	for i := range a.nowPlaying.related.items {
		forget(&a.nowPlaying.related.items[i])
	}
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
