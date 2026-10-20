# Changelog

All notable changes to Meiro are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **A playlist can be deleted from its menu.** The menu on a playlist card, and
  the menu on a playlist page, offers **Delete playlist**. It asks first, and
  names the playlist it would take, because a delete cannot be undone.

### Fixed

- **Auto-play does not queue music-video recommendations.** It prefers the
  song version when both are recommended, and does not change your chosen
  tracks or the one playing.
- **The "Close Meiro?" prompt does what you choose.** Dismissing it with
  Escape or its close button keeps the window open instead of quitting, and
  **Minimize to tray** shows the tray icon when it was off, so the window can
  be brought back even without **Remember my choice**.

## [0.4.2] - 2026-10-20

### Added

- **Windows has a portable download.** Extract the ZIP and run Meiro without
  installing it.

### Changed

- **A playlist page shows what YouTube suggests under it as its own part.** The
  suggested songs are there as the page opens, after the playlist, behind a
  divider and a **Similar songs** heading, five at a time; the **Show more**
  button under them shows the next ones. They no longer join the playlist's
  songs or its queue, so playing a playlist song plays the playlist, and
  playing a suggested song plays the suggestions.
- **The player bar keeps its mute button in narrow windows.** Hover it to
  reveal a vertical volume slider without taking space from the track.
- **The Linux tray menu now offers Open Meiro and Quit.** AppIndicator does
  not report icon position, so Meiro does not offer its custom tray mini
  player on Linux; Open Meiro restores the main window.
- **Discord Rich Presence shows a time bar with the track's length** instead
  of only the elapsed time.
- **Discord's member list shows the artist you are listening to** instead of
  Meiro's name.

### Fixed

- **Signing in works again.** A session was sent to YouTube with an
  authorization hash that no longer matched where the request went, so YouTube
  answered as if the session had expired: signing in was refused, a session
  that worked was reported as expired, and the library, likes and playlist
  writes failed. The hash now matches the request.
- **Discord Rich Presence works with a Flatpak Discord.** Meiro finds the
  socket Flatpak exports, so the manual symlink workaround is no longer
  needed.
- **Button labels stay whole when they fit.** Theme, playlist and shortcut
  buttons no longer lose the end of a label to an ellipsis because of small
  differences in platform font measurements.
- **Linux packages install the AppIndicator runtime needed to show the tray icon.**
- **Home shelves no longer show scrollbars over their artwork.** Use the left
  and right arrows to move through a shelf.
- **Show more on Home now appends the next sections to the feed.**

## [0.4.1] - 2026-10-19

### Changed

- **The tray mini player keeps its narrow, vertical shape, with smaller tabs
  and auto-play controls and more upcoming tracks visible.**
- **The playlist sort direction button also reverses the original order.** The
  button now stays before a field is chosen, and descending reads the whole
  playlist backwards, across its sections, which is what you want when you look
  for the songs you added last. Choosing another field also keeps the direction
  the page was in, instead of resetting it.

### Fixed

- **Discord Rich Presence shows the credited artist.** It no longer shows the
  song's full subtitle, which could also include the song type, album or length.

## [0.4.0] - 2026-10-18

### Added

- **Discord Rich Presence.** When enabled in Settings, Discord shows the
  playing track, artist, cover and elapsed time, without repeating the title
  in the cover's hover text. The status clears when playback pauses or stops.

### Fixed

- **YouTube brand channels can be switched from the account menu.** Meiro now
  reads the brand IDs YouTube supplies for channel switching, and marks only
  the active channel as selected.
- **Tracks now play while signed in.** Stream URLs resolve without account
  cookies, and YouTube playback history is reported after audio starts.
## [0.3.3] - 2026-10-18

### Changed

- **Playlist sort has a separate direction button.** The sort menu now only
  picks what to sort by, and a button beside it reverses the order in one
  click.

### Fixed

- **A track that YouTube gives no audio for now says so in plain words.**
  Before, the player bar and the skip message showed yt-dlp's raw
  "Requested format is not available" text. The skip message has a Learn
  more button that explains an outdated yt-dlp and what to do for your build:
  install the latest release of the full build, or update yt-dlp yourself in
  the minimal build.
- **The log file now records errors when Meiro is opened from the desktop.**
  Before, a launch with no standard error left the log empty, so a track that
  was skipped had no reason in the log.

## [0.3.2] - 2026-10-17

### Changed

- **The tray player always shows up next, lyrics and related.** The button that
  showed and hid them is gone, so the player keeps one size and no longer
  stretches or opens again.

### Fixed

- **The dynamic theme now follows the artwork when only the tray player is
  open.** For many covers the tray player never took their colour, because it
  loaded the picture at a different size than the theme reads. It now updates
  without the main window.

## [0.3.1] - 2026-10-17

### Changed

- **Settings is grouped under headings.** Cards now sit under Account,
  Appearance and sound, System, Storage and privacy, and About and support.
  Normalize loudness moved into Sound, and Show stats moved next to the
  listening history.

### Fixed

- **Auto-play no longer queues the same song twice.** Recommendations with the
  same title and artist are skipped, even when their lengths are missing or the
  title differs only in case or punctuation. When a song is recommended as both
  a video and a music version, the music version is queued.

- **The tray player now follows the artwork colour on its own.** With the
  dynamic theme on, the tray player took the new track's colour only after you
  opened the main window. It now updates when you open the tray player.

- **The tray player no longer stretches when you show or hide up next.** The
  player now opens again at the new size, so its content stays sharp.

- **The window opens again after you close it to the tray.** Open Meiro in the
  mini player, a second launch, a notification and a Dock click now show the
  hidden window.

## [0.3.0] - 2026-10-16

### Added

- **Firefox and Zen sign in with a browser window.** Account setup now opens
  a separate Firefox or Zen window with a temporary profile when one of those
  is the default browser, or when it is the one installed. Native, Flatpak and
  Snap installs all work, because the profile is created in the data directory
  the browser's sandbox sees. Meiro reads the session from that profile the
  way an import does, closes the window, and deletes the profile.
- **Synced lyrics.** The Lyrics tab now shows timed lyrics, from YouTube Music
  when it has them and from LRCLIB otherwise. The line being sung is lit and
  glides to the middle as the song plays, lines fade the further they are from
  it, and a click on a line jumps to it. Choose **Synced** or **Static** above
  the lyrics, where the source of the lyrics also shows; Meiro remembers the
  choice. A lookup that fails is tried again the next time the tab opens.
- **Windows release (untested).** Each release now includes a Windows
  installer (`amd64`) in a full and a minimal build. Windows support is
  untested: the author has no Windows machine, and no one has run Meiro there.

### Fixed

- **A Firefox or Zen sign-in finds the session it just made.** Firefox keeps
  a fresh cookie in a write-ahead log, and the sign-in read only the main
  database, so a finished sign-in could look signed out. The window now makes
  Firefox checkpoint the cookie at once.
- **A Firefox or Zen sign-in falls back to a Chromium browser without
  yt-dlp.** Reading a Firefox profile needs yt-dlp. Without it, the window
  opens in a Chromium-based browser instead of failing.
- **A removed Flatpak or Snap install no longer hides a native browser.** An
  export or snap link can outlive an uninstall and win over the browser
  actually installed. The sign-in now asks the package manager whether the
  install is still there.
- **The fallback order is the same on every platform.** When the default
  browser is not one Meiro drives, the window opens in the first
  Chromium-based browser Meiro knows, on Windows and macOS too. A user
  Flatpak install under a custom `XDG_DATA_HOME` is found as well.
- **A Zen Flatpak install shows its session in the import menu.** The menu
  searched a directory Zen never writes, so a session in a Flatpak Zen was
  missing from the list. It now finds the profiles in the directory Flatpak
  persists, `~/.var/app/app.zen_browser.zen/.zen`.
- **The menu button no longer slides.** When the side menu opens or closes,
  the button stays above the icons instead of drifting while the menu grows.
- **Small buttons are easier to hit.** Icon buttons, chips, switches and
  sliders now react to a press up to 48 pixels around them, as large as their
  look is small. Neighbouring buttons (the player's, the queue's and a shelf's
  arrows) sit a little further apart, so a press on one never lands on the
  next. The search bar keeps one width on every page, the playlist page lines its "Songs" heading up with the other pages, and the
  equaliser sliders spread across their card.
- **Cards line up with their heading, and the player bar fits narrow windows.**
  The name and details under a card's picture start at the picture's edge, as
  the section heading does. In a narrow window the player bar keeps room for
  the track's name instead of showing "…".
- **Album, playlist and artist pages fit narrow windows.** The picture and the
  buttons shrink, so Play, Shuffle and the rest stay on one line. The pictures
  of the songs now start where the page's picture and headings do.
- **The expanded navigation rail lines up.** Its menu button sits over the
  icons below it and no longer pushes them down when the rail opens. The twelve
  colours in Settings fit on one line.
- **The menu button on a picture can be seen.** On the large picture of the
  player and on cards, the three dots sat straight on the artwork and
  vanished against a bright cover. They now have a soft round ground.
- **Song lengths no longer jump.** A song's length stayed put only until the
  pointer reached its row, when the menu button pushed it to the left. The
  button now has its place held, and the dots of the song that is paused sit in
  the middle of its row.
- **Window buttons on Windows sit on the grey frame.** The page
  surface now starts below them instead of running under them, and the
  navigation rail no longer keeps an empty band at its top.
- **Songs in the queue no longer show as videos.** The recommended tracks after
  a song now show as songs. Only real music videos carry the video badge.
- **The playlist search bar grows and shrinks with its control.** It now
  expands beside the buttons that play the page, and closes back into the
  button that opened it. It used to open in a row of its own above them, at
  full size, and then jump straight into the button when it closed.
- **The playlist sort menu closes when its control is pressed again.** The
  button that opened the menu now closes it too, as the account menu's button
  does.

## [0.2.0] - 2026-10-15

### Added

- **Sign in with a browser window.** Account setup opens a separate
  Chromium-based browser (your default browser when it is one, such as Chrome,
  Edge, Brave, Helium, Chromium, or Vivaldi) with a temporary profile. Sign in
  there; Meiro reads the session, closes the window, and deletes the profile.
  Because that window is never used again, YouTube does not rotate its
  session. Importing from a browser and pasting a Cookie header are now under
  an Advanced toggle.
- **Ten-band equaliser.** Shape the sound in Settings → Sound: ten sliders
  over the usual frequency bands, plus the presets an audio player offers.
  A change restarts the music where it is, and a boosted band reserves
  matching headroom so the shaped signal cannot clip.
- **Loudness normalization** keeps tracks at a more consistent volume when enabled in Settings and the account is signed in.
- **Playlist sorting**: order playlist songs by title, artist, or duration in
  either direction. The selected order also controls Play, Shuffle, track
  activation, and playlist search results.
- **Last.fm scrobbling.** Connect your account in Settings; Meiro then sends
  what is playing and scrobbles each track once you have listened to half of
  it, or four minutes. Pause scrobbling without disconnecting, and plays that
  could not be sent are kept and retried, also after a restart.

### Changed

- A song played on its own now starts its mix directly, which saves one
  request.
- Settings scrolls faster, because only the cards in view are built and
  painted, and the hue slider paints fewer gradients. A page restored from
  the page cache returns to its previous scroll position.

### Fixed

- Signed-in playback is now reported to YouTube Music, so plays reach the
  listening history that recommendations use.
- On macOS, apps started from the Dock or Finder now find `ffmpeg` and
  `yt-dlp` in common tool folders such as Homebrew's.

## [0.1.0] - 2026-10-14

The first release.

### Added

- **Browse** YouTube Music: the Home feed and Explore, plus artist, album,
  and playlist pages, with your Library when you are signed in.
- **Search** from the bar on every page, filter the results by the categories
  YouTube Music offers, and keep your eight most recent searches.
- **Playback** from feeds, search results, and playlists, with seek, volume,
  previous and next track, shuffle, and repeat for the current track or the
  queue. A compact player stays available while you browse, and the full
  player shows the queue, lyrics, and related tracks.
- **Auto-play**, on by default, to add YouTube Music recommendations after the
  current queue ends, toggled from the queue.
- **Appearance** that follows the system theme or uses light or dark, with
  Material 3 palettes whose hue you can adjust or set from the current track's
  artwork.
- **Audio cache** for the ten most recently played tracks by default, with a
  configurable limit and folder and kept in a private cache folder. Set the
  limit to `0` to turn the cache off.
- **Keyboard shortcuts** for playback, navigation, search, and Settings, with
  a held modifier showing each button's key, all rebindable in Settings.
- **System media controls** through macOS Now Playing and Linux MPRIS, with
  track details, cover art, and playback state, and commands for play, pause,
  stop, previous, next, seek, volume, shuffle, and repeat.
- **Account import** from the YouTube Music session in Chrome, Safari,
  Firefox, Brave, Edge, or Helium, stored in the system credential store where
  one is available. Public browsing and playback need no account.
- **Search menus**: right-click a result on the search page, or an item in the
  panel under the search bar, to open its menu. A song offers Play next, Add to
  queue, Add to playlist and Copy link; an album, artist or playlist offers the
  page it names and its link, with Shuffle play for a playlist. The leading
  result offers the same menu, from a button on its artwork.
- **Playlist search**: an icon button beside a playlist page's Play and Shuffle
  buttons opens a bar that narrows the page to the songs whose title or artist
  holds what is typed. Play, Shuffle and a song's own place still use the whole
  playlist, so a match plays from where it sits in it.
- **Tray mini player**: the tray icon's menu opens a small player with the
  current track's artwork, a position slider, shuffle, previous, play or pause,
  next and repeat, and buttons to open Meiro or quit. On Linux it opens in the
  middle of the screen, since tray icons there report no clicks.
- **Packages** for macOS as one universal download that runs on Apple Silicon
  and Intel, and Linux `amd64` and `arm64` as `.deb`, `.rpm`, `.AppImage`, and
  `.tar.gz`. Full builds bundle ffmpeg and yt-dlp; minimal builds use the ones
  on your system.
- **Automatic updates**: the app checks once a day for a signed release, shows
  its notes, and offers to install, skip, or remind you later, with optional
  background installation in Settings. Package installs and development
  builds do not check.
