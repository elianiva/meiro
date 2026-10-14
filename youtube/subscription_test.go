package youtube

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestSubscriptionWritesNameTheChannel(t *testing.T) {
	for _, test := range []struct {
		follow bool
		path   string
	}{
		{true, "/youtubei/v1/subscription/subscribe"},
		{false, "/youtubei/v1/subscription/unsubscribe"},
	} {
		client, path, payload := writeClient(t, http.StatusOK, `{"responseContext":{}}`)
		var err error
		if test.follow {
			err = client.Subscribe(context.Background(), " UC1 ")
		} else {
			err = client.Unsubscribe(context.Background(), "UC1")
		}
		if err != nil {
			t.Fatal(err)
		}
		channels, _ := (*payload)["channelIds"].([]any)
		if *path != test.path || len(channels) != 1 || channels[0] != "UC1" {
			t.Errorf("follow=%v: path %q, channels %#v", test.follow, *path, channels)
		}
	}
}

func TestSubscriptionWriteRefusals(t *testing.T) {
	if err := NewClient(Options{}).Subscribe(context.Background(), "UC1"); err == nil {
		t.Error("a signed-out subscribe was sent")
	}
	client, path, _ := writeClient(t, http.StatusOK, `{}`)
	if err := client.Unsubscribe(context.Background(), " "); err == nil || *path != "" {
		t.Errorf("an unsubscribe without an artist was sent to %q: %v", *path, err)
	}
	client, _, _ = writeClient(t, http.StatusBadRequest, `{"error":{"code":400}}`)
	if err := client.Subscribe(context.Background(), "UC1"); err == nil {
		t.Error("a 400 was not an error")
	}
}

func TestArtistPagesSaySubscribed(t *testing.T) {
	page := func(button string) string {
		return `{"header":{"musicImmersiveHeaderRenderer":{"title":{"runs":[{"text":"A"}]},` + button + `}}}`
	}
	for _, test := range []struct {
		name, page string
		want       Subscription
	}{
		{"followed", page(`"subscriptionButton":{"subscribeButtonRenderer":{"subscribed":true,"channelId":"UC1"}}`), Subscription{"UC1", true}},
		{"not followed", page(`"subscriptionButton":{"subscribeButtonRenderer":{"subscribed":false,"channelId":"UC1"}}`), Subscription{"UC1", false}},
		{"no channel to write", page(`"subscriptionButton":{"subscribeButtonRenderer":{"subscribed":true}}`), Subscription{}},
		{"no button", page(`"x":1`), Subscription{}},
		{"not in the header", `{"contents":{"subscribeButtonRenderer":{"subscribed":true,"channelId":"UC1"}}}`, Subscription{}},
	} {
		if got := browseResult(decodeResponse(json.RawMessage(test.page))).Subscription; got != test.want {
			t.Errorf("%s: subscription %+v, want %+v", test.name, got, test.want)
		}
	}
}
