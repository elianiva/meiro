# Downloads

A user downloads songs, albums and playlists, then plays them with no network. The Downloads page lists what is saved, and a downloaded album or playlist page opens from its download when YouTube Music is out of reach.

## Sub-features

- `download-song` downloads a song from its menu on any row or card, and removes it again from the same menu. A downloaded song shows a check beside its length.
- `download-collection` downloads an album or playlist from its page's Download button, which then reads Downloading n/m and Downloaded. Clicking it again removes the download. Album and playlist cards and rows offer Download and Remove download in their menu.
- `downloads-page` lists the downloaded albums and playlists, then the songs, and plays from them.
- `downloads-offline` plays a downloaded song from its file without resolving it, and opens a downloaded album or playlist from its download when the network fails.

## How to get to it (user POV)

- Open a song's menu and choose Download.
- Open an album or playlist and click Download beside Play and Shuffle.
- Click Downloads in the navigation rail.
- Disconnect, open Downloads, open an album and play a song.

## Driving it with the headless tester

Preconditions:

- Run `scripts/doctor.sh` first.
- Call `a.openDownloads(dir)` with a temp dir: without it the app has no downloads folder and hides every download control.
- Give `playbackApp(t)` a resolver URL served by `httptest`, so the download saves real bytes.
- `TestADownloadedAlbumPlaysOffline` downloads the album, checks that the album page keeps its own content after a visit to Downloads, restarts the app on the same folder with every request failing, opens the album from Downloads, plays it without resolving, and removes the download.
- `offline.TestRemovingACollectionKeepsSongsWantedElsewhere` and `offline.TestReadKeepsListedSongsAndSweepsUnlistedAudio` cover the index.

## Gotchas

- `a.run` is inline in tests, so a download finishes inside the click. The Downloading n/m state is not visible headless.
- The headless tester draws a page it navigated to blank after a router push from another page, before and after this feature. The text tree is still right. Open a new `ui.NewTester` on the page to capture it.
- The hero button and the song menu both say Download: `tt.Click("Download")` finds the hero button first on an album page.
- Native check: the menu Download on a real song saves a WebM file under `Downloads/` in the app's data folder, and playing it logs `playback: playing downloaded audio`.
