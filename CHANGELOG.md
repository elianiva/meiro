# Changelog

All notable changes to Meiro are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Windows release (untested).** Each release now includes a Windows
  installer (`amd64`) in a full and a minimal build. Windows support is
  untested: the author has no Windows machine, and no one has run Meiro there.

### Fixed

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
