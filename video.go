package main

import (
	"context"
	"errors"
	"image"
	"log"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// videoState is the picture of the track playing, shown in the full-screen
// player in place of the artwork when the user asks for it.
type videoState struct {
	// load finds the picture's stream and gives it to the player.
	load task
	// id is the track whose picture was last asked for, and err why it
	// could not be shown; a track is not asked for again until it changes.
	// fullScreen says the picture asked for was the full-screen one.
	id         string
	err        string
	fullScreen bool
	// bitmap holds the frame drawn, once a frame of the track id has come;
	// hasFrame says one has.
	bitmap   *ui.Bitmap
	hasFrame bool
	// buffering says the picture fell behind the sound, which is held until
	// the picture catches up and then plays on.
	buffering bool
	// flash is the icon that swells and fades over the picture after a
	// click on it plays or pauses the track; flashNew says it has yet to
	// start.
	flash    *ui.SVG
	flashNew bool
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

// The picture's frames are as tall as its stream: 720p in the player, and
// 1080p over a screen at least that tall.
const (
	videoHeight           = 720
	fullScreenVideoHeight = 1080
)

// syncVideo brings the player's picture in line with wantVideo: it starts
// the picture of the track playing when it is wanted and not yet shown, and
// stops it when it is not. Full screen asks for the sharper picture, and
// leaving it for the one the player shows; the player switches between them
// without a gap. Opening the player, choosing the picture, a track
// starting, filling the screen and each poll call it, so the picture
// follows them all.
func (a *app) syncVideo() {
	if !a.wantVideo() {
		a.setFullscreen(false)
		a.video.load.stop()
		a.video.id, a.video.err, a.video.hasFrame = "", "", false
		if a.player.VideoURL() != "" {
			a.player.StopVideo()
		}
		if a.video.buffering {
			a.video.buffering = false // nothing to wait for
			a.player.Resume()
		}
		return
	}
	id, fullScreen := a.current.VideoID, a.nowPlaying.fullscreen
	if a.video.id == id && (a.video.err != "" ||
		a.video.fullScreen == fullScreen && (a.video.load.busy() || a.player.VideoURL() != "")) {
		return
	}
	if a.video.id != id {
		// A new track plays as it started, whatever the last one waited for.
		a.video.id, a.video.err, a.video.hasFrame, a.video.buffering = id, "", false, false
	}
	a.video.fullScreen = fullScreen
	// The picture's URL came with the audio's, from the one yt-dlp run that
	// found it; a track whose audio came from the cache resolves now.
	streams := a.streams
	// A screen too short for the sharper picture shows the player's,
	// stretched, rather than decode pixels it cannot show.
	sharper := fullScreen
	if screen := a.screenHeight(); screen > 0 && screen < fullScreenVideoHeight {
		sharper = false
	}
	startTask(a, &a.video.load, audio.ResolveTimeout, func(ctx context.Context) (string, error) {
		stream, err := streams.Get(ctx, id)
		if err != nil {
			return "", err
		}
		if stream.Video == "" {
			return "", errors.New("the track has no picture")
		}
		url, height, fps := stream.Video, videoHeight, stream.VideoFPS
		if sharper && stream.FullScreenVideo != "" {
			url, height, fps = stream.FullScreenVideo, fullScreenVideoHeight, stream.FullScreenVideoFPS
		}
		// The player takes the picture only once a stream plays; before
		// that this is a no-op, and the next poll asks again, with the
		// URL now remembered.
		return url, a.player.SetVideo(ctx, url, height, fps)
	}, func(url string, err error) {
		if err != nil {
			log.Printf("playback: video unavailable video_id=%s: %v", id, err)
			a.video.err = "The video isn't available."
			return
		}
		log.Printf("playback: video started video_id=%s full_screen=%t", id, fullScreen)
	})
}

// maxVideoFPS is the highest frame rate YouTube serves.
const maxVideoFPS = 60

// startVideoPump wakes the main thread at the picture's frame rate while a
// picture decodes, so pumpVideo can draw each frame as the sound reaches it.
// It ticks at maxVideoFPS and passes on the ticks that keep to the
// picture's own rate, so a 24 fps picture redraws the window 24 times a
// second, not 60.
func (a *app) startVideoPump() {
	var next time.Time
	a.every(time.Second/maxVideoFPS, func() {
		fps := a.player.VideoFPS()
		if fps <= 0 {
			return
		}
		// A tick may come a little before its time; at 60 fps it would
		// otherwise miss the frame it is for.
		now := time.Now()
		if now.Add(4 * time.Millisecond).Before(next) {
			return
		}
		next = next.Add(time.Duration(float64(time.Second) / fps))
		if next.Before(now) {
			// Behind by more than a frame, or just started: keep time from
			// now, not from ticks that are gone.
			next = now.Add(time.Duration(float64(time.Second) / fps))
		}
		a.update(a.pumpVideo)
	})
}

// videoStall is how far a playing picture may fall behind the sound before
// the sound waits for it.
const videoStall = 300 * time.Millisecond

// pumpVideo draws the frame due at the sound's position, if a new one is,
// and holds the sound while the picture has fallen behind it, as when the
// network stalls. A picture yet to give its first frame, as after a seek,
// holds the sound at once, even while the sound is still opening, so the
// two wait together rather than one after the other. It runs on the main
// thread.
func (a *app) pumpVideo() {
	a.syncVideo() // the player may have closed since the last frame
	switch lag := a.player.VideoLag(); {
	case !a.video.buffering && a.player.Playing() && (lag > videoStall || a.player.VideoStarting()):
		log.Printf("playback: waiting for the video video_id=%s behind=%s", a.video.id, lag.Round(time.Millisecond))
		a.video.buffering = true
		a.player.Pause()
	case a.video.buffering && lag <= 0:
		a.video.buffering = false
		a.player.Resume()
	}
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

// setFullscreen puts the picture over the whole window, and the window over
// the whole screen, or puts both back.
func (a *app) setFullscreen(on bool) {
	if a.nowPlaying.fullscreen == on {
		return
	}
	a.nowPlaying.fullscreen = on
	a.desktop.setFullScreen(on)
	a.syncVideo()
}

// nowPlayingVideo shows the picture of the track playing where the artwork
// goes, with the track menu over it and the button that fills the screen
// with it. Until the first frame comes it shows the artwork with a loading
// indicator, and when the picture cannot be shown, why not.
func (a *app) nowPlayingVideo(c *ui.Context, width, height, radius float32) {
	sc := m3.Of(c).Scheme
	key := itemKey("now-playing-menu", a.current)
	var menuButton ui.Element
	box := ui.Box(c).Size(width, height).Radius(radius).Background(ui.RGB(0, 0, 0)).Clip().Shrink(0)
	box.Children(func() {
		a.videoPicture(c)
		menuButton = m3.IconButton(c, m3.IconButtonSpec{
			Icon: m3.IconMore, Label: "More options for " + a.current.Title, Key: key + "-button",
			Container: sc.SurfaceContainerHighest.Alpha(0.85),
		}).Attach(ui.AnchorTopRight, ui.AnchorTopRight).Top(8).Right(8)
		if m3.IconButton(c, m3.IconButtonSpec{
			Icon: m3.IconFullscreen, Label: "Full screen", Key: "np-fullscreen",
			Container: sc.SurfaceContainerHighest.Alpha(0.85),
		}).Attach(ui.AnchorBottomRight, ui.AnchorBottomRight).Bottom(8).Right(8).Clicked() {
			a.nowPlaying.pointerAt = c.Now() // the click counts as the pointer's last move
			a.setFullscreen(true)
		}
	})
	m3.Elevation(c, box, 3)
	if menuButton.Clicked() {
		a.menu.show(key, false, 0, 0)
	}
	if a.menu.owns(key) {
		a.songMenu(c, menuButton, key, a.current, songOptions{})
	}
}

// videoPicture fills its parent with the frame drawn, or, until one has
// come, with the artwork dimmed under a loading indicator or the reason the
// picture cannot be shown. A click on it plays or pauses the track, and a
// double click fills the screen with it or puts it back. It returns the
// picture.
func (a *app) videoPicture(c *ui.Context) ui.Element {
	picture := ui.Box(c.Key("video-picture")).Fill()
	// The first click of a double click has already played or paused the
	// track; the second undoes that, so a double click leaves it as it was.
	switch {
	case picture.DoubleClicked():
		a.togglePlay()
		a.nowPlaying.pointerAt = c.Now() // the click counts as the pointer's last move
		a.setFullscreen(!a.nowPlaying.fullscreen)
	case picture.Clicked():
		a.video.flash, a.video.flashNew = m3.IconPlay, true
		if a.playing() {
			a.video.flash = m3.IconPause
		}
		a.togglePlay()
	}
	return picture.Children(func() {
		a.videoFrame(c)
		a.videoFlash(c)
	})
}

// flashTime is how long the icon over a clicked picture takes to fade.
const flashTime = 500 * time.Millisecond

// videoFlash shows what a click on the picture did, as a play or pause icon
// in a dark circle that swells and fades from the middle of the picture.
func (a *app) videoFlash(c *ui.Context) {
	if a.video.flash == nil {
		return
	}
	flash := ui.Box(c.Key("video-flash")).Size(64, 64).Radius(m3.Full).Background(ui.RGB(0, 0, 0).Alpha(0.5)).
		Center().Attach(ui.AnchorCenter, ui.AnchorCenter).PassThrough()
	if a.video.flashNew {
		a.video.flashNew = false
		flash.Jump("opacity", 1)
		flash.Jump("scale", 1)
	}
	scale := flash.Animate("scale", 1.6, flashTime)
	flash.Opacity(flash.Animate("opacity", 0, flashTime)).Scale(scale, scale)
	flash.Children(func() {
		ui.Icon(c, a.video.flash).FontSize(36).TextColor(ui.RGB(255, 255, 255))
	})
}

// videoFrame is what videoPicture shows: the frame drawn, with a loading
// indicator over it on a dark disc while the track loads, the sound or the
// picture.
func (a *app) videoFrame(c *ui.Context) {
	if a.video.hasFrame {
		ui.Image(c, a.video.bitmap).Fit(ui.Contain).Fill()
		if a.loading() {
			ui.Box(c.Key("video-buffering")).Size(64, 64).Radius(m3.Full).Background(ui.RGB(0, 0, 0).Alpha(0.5)).
				Center().Attach(ui.AnchorCenter, ui.AnchorCenter).PassThrough().Children(func() {
				m3.LoadingIndicatorIn(c, 48, ui.RGB(255, 255, 255))
			})
		}
		return
	}
	art := a.thumbs.Bitmap(a.current.Thumbnail, 640)
	if art != nil {
		ui.Image(c, art).Fit(ui.Cover).Fill().Opacity(0.4)
	}
	ui.Box(c).Fill().Center().Children(func() {
		if a.video.err != "" {
			ui.Box(c).PaddingX(16).PaddingY(8).Radius(m3.Full).Background(ui.RGB(0, 0, 0).Alpha(0.55)).Children(func() {
				m3.BodyMedium.Style(ui.Text(c, a.video.err), false).TextColor(ui.RGB(255, 255, 255))
			})
		} else {
			m3.LoadingIndicatorIn(c, 48, ui.RGB(255, 255, 255))
		}
	})
}

// controlsLinger is how long the controls stay over the full-screen picture
// after the pointer rests.
const controlsLinger = 3 * time.Second

// fullscreenVideoView is the picture over the whole window, on black, with
// the track, the scrubber and the transport laid over it. They show while the
// pointer moves, and go after it has rested for controlsLinger, taking the
// pointer with them. It is drawn dark whatever the appearance, since the
// ground is black. It returns the element that takes the window.
func (a *app) fullscreenVideoView(c *ui.Context) ui.Element {
	shown := m3.Of(c)
	m3.Provide(c, a.darkTheme(c))
	defer m3.Provide(c, shown)
	w, _ := c.Size()
	rested := c.Now().Sub(a.nowPlaying.pointerAt)
	controls := rested < controlsLinger
	if controls {
		c.After(controlsLinger - rested)
	}
	root := ui.Box(c.Key("fullscreen-video")).Fill().Background(ui.RGB(0, 0, 0))
	root.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind != ui.InputPointerMove {
			return false
		}
		hidden := ev.Time.Sub(a.nowPlaying.pointerAt) >= controlsLinger
		a.nowPlaying.pointerAt = ev.Time
		// A frame follows a taken event, which brings the controls back; a
		// move while they show goes on to them.
		return hidden
	})
	root.Children(func() {
		picture := a.videoPicture(c)
		if !controls {
			picture.Cursor(ui.CursorNone)
			return
		}
		scrim := ui.RGB(0, 0, 0).Alpha(0.7)
		ui.Row(c).Absolute().Top(0).Left(0).Right(0).Padding(24, 32, 48).Gap(16).AlignItems(ui.Center).
			Gradient(scrim, ui.Transparent, 180).Children(func() {
			if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconFullscreenExit, Label: "Leave full screen", Key: "np-fullscreen-exit"}).Clicked() {
				a.setFullscreen(false)
			}
			a.trackLine(c, m3.TitleLarge, m3.BodyMedium).Grow(1)
		})
		ui.Column(c).Absolute().Bottom(0).Left(0).Right(0).Padding(48, 32, 24).AlignItems(ui.Center).
			Gradient(ui.Transparent, scrim, 180).Children(func() {
			ui.Column(c).Width(min(w, 960)).Gap(8).Children(func() {
				a.scrubber(c)
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Row(c).Grow(1).Basis(0).Children(func() { a.volumeControl(c, 120) })
					a.transport(c, 56, 8)
					ui.Row(c).Grow(1).Basis(0).Justify(ui.End).AlignItems(ui.Center).Gap(8).Children(func() {
						a.speedControl(c, "fullscreen-speed")
						a.likeButton(c)
					})
				})
			})
		})
	})
	return root
}
