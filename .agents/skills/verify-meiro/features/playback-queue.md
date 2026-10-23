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
- `play-volume` keeps the mute button visible in narrow windows and reveals
  the vertical volume slider on hover.
- `play-mini` shows and controls the mini player.
- `play-mini-navigation` opens an artist page from the mini player's track menu and returns to the main window.
- `play-real-audio` decodes real audio through ffmpeg on the null device.

## How to get to it (user POV)

- Click a track row. Click `Play` or `Shuffle` on an album, playlist or artist page.
- Use the player bar buttons `Shuffle`, `Previous`, `Next`, `Repeat`, `Mute`, and the `Position` and `Volume` sliders.
- In a narrow window, hover `Mute` to reveal the vertical `Volume` slider;
  move onto the slider and drag it to adjust playback without losing the panel.
- Click `Up next` in the player bar, or `Open the player`, to open the player panel with `Up next`, `Lyrics` and `Related`.
- Open the tray mini player from the tray icon on macOS and Windows. On Linux, choose `Open Meiro` from the tray menu to restore the main window; Linux does not offer the tray mini player.
- In the mini player's queue, right-click a track and choose `Go to artist`. The flyout closes and the main window shows that artist's page.
- Right click a track for `Play next` and `Add to queue`.
- In `Up next`, Cmd-click on macOS or Ctrl-click on Windows/Linux to select
  several tracks; Shift-click extends the selection. Drag a selected row to
  reorder the group, or right-click a row and choose `Remove N tracks`.
  The selection highlight follows the active theme, and the track playing is
  not removed.
- Press `Space`, `Cmd+Shift+L`, `Cmd+Shift+J`, `Cmd+Shift+R`, `Cmd+Shift+S`.

## Driving it with the headless tester

Preconditions:

- Use `a, r := playbackApp(t)`. It sets `AutoPlay` off and resolves streams through `r`, so a click needs no yt-dlp. Set `r.file` to a tone for real audio, as `TestPlayingATrackThroughToTheEnd` does with `ffmpeg -f lavfi -i sine=duration=1`.
- `a.router.Push("/album/MPREb_test")`, `tt := ui.NewTester(a.view, 1000, 700)`.

- **Play.** `p.click("Album Track One", "playing")`. `r.asked("vid-3")` is true and the player bar shows the title.
- **Narrow volume.** Set `a.current` to a seeded track, make a 920x700
  `ui.NewTester(a.view, ...)`, and record the idle player bar. Move over
  `Mute` and record again: `Volume` appears with a taller-than-wide bound.
  Move onto and drag the slider, assert `a.volume` changes, then move away and
  check the slider closes. At 1280 pixels wide, `Volume` is horizontal and
  visible without hovering.
- **Next.** `p.click("Next", "next")`. The seeded album has one track, so Next stays on `vid-3`. Use a longer queue to see it move, and assert `a.current.VideoID`.
- **Queue panel.** `p.click("Up next", "bar")`, then check `a.nowPlaying.open`. The panel draws in its own view. Make a second window with `panel := newProof(t, ui.NewTester(a.nowPlayingView, 1000, 700))` and call `panel.step`. It shows `Lyrics`, `Related` and `Clear queue`. The header reads `Playing from` and the playlist on one line, and the source, `Auto-play` and the count sit the same distance apart. To edit, right-click a row (`tt.RightClickAt` its middle) and choose `Remove from queue`, or click `Clear queue`, as `TestEditingTheQueueFromItsPanel` does.
- **Failure.** Set `r.err["bad"] = errors.New("Video unavailable")` before the click. The bar shows `This track is no longer available.` or the skip notice.
- **Real audio.** Make a tone with `ffmpeg -f lavfi -i sine=duration=3`, set `r.file`, click the track, then loop `a.poll()` and `time.Sleep(10ms)` until `a.player.Position()` passes 500ms and `a.player.Playing()` is true. Then `p.step("heard")`. Verified: position reaches 700ms in under half a second.
- **Resume.** Mirror `TestPlaybackResumesWhereItWasLeft`. A second app on the same data comes back paused.
- **Lyrics.** In the player panel, `Lyrics` loads YouTube Music's timed lines, then LRCLIB's (`a.lyricsSource`, stubbed to nothing in `newTestApp`; set it to return `lrclib.Lyrics`). A track with timed lines shows `Synced` and `Static` chips; `Static` saves `StaticLyrics`. The lit line follows `a.playbackPosition()`, so set `a.total` as well as `a.scrub`, or the position slider clamps it back to 0. Use a window wider than 1180 to see the panel beside the player.
- **The same song twice.** Right-click a track and `Add to queue`, then play it and open `Up next`: the queue holds the song twice, and only one row is drawn as playing, with the tonal row and the dancing bars. The copy that does not play carries its `Remove … from the queue` button. `a.playingRow(item, songOptions{list: "queue", position: i})` is the rule: a queue row is picked out by its place, every other list by the song's ID.
- **Mini player.** `ui.NewTester(a.miniPlayer, miniWidth, miniHeight)`. `Nothing playing` shows when idle. The artwork, track details, scrubber and transport sit above a compact queue with its source; with a long queue, verify at least `Track e` is visible. Call `a.setMiniShown(true)` to load the panel, as opening the tray player does on macOS and Windows. Check the `Track a` and `Lyrics` bounds to confirm the vertical layout.
- **Open an artist from the mini player.** Call `a.setMiniShown(true)`, build both `a.view` and `a.miniPlayer` testers, then right-click a track in the mini player's queue and click `Go to artist`. The main tester shows the artist page and `a.nowPlaying.miniOpen` is false. The headless tester cannot confirm OS-level window focus.
- **Linux tray.** The minimal AppIndicator menu offers `Open Meiro` and `Quit`. `Open Meiro` restores the main window rather than creating a separate mini-player flyout. The tray also needs the AppIndicator runtime and a StatusNotifier-capable panel; see [Linux troubleshooting](../../../../README.md#troubleshooting).

## Gotchas

- Sound is not audible in a proof. "Playing" means `a.player.Position()` advanced and ffmpeg exited cleanly on the null device.
- The app readies the next track ahead of time, so `r.asked` for a later track can be true before it plays. Assert on `a.current.VideoID` and the screen.
- Without `r.file` the stream is a fake path, so the bar shows `Loading` and the position stays `0:00`. That proves what the app decided. A proof of sound needs `r.file`.
- Time based behavior (seek, handover) needs real sleeps. `a.sleep` is inline only for the app's own delays.
