package main

import (
	"context"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

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
	if a.writing == nil {
		a.writing = make(map[string]bool)
	}
	a.writing[key] = true
	follow := !s.Subscribed
	a.setSubscribed(s.ChannelID, follow)
	client := a.client()
	spawn(a, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), playlistTimeout)
		defer cancel()
		if follow {
			return client.Subscribe(ctx, s.ChannelID)
		}
		return client.Unsubscribe(ctx, s.ChannelID)
	}, func(err error) {
		if a.client() != client {
			return // another account is in use now: its pages say their own
		}
		delete(a.writing, key)
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
