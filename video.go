package main

import (
	"context"
	"image"
	"log"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/player"
	"github.com/elianiva/meiro/youtube"
)

// videoState is the picture of the track playing, shown in the full-screen
// player in place of the artwork when the user asks for it.
type videoState struct {
	// load finds the picture's stream and gives it to the player.
	load task
	// streams remembers resolved picture URLs, as playback.streams does the
	// audio's.
	streams *audio.URLs
	// id is the track whose picture was last asked for, and err why it
	// could not be shown; a track is not asked for again until it changes.
	id  string
	err string
	// bitmap holds the frame drawn, once a frame of the track id has come;
	// hasFrame says one has.
	bitmap   *ui.Bitmap
	hasFrame bool
}

// hasVideo reports whether item has a picture to show: a music video, or an
// episode, which may be one.
func hasVideo(item youtube.MusicItem) bool {
	return item.IsVideo() || item.IsEpisode()
}

// wantVideo reports whether the picture of the track playing should decode:
// the user asked for it, the track has one, and the player that shows it is
// open. A closed player shows nothing, so decoding for it would be waste.
func (a *app) wantVideo() bool {
	return a.settings.ShowVideo && a.nowPlaying.open && hasVideo(a.current)
}

// setShowVideo chooses between the artwork and the picture, and keeps the
// choice.
func (a *app) setShowVideo(on bool) {
	if a.settings.ShowVideo == on {
		return
	}
	a.settings.ShowVideo = on
	a.saveSettings()
	a.syncVideo()
}

// syncVideo brings the player's picture in line with wantVideo: it starts
// the picture of the track playing when it is wanted and not yet shown, and
// stops it when it is not. Opening the player, choosing the picture, a track
// starting and each poll call it, so the picture follows them all.
func (a *app) syncVideo() {
	if !a.wantVideo() {
		a.video.load.stop()
		a.video.id, a.video.err, a.video.hasFrame = "", "", false
		if a.player.VideoURL() != "" {
			a.player.StopVideo()
		}
		return
	}
	id := a.current.VideoID
	if a.video.id == id && (a.video.load.busy() || a.video.err != "" || a.player.VideoURL() != "") {
		return
	}
	if a.video.id != id {
		a.video.id, a.video.err, a.video.hasFrame = id, "", false
	}
	streams := a.video.streams
	startTask(a, &a.video.load, audio.ResolveTimeout, func(ctx context.Context) (string, error) {
		url, _, err := streams.Get(ctx, id)
		if err != nil {
			return "", err
		}
		// The player takes the picture only once a stream plays; before
		// that this is a no-op, and the next poll asks again, with the
		// URL now remembered.
		return url, a.player.SetVideo(url)
	}, func(url string, err error) {
		if err != nil {
			log.Printf("playback: video unavailable video_id=%s: %v", id, err)
			a.video.err = "The video isn't available."
			return
		}
		log.Printf("playback: video started video_id=%s", id)
	})
}

// startVideoPump wakes the main thread at the picture's frame rate while a
// picture decodes, so pumpVideo can draw each frame as the sound reaches it.
func (a *app) startVideoPump() {
	a.every(time.Second/player.VideoFPS, func() {
		if a.player.VideoURL() != "" {
			a.update(a.pumpVideo)
		}
	})
}

// pumpVideo draws the frame due at the sound's position, if a new one is. It
// runs on the main thread.
func (a *app) pumpVideo() {
	a.syncVideo() // the player may have closed since the last frame
	frame, changed := a.player.Frame()
	if !changed {
		if a.video.err == "" {
			if failure := a.player.VideoFailure(); failure != "" {
				log.Printf("playback: video stopped video_id=%s: %s", a.video.id, failure)
				a.video.err = "The video stopped playing."
			}
		}
		return
	}
	a.updateVideoFrame(frame)
}

// updateVideoFrame shows frame as the picture.
func (a *app) updateVideoFrame(frame *image.RGBA) {
	if a.video.bitmap == nil {
		a.video.bitmap = ui.NewBitmap(frame)
	} else {
		a.video.bitmap.Update(frame)
	}
	a.video.hasFrame = true
}

// videoSize is the size the picture shows at: as tall as the artwork would
// be, and as wide as that makes it, within the width the player has to
// spare.
func videoSize(artSize, available float32) (width, height float32) {
	width = min(artSize*16/9, max(available, 200))
	return width, width * 9 / 16
}

// nowPlayingVideo shows the picture of the track playing where the artwork
// goes, with the track menu over it. Until the first frame comes it shows
// the artwork with a loading indicator, and when the picture cannot be shown,
// why not.
func (a *app) nowPlayingVideo(c *ui.Context, width, height, radius float32) {
	sc := m3.Of(c).Scheme
	key := itemKey("now-playing-menu", a.current)
	var menuButton ui.Element
	box := ui.Box(c).Size(width, height).Radius(radius).Background(ui.RGB(0, 0, 0)).Clip().Shrink(0)
	box.Children(func() {
		switch {
		case a.video.hasFrame:
			ui.Image(c, a.video.bitmap).Fit(ui.Contain).Fill()
		default:
			art := a.thumbs.Bitmap(a.current.Thumbnail, 640)
			if art != nil {
				ui.Image(c, art).Fit(ui.Cover).Fill().Opacity(0.4)
			}
			ui.Box(c).Fill().Center().Children(func() {
				if a.video.err != "" {
					ui.Box(c).PaddingX(16).PaddingY(8).Radius(m3.Full).Background(sc.Scrim.Alpha(0.55)).Children(func() {
						m3.BodyMedium.Style(ui.Text(c, a.video.err), false).TextColor(ui.RGB(255, 255, 255))
					})
				} else {
					m3.LoadingIndicatorIn(c, 48, ui.RGB(255, 255, 255))
				}
			})
		}
		menuButton = m3.IconButton(c, m3.IconButtonSpec{
			Icon: m3.IconMore, Label: "More options for " + a.current.Title, Key: key + "-button",
			Container: sc.SurfaceContainerHighest.Alpha(0.85),
		}).Attach(ui.AnchorTopRight, ui.AnchorTopRight).Top(8).Right(8)
	})
	m3.Elevation(c, box, 3)
	if menuButton.Clicked() {
		a.menu.show(key, false, 0, 0)
	}
	if a.menu.owns(key) {
		a.songMenu(c, menuButton, key, a.current, songOptions{})
	}
}
