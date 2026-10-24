package main

import (
	"log"
	"time"

	"github.com/elianiva/meiro/internal/discordrpc"
	"github.com/elianiva/meiro/systemmedia"
	"github.com/elianiva/meiro/youtube"
)

// Discord Rich Presence uses Meiro's public Discord application ID.
const discordPresenceApplicationID = "1561435721456296087"

// systemMediaControls maps operating-system commands to the app's playback
// actions. Platform callbacks arrive on other goroutines, so each goes
// through update before it touches the app.
func (a *app) systemMediaControls() systemmedia.Controls {
	on := func(fn func()) func() { return func() { a.update(fn) } }
	return systemmedia.Controls{
		Play:     on(a.resume),
		Pause:    on(a.pause),
		Toggle:   on(a.togglePlay),
		Stop:     on(a.stopAtQueueEnd),
		Next:     on(a.advance),
		Previous: on(a.previous),
		Seek: func(position time.Duration) {
			a.update(func() { a.seek(position) })
		},
		SetVolume: func(volume float64) {
			a.update(func() {
				a.setVolume(volume * 100)
				a.saveSettings()
			})
		},
		SetShuffle: func(shuffle bool) {
			a.update(func() { a.setShuffle(shuffle) })
		},
		SetLoop: func(loop systemmedia.Loop) {
			a.update(func() {
				a.repeat = repeatFromLoop(loop)
				a.syncSystemMedia()
			})
		},
	}
}

// repeatLoops pairs each repeat mode with the loop status the system shows.
var repeatLoops = [...]systemmedia.Loop{
	repeatOff:   systemmedia.LoopNone,
	repeatQueue: systemmedia.LoopPlaylist,
	repeatTrack: systemmedia.LoopTrack,
}

func repeatFromLoop(loop systemmedia.Loop) repeatMode {
	for mode, l := range repeatLoops {
		if l == loop {
			return repeatMode(mode)
		}
	}
	return repeatOff
}

// syncSystemMedia publishes a snapshot of playback to the system's media
// controls, and lines the sleep assertion and the tray up with it. Every
// path that changes playback calls it.
func (a *app) syncSystemMedia() {
	a.syncDesktop()
	a.syncDiscordPresence()
	if a.media == nil {
		return
	}
	status := systemmedia.Stopped
	switch {
	case a.player.Playing():
		status = systemmedia.Playing
	case a.player.Paused():
		status = systemmedia.Paused
	}
	position := time.Duration(0)
	if a.player.Active() {
		position = a.player.Position()
	}
	hasTrack := a.current.VideoID != ""
	canNext := false
	if hasTrack {
		_, canNext = a.queue.next(a.settings.AutoPlay, a.repeat)
		if !canNext && a.settings.AutoPlay {
			canNext = a.upNext.load.busy() || !a.upNext.fetched || a.upNext.token != ""
		}
	}
	if err := a.media.Update(systemmedia.State{
		VideoID:     a.current.VideoID,
		Title:       a.current.Title,
		Artist:      a.current.Subtitle,
		ArtworkURL:  a.current.Thumbnail,
		Duration:    a.total,
		Position:    position,
		SeekVersion: a.seekVersion,
		Status:      status,
		Loop:        repeatLoops[a.repeat],
		Volume:      a.volume / 100,
		CanPlay:     hasTrack,
		CanPause:    a.player.Playing(),
		CanNext:     canNext,
		CanPrevious: hasTrack,
		CanSeek:     a.total > 0 && a.player.Active(),
		Shuffle:     a.shuffle,
	}); err != nil {
		log.Printf("system media controls unavailable: %v", err)
		if closeErr := a.media.Close(); closeErr != nil {
			log.Printf("close system media controls: %v", closeErr)
		}
		a.media = nil
	}
}

// syncDiscordPresence publishes only while playback is audible and the user
// has enabled sharing. Discord IPC runs on its own worker, not the UI thread.
func (a *app) syncDiscordPresence() {
	if !a.settings.DiscordPresence {
		a.closeDiscordPresence()
		return
	}
	id, err := discordrpc.ParseApplicationID(discordPresenceApplicationID)
	if err != nil {
		a.closeDiscordPresence()
		return
	}
	if a.desktop == nil {
		return
	}
	if !a.player.Playing() || a.current.VideoID == "" {
		if a.discord != nil {
			a.discord.Update(discordrpc.State{})
		}
		return
	}
	if a.discord == nil {
		a.discord = discordrpc.New(id)
	}
	a.discord.Update(discordPresenceState(a.current, a.player.Position(), a.total, a.seekVersion))
}

// shownArtist is the artist line for a track: the credited artists, else the
// one credit an older saved queue kept, else the byline the player shows for a
// row that links none.
func shownArtist(item youtube.MusicItem) string {
	if item.Artists != "" {
		return item.Artists
	}
	if item.Artist != "" {
		return item.Artist
	}
	if item.Kind != youtube.KindTrack {
		return ""
	}
	return item.Subtitle
}

// discordPresenceState selects the fields from the current track that belong
// in Discord's compact activity.
func discordPresenceState(item youtube.MusicItem, position, total time.Duration, seekVersion uint64) discordrpc.State {
	return discordrpc.State{
		VideoID:     item.VideoID,
		Title:       item.Title,
		Artist:      shownArtist(item),
		ArtworkURL:  item.Thumbnail,
		Position:    position,
		Duration:    total,
		SeekVersion: seekVersion,
		Playing:     true,
	}
}

// closeDiscordPresence stops the old IPC worker away from the UI thread.
func (a *app) closeDiscordPresence() {
	if a.discord == nil {
		return
	}
	session := a.discord
	a.discord = nil
	a.run(session.Close)
}
