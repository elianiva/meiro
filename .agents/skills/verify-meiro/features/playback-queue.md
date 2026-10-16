# Playback and queue

A user plays a track, controls the player bar, edits the queue, and finds the same queue after a restart. A failing track says why and the queue moves on.

## Sub-features

- `play-click` plays the clicked track and fills the player bar.
- `play-queue` adds, removes, reorders and clears queue items from the panel and menus.
- `play-modes` covers repeat (off, queue, track) and shuffle.
- `play-seek` seeks, and hands over at the end of a track.
- `play-fail` shows a readable reason, skips up to `maxSkips`, and retries on Play.
- `play-autoplay` queues recommendations when the queue runs out.
- `play-resume` restores the queue paused after a restart.
- `play-mini` shows and controls the mini player.
- `play-real-audio` decodes real audio through ffmpeg on the null device.

## How to get to it (user POV)

- Click a track row. Click `Play` or `Shuffle` on an album, playlist or artist page.
- Use the player bar buttons `Shuffle`, `Previous`, `Next`, `Repeat`, `Mute`, and the `Position` and `Volume` sliders.
- Click `Up next` in the player bar, or `Open the player`, to open the player panel with `Up next`, `Lyrics` and `Related`.
- Right click a track for `Play next` and `Add to queue`.
- Press `Space`, `Cmd+Shift+L`, `Cmd+Shift+J`, `Cmd+Shift+R`, `Cmd+Shift+S`.

## Driving it with the headless tester

Preconditions:

- Use `a, r := playbackApp(t)`. It sets `AutoPlay` off and resolves streams through `r`, so a click needs no yt-dlp. Set `r.file` to a tone for real audio, as `TestPlayingATrackThroughToTheEnd` does with `ffmpeg -f lavfi -i sine=duration=1`.
- `a.router.Push("/album/MPREb_test")`, `tt := ui.NewTester(a.view, 1000, 700)`.

- **Play.** `p.click("Album Track One", "playing")`. `r.asked("vid-3")` is true and the player bar shows the title.
- **Next.** `p.click("Next", "next")`. The seeded album has one track, so Next stays on `vid-3`. Use a longer queue to see it move, and assert `a.current.VideoID`.
- **Queue panel.** `p.click("Up next", "bar")`, then check `a.nowPlaying.open`. The panel draws in its own view. Make a second window with `panel := newProof(t, ui.NewTester(a.nowPlayingView, 1000, 700))` and call `panel.step`. It shows `Lyrics`, `Related` and `Clear queue`. To edit, click `Remove Track b from the queue` or `Clear queue` in that window, as `TestEditingTheQueueFromItsPanel` does.
- **Failure.** Set `r.err["bad"] = errors.New("Video unavailable")` before the click. The bar shows `This track is no longer available.` or the skip notice.
- **Real audio.** Make a tone with `ffmpeg -f lavfi -i sine=duration=3`, set `r.file`, click the track, then loop `a.poll()` and `time.Sleep(10ms)` until `a.player.Position()` passes 500ms and `a.player.Playing()` is true. Then `p.step("heard")`. Verified: position reaches 700ms in under half a second.
- **Resume.** Mirror `TestPlaybackResumesWhereItWasLeft`. A second app on the same data comes back paused.
- **Lyrics.** In the player panel, `Lyrics` loads YouTube Music's timed lines, then LRCLIB's (`a.lyricsSource`, stubbed to nothing in `newTestApp`; set it to return `lrclib.Lyrics`). A track with timed lines shows `Synced` and `Static` chips; `Static` saves `StaticLyrics`. The lit line follows `a.playbackPosition()`, so set `a.total` as well as `a.scrub`, or the position slider clamps it back to 0. Use a window wider than 1180 to see the panel beside the player.
- **Mini player.** `ui.NewTester(a.miniPlayer, miniWidth, miniHeight)`. `Nothing playing` shows when idle. Its panel button is `Show up next, lyrics and related`.

## Gotchas

- Sound is not audible in a proof. "Playing" means `a.player.Position()` advanced and ffmpeg exited cleanly on the null device.
- The app readies the next track ahead of time, so `r.asked` for a later track can be true before it plays. Assert on `a.current.VideoID` and the screen.
- Without `r.file` the stream is a fake path, so the bar shows `Loading` and the position stays `0:00`. That proves what the app decided. A proof of sound needs `r.file`.
- Time based behavior (seek, handover) needs real sleeps. `a.sleep` is inline only for the app's own delays.
