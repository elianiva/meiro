package youtube

import (
	"context"
	"errors"
	"strings"
)

func (item MusicItem) InLibrary() LibraryState {
	if item.Library == nil {
		return LibraryState{}
	}
	return *item.Library
}

type LibraryState struct {
	ID    string
	Saved bool
}

func (c *Client) AddToLibrary(ctx context.Context, playlistID string) error {
	return c.writeLibrary(ctx, "AddToLibrary", playlistID, true)
}

func (c *Client) RemoveFromLibrary(ctx context.Context, playlistID string) error {
	return c.writeLibrary(ctx, "RemoveFromLibrary", playlistID, false)
}

func (c *Client) writeLibrary(ctx context.Context, name, playlistID string, save bool) error {
	if c.cookieAuth == nil {
		return errors.New("youtube: " + name + " requires cookie authentication")
	}
	playlistID = strings.TrimPrefix(strings.TrimSpace(playlistID), browsePlaylistPrefix)
	if playlistID == "" {
		return errors.New("youtube: " + name + " needs a playlist")
	}
	return c.rate(ctx, map[string]any{"playlistId": playlistID}, save)
}

func headerLibraryState(root any) LibraryState {
	object, ok := root.(*jsonObject)
	if !ok {
		return LibraryState{}
	}
	var walk func(any) (LibraryState, bool)
	walk = func(value any) (LibraryState, bool) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if state, ok := walk(child); ok {
					return state, true
				}
			}
		case *jsonObject:
			for _, key := range libraryToggles {
				if toggle, ok := node.get(key).(*jsonObject); ok {
					if state, ok := libraryToggle(toggle); ok {
						return state, true
					}
				}
			}
			for _, member := range node.members {
				if state, ok := walk(member.value); ok {
					return state, true
				}
			}
		}
		return LibraryState{}, false
	}
	state, _ := walk(object.get("header"))
	return state
}

func itemLibraryState(renderer *jsonObject) *LibraryState {
	menu, _ := renderer.get("menu").(*jsonObject)
	menuRenderer, _ := menu.get("menuRenderer").(*jsonObject)
	for _, list := range []string{"topLevelButtons", "items"} {
		entries, _ := menuRenderer.get(list).([]any)
		for _, entry := range entries {
			object, _ := entry.(*jsonObject)
			for _, key := range libraryToggles {
				if toggle, ok := object.get(key).(*jsonObject); ok {
					if state, ok := libraryToggle(toggle); ok {
						return &state
					}
				}
			}
		}
	}
	return nil
}

var libraryToggles = [...]string{"toggleButtonRenderer", "toggleMenuServiceItemRenderer"}

func libraryToggle(toggle *jsonObject) (LibraryState, bool) {
	endpoint := func(name string) (id, status string) {
		like, _ := toggle.get(name).(*jsonObject)
		like, _ = like.get("likeEndpoint").(*jsonObject)
		target, _ := like.get("target").(*jsonObject)
		id, _ = target.get("playlistId").(string)
		status, _ = like.get("status").(string)
		return id, status
	}
	id, status := endpoint("defaultServiceEndpoint")
	if id == "" {
		id, _ = endpoint("toggledServiceEndpoint")
	}
	if id == "" {
		return LibraryState{}, false
	}
	saved := status == "INDIFFERENT"
	if toggle.has("isToggled") {
		saved = rendererBool(toggle.get("isToggled"))
	}
	return LibraryState{ID: strings.TrimPrefix(id, browsePlaylistPrefix), Saved: saved}, true
}
