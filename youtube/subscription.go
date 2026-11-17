package youtube

import (
	"context"
	"errors"
	"strings"
)

type Subscription struct {
	ChannelID  string
	Subscribed bool
}

func (c *Client) Subscribe(ctx context.Context, channelID string) error {
	return c.writeSubscription(ctx, "Subscribe", "subscription/subscribe", channelID)
}

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
