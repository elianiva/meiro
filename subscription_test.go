package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/youtube"
)

// subscriptionWrite is the last write the fake YouTube was asked for to a
// subscription: its endpoint, and the channels it names.
func subscriptionWrite() (endpoint string, channels []any) {
	write := written()
	channels, _ = write.body["channelIds"].([]any)
	return write.endpoint[strings.LastIndex(write.endpoint, "/subscription/"):], channels
}

func TestSubscribingToAnArtistAndUnsubscribing(t *testing.T) {
	resetFakes()
	a := signedInApp(t)
	a.router.Push("/artist/UCartist")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	if !slices.Contains(tt.Texts(), "Subscribe") || slices.Contains(tt.Texts(), "Subscribed") {
		t.Fatalf("an artist not followed should offer Subscribe: %q", tt.Texts())
	}
	if err := tt.Click("Subscribe"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if endpoint, channels := subscriptionWrite(); endpoint != "/subscription/subscribe" || !slices.Equal(channels, []any{"UCartist"}) || subscribeWrites.Load() != 1 {
		t.Fatalf("subscribing wrote %s for %v (%d writes)", endpoint, channels, subscribeWrites.Load())
	}
	if !slices.Contains(tt.Texts(), "Subscribed") || !tt.HasText("Subscribed to Aurora Vale") {
		t.Fatalf("the page did not show the artist followed: %q", tt.Texts())
	}

	// The library is read again, and lists the artist.
	libraryLoads.Store(0)
	a.router.Push("/library")
	tt.Frame()
	if libraryLoads.Load() != 1 || !tt.HasText("Aurora Vale Trio") {
		t.Fatalf("the library was read %d times, and shows: %q", libraryLoads.Load(), tt.Texts())
	}

	// The artist page says what YouTube now says, and unsubscribes.
	a.router.Push("/artist/UCartist")
	tt.Frame()
	if a.page.subscription != (youtube.Subscription{ChannelID: "UCartist", Subscribed: true}) || !slices.Contains(tt.Texts(), "Subscribed") {
		t.Fatalf("the artist page did not read its state back: %+v %q", a.page.subscription, tt.Texts())
	}
	if err := tt.Click("Subscribed"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if endpoint, channels := subscriptionWrite(); endpoint != "/subscription/unsubscribe" || !slices.Equal(channels, []any{"UCartist"}) || subscribeWrites.Load() != 2 {
		t.Fatalf("unsubscribing wrote %s for %v (%d writes)", endpoint, channels, subscribeWrites.Load())
	}
	if !slices.Contains(tt.Texts(), "Subscribe") || fakeSaved(fakeArtist) || !tt.HasText("Unsubscribed from Aurora Vale") {
		t.Fatalf("the artist was not unfollowed: %q", tt.Texts())
	}
}

func TestARejectedSubscriptionIsTakenBack(t *testing.T) {
	resetFakes()
	refuseWrites.Store(true)
	t.Cleanup(resetFakes)
	a := signedInApp(t)
	a.router.Push("/artist/UCartist")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	var held []func()
	run := a.run
	a.run = func(work func()) { held = append(held, work) }
	if err := tt.Click("Subscribe"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !slices.Contains(tt.Texts(), "Subscribed") {
		t.Fatalf("the button did not flip before YouTube answered: %q", tt.Texts())
	}
	if err := tt.Click("Subscribed"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(held) != 1 {
		t.Fatalf("a press while the write was out wrote again: %d held", len(held))
	}
	a.run = run
	for _, work := range held {
		work()
	}
	tt.Frame()
	if !slices.Contains(tt.Texts(), "Subscribe") || a.page.subscription.Subscribed {
		t.Fatalf("the button stayed flipped after a rejection: %q", tt.Texts())
	}
	if !tt.HasText("Could not subscribe to Aurora Vale") {
		t.Fatalf("the failure was not announced: %q", tt.Texts())
	}
	if fakeSaved(fakeArtist) {
		t.Error("a refused write changed the subscriptions")
	}
}

func TestSignedOutHasNoSubscribeButton(t *testing.T) {
	resetFakes()
	a := newTestApp()
	a.router.Push("/artist/UCartist")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if slices.Contains(tt.Texts(), "Subscribe") || slices.Contains(tt.Texts(), "Subscribed") || !tt.HasText("Artist Track") {
		t.Fatalf("a signed-out artist page offers to subscribe, or did not load: %q", tt.Texts())
	}
	a.page.subscription = youtube.Subscription{ChannelID: "UCartist"}
	a.toggleSubscription() // nothing may reach YouTube
	if subscribeWrites.Load() != 0 {
		t.Fatalf("a signed-out subscribe wrote %d times", subscribeWrites.Load())
	}
}
