package youtube

import (
	"context"
	"errors"
	"strings"
)

// Subscription is whether the signed-in account follows an artist, and the
// channel the write names. ChannelID is empty for what the page offers no
// button for: an artist seen signed out, or a page that is not an artist's.
type Subscription struct {
	// ChannelID is the channel of the artist. It is read from the page's
	// button: the browse ID an artist page is opened with can differ from it.
	ChannelID  string
	Subscribed bool
}

// Subscribe follows an artist, with subscription/subscribe and the artist's
// channel, as YouTube Music's own artist page does.
func (c *Client) Subscribe(ctx context.Context, channelID string) error {
	return c.writeSubscription(ctx, "Subscribe", "subscription/subscribe", channelID)
}

// Unsubscribe stops following an artist.
func (c *Client) Unsubscribe(ctx context.Context, channelID string) error {
	return c.writeSubscription(ctx, "Unsubscribe", "subscription/unsubscribe", channelID)
}

func (c *Client) writeSubscription(ctx context.Context, name, endpoint, channelID string) error {
	if c.cookieAuth == nil {
		return errors.New("youtube: " + name + " requires cookie authentication")
	}
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return errors.New("youtube: " + name + " needs an artist")
	}
	raw, err := c.execute(ctx, endpoint, map[string]any{"channelIds": []string{channelID}})
	if err != nil {
		return err
	}
	return writeFailure(endpoint, raw)
}

// headerSubscription reads the subscribe button of an artist's header. Like the
// save button of an album, it is searched for in the header rather than
// followed down a path, since the header renderer differs by page.
func headerSubscription(root any) Subscription {
	object, ok := root.(*jsonObject)
	if !ok {
		return Subscription{}
	}
	var walk func(any) (Subscription, bool)
	walk = func(value any) (Subscription, bool) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if found, ok := walk(child); ok {
					return found, true
				}
			}
		case *jsonObject:
			if button, ok := node.get("subscribeButtonRenderer").(*jsonObject); ok {
				if id, _ := button.get("channelId").(string); id != "" {
					return Subscription{ChannelID: id, Subscribed: rendererBool(button.get("subscribed"))}, true
				}
			}
			for _, member := range node.members {
				if found, ok := walk(member.value); ok {
					return found, true
				}
			}
		}
		return Subscription{}, false
	}
	found, _ := walk(object.get("header"))
	return found
}
