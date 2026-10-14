package youtube

import (
	"context"
	"errors"
	"strings"
)

// InLibrary is what the item's menu said of the album or the playlist, or the
// zero state when it said nothing.
func (item MusicItem) InLibrary() LibraryState {
	if item.Library == nil {
		return LibraryState{}
	}
	return *item.Library
}

// LibraryState is whether an album or a playlist is in the account's library,
// and what to write to change it. ID is empty for what the page offers no way
// to save: a playlist the account made, a mix, or anything seen signed out.
type LibraryState struct {
	// ID is the playlist the write names. For an album it is the album's audio
	// playlist (OLAK5uy_…), not its browse ID, which is why it is read from
	// the page rather than derived.
	ID    string
	Saved bool
}

// AddToLibrary saves an album or a playlist to the account's library. YouTube
// Music keeps a saved playlist as a like of it, so this is the call that
// likes a track, aimed at a playlist: like/like with the playlist as the
// target.
func (c *Client) AddToLibrary(ctx context.Context, playlistID string) error {
	return c.writeLibrary(ctx, "AddToLibrary", playlistID, true)
}

// RemoveFromLibrary takes an album or a playlist out of the account's
// library, with like/removelike.
func (c *Client) RemoveFromLibrary(ctx context.Context, playlistID string) error {
	return c.writeLibrary(ctx, "RemoveFromLibrary", playlistID, false)
}

func (c *Client) writeLibrary(ctx context.Context, name, playlistID string, save bool) error {
	if c.cookieAuth == nil {
		return errors.New("youtube: " + name + " requires cookie authentication")
	}
	// A browse ID carries the "VL" a write does not take.
	playlistID = strings.TrimPrefix(strings.TrimSpace(playlistID), "VL")
	if playlistID == "" {
		return errors.New("youtube: " + name + " needs a playlist")
	}
	return c.rate(ctx, map[string]any{"playlistId": playlistID}, save)
}

// headerLibraryState reads the save button of an album's or a playlist's
// header: the first toggle in it that likes a playlist. The renderers that
// hold it differ by page and by the version of the page, which is why the
// header is searched for the toggle rather than the toggle's path followed.
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

// itemLibraryState reads the save toggle of an item's menu, which an album or
// a playlist card carries among its menu items. It is nil for an item with
// none, which is nearly all of them.
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

// libraryToggles are the renderers that hold a toggle: a button of a header,
// and an entry of a menu.
var libraryToggles = [...]string{"toggleButtonRenderer", "toggleMenuServiceItemRenderer"}

// libraryToggle reads a toggle that likes a playlist: the playlist it names,
// and whether it says the playlist is saved. A toggle that likes anything else,
// as a song's does, is not one.
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
	// A toggle button says whether it is toggled. A menu item does not: its
	// default endpoint is the write the item offers now, so one that would
	// take the like back is one already given.
	saved := status == "INDIFFERENT"
	if toggle.has("isToggled") {
		saved = rendererBool(toggle.get("isToggled"))
	}
	return LibraryState{ID: strings.TrimPrefix(id, "VL"), Saved: saved}, true
}
