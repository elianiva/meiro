# Changelog

All notable changes to Meiro are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.4.6] - 2026-10-22

### Fixed

- **Discord Rich Presence finds custom clients installed with Flatpak or Snap.** On Linux, Meiro checks each client's exported socket location instead of only the official Discord Flatpak paths. Any client that exposes Discord's standard RPC socket can share activity.
- **Keyboard navigation keeps focus on the control you choose.** Tab and Shift+Tab reach row and card menus without losing focus. Menus support arrow keys, Home, End and Escape, and return focus when closed. Shift+F10 opens an item's menu, the player tabs support arrow keys, and Enter plays the selected queue track.
- **Keyboard focus follows each control's shape.** Song rows have one outline instead of an inset outline inside a second highlight. Buttons, navigation and sliders indicate the focused shape rather than their larger pointer target, and selected queue text stays readable.
- **Keyboard slider changes take effect.** Position changes seek playback, volume changes are saved, and the narrow player's volume popup keeps focus when you Tab into it.

## [0.4.5] - 2026-10-22

### Added

- **A volume slider in the tray mini player.** Adjust or mute playback volume directly from the tray flyout.
- **Media buttons on the Windows taskbar thumbnail.** Hover over Meiro in the taskbar for Previous, Play or Pause, and Next, the way Spotify offers them, without bringing the window back. They follow the taskbar's light or dark theme and come back after Explorer restarts.

### Fixed

- **The player's side panel does not crowd controls in short windows.** When the
  window is wide but short, the queue and lyrics panel stays as an overlay behind
  its toggle instead of splitting side-by-side and squeezing the album artwork.
- **The tab of the page already showing puts the player away.** With the player
  open, the page's own tab in the rail did nothing, so the only way back to the
  page was the player's close button. The tab now closes the player and leaves
  the page as it was.
- **The queue's Recommended heading has equal space above and below, and cannot be selected.** It lines up with the artwork and separates recommendations from chosen tracks without a full-width divider.

### Changed

- **Carousel scrollbars sit below each card's text.** A clear gap separates
  the bar from the song title and artist.
- **The player always shows the wavy progress bar.** The setting is gone, and the wave is on by default on every platform.

## [0.4.4] - 2026-10-21

### Changed

- **Queue tracks can be selected and edited together.** Use Cmd-click on macOS or Ctrl-click on Windows/Linux to select tracks, then drag any selected row to move the group or right-click for **Remove N tracks**. The selection highlight follows the active theme, and the track playing is never removed.
- **The tray keeps its place.** macOS remembers where Meiro's icon was placed in the menu bar, and Windows remembers whether it was shown on the taskbar or in its overflow.
- **The mini player uses the system's popover material where supported.** Its contents stay legible over the native backdrop, while platforms without vibrancy keep Meiro's themed surface.
- **Full builds include FFmpeg 9.0.2 for audio decoding.**
- **Scrollbars match the active theme.** Their slimmer, primary-colour thumb is consistent across pages, and carousel shelves leave more room below the bar before the next section.
- **The queue's header reads in one line.** Where the queue plays from is one line, **Playing from** and the playlist in the same size with the playlist in bold, instead of a caption over a second line. The source, **Auto-play** and the count of the songs next sit one gap apart, and closer than before.
- **A queue row is its song and nothing else.** The three-dot button and the X are gone from the rows, and the length takes the width they held. Right-click a row for **Move up**, **Move down** and **Remove from queue**. The track playing still offers no remove.
- **Shuffle arranges the whole queue, and turning it off puts the queue back.** Turning shuffle on moves the track playing to the top and shuffles every other song behind it, the way Spotify does it, instead of arranging only the songs after the track playing and leaving the ones before it out. Turning shuffle off returns the queue to the order it held, with the track playing where it was, and the generated tracks keep their place at the end.
- **A long playlist sorts and filters without a stall.** Sorting by title, artist or length is several times faster, which also shows in each key typed into a sorted playlist's search.
- **Artwork shows sooner.** A cover appears as soon as it is downloaded, rather than after it is saved to disk.

### Fixed

- **The queue's Recommended heading aligns with the artwork below it, without a full-width divider.** The recommendations get a cleaner break from the chosen tracks.
- **Long playlists show all their songs.** Later batches stay with the playlist instead of being mistaken for recommendations.
- **Closing the player no longer flashes a blank frame before the page returns.** The page now appears as the player finishes closing, without a full-window clipping mask.
- **Buttons near the sheet's rounded corners respond where they appear.** The corner decoration no longer takes pointer events from the controls beneath it.
- **The full player's cover holds still while the window is resized.** It blinked to the placeholder and was decoded again at nearly every height the window passed through, which also pushed other covers out of memory.
- **The mini player's Clear queue ends on the queue's edge.** Its label stopped 16 px short of the lengths under it, so the narrow column read as two right edges.
- **The player's controls keep their distance from their edges.** The three-dot menu on the album art sat against the artwork's corner, and the Up next, Lyrics and Related row started further below the side panel's top than from its sides; both now keep the margin the rest of the player keeps.
- **A queue that holds the same song twice picks out only the copy playing.** Both copies were drawn as playing, with the dancing bars and the tonal row, because a row was picked out by its song rather than by its place in the queue.
- **A context menu opens again where it was closed.** The mark a menu left where the pointer pressed took the next right click on that spot for itself, so a card's or a page's menu could be opened only once until the page changed.
- **A playlist write YouTube refuses is written to the log in full**, for the add and the remove path, so a refusal that the player bar shows as a line can be read whole.
- **A track whose stream stops at its end plays the next one.** YouTube's server stops at the end of a track's data, which the app took for a stream that broke off: it found the stream again and played the last moment of the track a second time. The break now ends the track.
- **A track starts at its corrected volume.** With normalization on, the first moment of a track played at the volume the track before it had — or at the plain volume when the app started — until the correction landed a moment later. The gain is now in place before the audio starts.

## [0.4.3] - 2026-10-20

### Added

- **A playlist can be deleted from its menu.** The menu on a playlist card, and the menu on a playlist page, offers **Delete playlist**. It asks first, and names the playlist it would take, because a delete cannot be undone.
- **Podcast shows open.** A show from search opens as a page with its description and its episodes, newest first, each with its description, date and length. An episode plays like a track, with seek, the queue and the player bar. An episode you leave part-way through starts where you stopped the next time you play it, also after a restart. When you are signed in, Meiro also reads and updates YouTube Music's episode progress, so you can continue listening on another device; a local resume position takes priority.

### Changed

- **Discord shows every artist on a track.** Collaborations now list all credited artists instead of only the first.
- **Settings pages have a simpler top bar.** The music search and account menu are hidden while you are in Settings.

### Fixed

- **Auto-play skips music-video recommendations.** When YouTube recommends both a song and its video, Meiro chooses the song and leaves your queue and current track alone.
- **The "Close Meiro?" prompt respects your choice.** Pressing Escape or the close button keeps the app open. Choosing **Minimize to tray** also makes sure the tray icon appears, even if **Remember my choice** is off.

## [0.4.2] - 2026-10-20

### Added

- **Windows has a portable download.** Extract the ZIP and run Meiro without installing it.

### Changed

- **Similar songs have their own section on playlist pages.** The first five suggestions appear below the playlist under **Similar songs**. Choose **Show more** to load more. Suggestions stay separate from the playlist and its queue: playing a playlist song plays the playlist, while playing a suggestion starts the suggestions.
- **The player bar keeps its mute button in narrow windows.** Hover it to reveal a vertical volume slider without taking space from the track.
- **The Linux tray menu now offers Open Meiro and Quit.** The Linux tray does not provide the icon position needed to place the mini player, so **Open Meiro** brings back the main window instead.
- **Discord Rich Presence shows a time bar with the track's length** instead of only the elapsed time.
- **Discord's member list shows the artist you are listening to** instead of Meiro's name.

### Fixed

- **Signing in and account features work again.** YouTube rejected valid sessions because a request's authorization hash did not match its destination. The hash now matches, so sign-in, library access, likes and playlist changes work as expected.
- **Discord Rich Presence works with a Flatpak Discord.** Meiro finds the socket Flatpak exports, so the manual symlink workaround is no longer needed.
- **Button labels stay whole when they fit.** Theme, playlist and shortcut buttons no longer truncate labels that have room to display in full.
- **Linux packages install the AppIndicator runtime needed to show the tray icon.**
- **Home shelves no longer show scrollbars over their artwork.** Use the left and right arrows to move through a shelf.
- **Show more on Home now appends the next sections to the feed.**

## [0.4.1] - 2026-10-19

### Changed

- **The tray mini player stays compact and vertical.** Its tabs and auto-play controls are smaller, leaving more room to see upcoming tracks.
- **Playlist sorting makes it easier to find recently added songs.** Descending order now reverses the entire playlist, across sections. You can choose the sort direction before picking a field, and changing the field keeps the direction you selected.

### Fixed

- **Discord Rich Presence shows the credited artist.** It no longer shows the song's full subtitle, which could also include the song type, album or length.

## [0.4.0] - 2026-10-18

### Added

- **Discord Rich Presence.** When enabled in Settings, Discord shows the playing track, artist, cover and elapsed time, without repeating the title in the cover's hover text. The status clears when playback pauses or stops.

### Fixed

- **Switching YouTube brand accounts selects the right channel.** The account menu now marks only the active channel as selected.
- **Tracks play while signed in.** Plays are added to your YouTube Music history after playback starts.

## [0.3.3] - 2026-10-18

### Changed

- **Playlist sort has a separate direction button.** The sort menu now only picks what to sort by, and a button beside it reverses the order in one click.

### Fixed

- **Tracks without available audio now show a clear message.** Instead of yt-dlp's technical error, the player and skip message explain that yt-dlp may need an update. The **Learn more** button gives steps for full and minimal builds.
- **Desktop launches now record errors in the log.** Errors are saved even when Meiro is opened without a terminal, so a skipped track has a reason in the log.

## [0.3.2] - 2026-10-17

### Changed

- **The tray player always shows Up next, Lyrics and Related.** The show-or-hide button is gone, so the player keeps a consistent size instead of stretching or reopening.

### Fixed

- **The tray player's theme now follows the artwork on its own.** Its colours update with each track, even when the main window is closed.

## [0.3.1] - 2026-10-17

### Changed

- **Settings is easier to browse.** Cards are grouped under Account, Appearance and sound, System, Storage and privacy, and About and support. Loudness normalization is under Sound, and listening stats are next to listening history.

### Fixed

- **Auto-play no longer queues the same song twice.** Recommendations with the same title and artist are skipped, even when their lengths are missing or the title differs only in case or punctuation. When a song is recommended as both a video and a music version, the music version is queued.

- **The tray player updates its theme with the artwork.** With dynamic colours on, it now matches each track without needing you to open the main window.

- **The tray player no longer stretches when you show or hide up next.** The player now opens again at the new size, so its content stays sharp.

- **You can reopen Meiro after closing it to the tray.** Choose **Open Meiro** in the mini player, launch the app again, click a notification or click its Dock icon to show the window.

## [0.3.0] - 2026-10-16

### Added

- **Firefox and Zen sign in with a browser window.** Account setup now opens a separate Firefox or Zen window with a temporary profile when one of those is the default browser, or when it is the one installed. Native, Flatpak and Snap installs all work, because the profile is created in the data directory the browser's sandbox sees. Meiro reads the session from that profile the way an import does, closes the window, and deletes the profile.
- **Synced lyrics.** The Lyrics tab now shows timed lyrics, from YouTube Music when it has them and from LRCLIB otherwise. The line being sung is lit and glides to the middle as the song plays, lines fade the further they are from it, and a click on a line jumps to it. Choose **Synced** or **Static** above the lyrics, where the source of the lyrics also shows; Meiro remembers the choice. A lookup that fails is tried again the next time the tab opens.
- **Windows downloads are available (untested).** Releases include an `amd64` installer in full and minimal builds. Windows support has not yet been tested.

### Fixed

- **A Firefox or Zen sign-in finds the session it just made.** Firefox keeps a fresh cookie in a write-ahead log, and the sign-in read only the main database, so a finished sign-in could look signed out. The window now makes Firefox checkpoint the cookie at once.
- **A Firefox or Zen sign-in falls back to a Chromium browser without yt-dlp.** Reading a Firefox profile needs yt-dlp. Without it, the window opens in a Chromium-based browser instead of failing.
- **A removed Flatpak or Snap install no longer hides a native browser.** An export or snap link can outlive an uninstall and win over the browser actually installed. The sign-in now asks the package manager whether the install is still there.
- **The fallback order is the same on every platform.** When the default browser is not one Meiro drives, the window opens in the first Chromium-based browser Meiro knows, on Windows and macOS too. A user Flatpak install under a custom `XDG_DATA_HOME` is found as well.
- **A Zen Flatpak install shows its session in the import menu.** The menu searched a directory Zen never writes, so a session in a Flatpak Zen was missing from the list. It now finds the profiles in the directory Flatpak persists, `~/.var/app/app.zen_browser.zen/.zen`.
- **The menu button no longer slides.** When the side menu opens or closes, the button stays above the icons instead of drifting while the menu grows.
- **Small buttons are easier to hit.** Icon buttons, chips, switches and sliders now react to a press up to 48 pixels around them, as large as their look is small. Neighbouring buttons (the player's, the queue's and a shelf's arrows) sit a little further apart, so a press on one never lands on the next. The search bar keeps one width on every page, the playlist page lines its "Songs" heading up with the other pages, and the equaliser sliders spread across their card.
- **Cards line up with their heading, and the player bar fits narrow windows.** The name and details under a card's picture start at the picture's edge, as the section heading does. In a narrow window the player bar keeps room for the track's name instead of showing "…".
- **Album, playlist and artist pages fit narrow windows.** The picture and the buttons shrink, so Play, Shuffle and the rest stay on one line. The pictures of the songs now start where the page's picture and headings do.
- **The expanded navigation rail lines up.** Its menu button sits over the icons below it and no longer pushes them down when the rail opens. The twelve colours in Settings fit on one line.
- **The menu button on a picture can be seen.** On the large picture of the player and on cards, the three dots sat straight on the artwork and vanished against a bright cover. They now have a soft round ground.
- **Song lengths no longer jump.** A song's length stayed put only until the pointer reached its row, when the menu button pushed it to the left. The button now has its place held, and the dots of the song that is paused sit in the middle of its row.
- **Window buttons on Windows sit on the grey frame.** The page surface now starts below them instead of running under them, and the navigation rail no longer keeps an empty band at its top.
- **Songs in the queue no longer show as videos.** The recommended tracks after a song now show as songs. Only real music videos carry the video badge.
- **The playlist search bar grows and shrinks with its control.** It now expands beside the buttons that play the page, and closes back into the button that opened it. It used to open in a row of its own above them, at full size, and then jump straight into the button when it closed.
- **The playlist sort menu closes when its control is pressed again.** The button that opened the menu now closes it too, as the account menu's button does.

## [0.2.0] - 2026-10-15

### Added

- **Sign in with a browser window.** Account setup opens a separate Chromium-based browser (your default browser when it is one, such as Chrome, Edge, Brave, Helium, Chromium or Vivaldi) with a temporary profile. Sign in there; Meiro reads the session, closes the window and deletes the profile. YouTube does not rotate the session because the window is not used again. Browser import and Cookie-header sign-in are under **Advanced**.
- **Ten-band equaliser.** Shape the sound in Settings → Sound with ten frequency sliders and a set of presets. Changing the equaliser restarts playback at its current position. Boosted bands reserve matching headroom to prevent clipping.
- **Loudness normalization** keeps tracks at a more consistent volume when enabled in Settings and the account is signed in.
- **Playlist sorting**: order playlist songs by title, artist, or duration in either direction. The selected order also controls Play, Shuffle, track activation, and playlist search results.
- **Last.fm scrobbling.** Connect your account in Settings; Meiro then sends what is playing and scrobbles each track once you have listened to half of it, or four minutes. Pause scrobbling without disconnecting, and plays that could not be sent are kept and retried, also after a restart.

### Changed

- A song played on its own now starts its mix directly, which saves one request.
- Settings scrolls faster, because only the cards in view are built and painted, and the hue slider paints fewer gradients. A page restored from the page cache returns to its previous scroll position.

### Fixed

- Signed-in playback is now reported to YouTube Music, so plays reach the listening history that recommendations use.
- On macOS, apps started from the Dock or Finder now find `ffmpeg` and `yt-dlp` in common tool folders such as Homebrew's.

## [0.1.0] - 2026-10-14

The first release.

### Added

- **Browse** YouTube Music: the Home feed and Explore, plus artist, album, and playlist pages, with your Library when you are signed in.
- **Search** from the bar on every page, filter the results by the categories YouTube Music offers, and keep your eight most recent searches.
- **Playback** from feeds, search results, and playlists, with seek, volume, previous and next track, shuffle, and repeat for the current track or the queue. A compact player stays available while you browse, and the full player shows the queue, lyrics, and related tracks.
- **Auto-play**, on by default, to add YouTube Music recommendations after the current queue ends, toggled from the queue.
- **Appearance** that follows the system theme or uses light or dark, with Material 3 palettes whose hue you can adjust or set from the current track's artwork.
- **Audio cache** for the ten most recently played tracks by default, with a configurable limit and folder and kept in a private cache folder. Set the limit to `0` to turn the cache off.
- **Keyboard shortcuts** for playback, navigation, search, and Settings, with a held modifier showing each button's key, all rebindable in Settings.
- **System media controls** through macOS Now Playing and Linux MPRIS, with track details, cover art, and playback state, and commands for play, pause, stop, previous, next, seek, volume, shuffle, and repeat.
- **Account import** from the YouTube Music session in Chrome, Safari, Firefox, Brave, Edge, or Helium, stored in the system credential store where one is available. Public browsing and playback need no account.
- **Search menus**: right-click a result on the search page, or an item in the panel under the search bar, to open its menu. A song offers Play next, Add to queue, Add to playlist and Copy link; an album, artist or playlist offers the page it names and its link, with Shuffle play for a playlist. The leading result offers the same menu, from a button on its artwork.
- **Playlist search**: an icon button beside a playlist page's Play and Shuffle buttons opens a bar that narrows the page to the songs whose title or artist holds what is typed. Play, Shuffle and a song's own place still use the whole playlist, so a match plays from where it sits in it.
- **Tray mini player**: the tray icon's menu opens a small player with the current track's artwork, a position slider, shuffle, previous, play or pause, next and repeat, and buttons to open Meiro or quit. On Linux it opens in the middle of the screen, since tray icons there report no clicks.
- **Packages** for macOS as one universal download that runs on Apple Silicon and Intel, and Linux `amd64` and `arm64` as `.deb`, `.rpm`, `.AppImage`, and `.tar.gz`. Full builds bundle ffmpeg and yt-dlp; minimal builds use the ones on your system.
- **Automatic updates**: the app checks once a day for a signed release, shows its notes, and offers to install, skip, or remind you later, with optional background installation in Settings. Package installs and development builds do not check.
