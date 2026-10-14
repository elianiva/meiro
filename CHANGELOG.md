# Changelog

All notable changes to Meiro are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **Playlist track controls**: keep the hover play overlay centered inside the
  track artwork instead of placing it below the row.

### Added

- **Search menus**: right-click a result on the search page, or an item in the
  panel under the search bar, to open its menu. A song offers Play next, Add to
  queue, Add to playlist and Copy link; an album, artist or playlist offers the
  page it names and its link, with Shuffle play for a playlist. The leading
  result offers the same menu, from a button on its artwork.
- **Playlist search**: an icon button beside a playlist page's Play and Shuffle
  buttons opens a bar that narrows the page to the songs whose title or artist
  holds what is typed. Play, Shuffle and a song's own place still use the whole
  playlist, so a match plays from where it sits in it.

## [0.1.0]

<!-- Add the release date when v0.1.0 is tagged: ## [0.1.0] - YYYY-MM-DD -->

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
- **Packages** for macOS Apple Silicon and Intel, and Linux `amd64` and
  `arm64` as `.deb`, `.rpm`, `.AppImage`, and `.tar.gz`.
