package main

import (
	"time"

	"github.com/elianiva/meiro/youtube"
)

// hoverResolveDelay is how long the pointer rests on a song before its audio
// URL is looked up, so sweeping across a list starts nothing.
const hoverResolveDelay = 400 * time.Millisecond

// hoverSong notes whether the pointer is on a song, drawn under key. A song
// the pointer rests on has its audio URL resolved ahead, so a click plays
// without waiting on yt-dlp. Only a few kilobytes are fetched; nothing is
// downloaded. Each draw of a row reports its own state, so the row the
// pointer left cancels its own pending lookup.
func (a *app) hoverSong(key string, item youtube.MusicItem, hovered bool) {
	if !hovered {
		if a.hoverKey == key {
			a.cancelHover()
		}
		return
	}
	if a.hoverKey == key || item.VideoID == "" {
		return
	}
	a.cancelHover()
	a.hoverKey = key
	id := item.VideoID
	a.hoverStop = a.schedule(hoverResolveDelay, func() {
		a.update(func() { a.resolveHovered(key, id) })
	})
}

func (a *app) cancelHover() {
	if a.hoverStop != nil {
		a.hoverStop()
	}
	a.hoverKey, a.hoverStop = "", nil
}

// resolveHovered looks up the audio of the song under the pointer, unless
// that is pointless or another lookup is still running.
func (a *app) resolveHovered(key, videoID string) {
	if a.hoverKey != key || a.hoverBusy || a.resolving || videoID == a.current.VideoID {
		return
	}
	if _, cached := a.currentAudioCache().get(videoID); cached {
		return
	}
	a.hoverBusy = true
	cookie := a.ytDlpCookie
	a.run(func() {
		_, _, _ = a.streams.get(videoID, cookie)
		a.update(func() { a.hoverBusy = false })
	})
}
