# Meiro verification map

This directory is the maintained source for verifying what a Meiro user can do. Read this file, then open the feature file that matches the change. A proof that drives one convenient entry point is incomplete when the feature file lists others.

## Baseline preconditions

- Run `scripts/doctor.sh` and require no `FAIL` line.
- Every proof is a scratch Go test, `zz_proof_<feature>_test.go`, in the repo root. It builds the app with `newTestApp(t)` (or `signedInApp(t)`, or `stateApp(t, s, signedIn)`) and drives it with `ui.NewTester`. Copy `templates/zz_proof_test.go.tmpl`; `prove.sh` injects the shared recorder helpers.
- YouTube is `fakeMusic` in `harness_test.go`. Artwork is `noArtwork`. Stream URLs come from `playbackApp(t)`. Nothing touches the network, the account, or the sound card.
- A feature that needs a new canned YouTube response gets it in `harness_test.go` in the same change, so the proof and the committed tests share it.

## Driving conventions

- Click by visible label or accessible name, as the existing tests do: `tt.Click("Save to library")`. Never call `a.play`, `a.enqueue` or other app methods to act. Use them only to read state.
- Call `p.step(label)` after every user action. A proof is the sequence, not the last frame.
- Window size is 1000x700. The mini player has its own size, `miniWidth` by `miniHeight`.
- Seed data lives in `harness_test.go`. Titles you can rely on are `Album Track One` (`vid-3`), `Search Result Song`, `Yorushika`, `Ambient One`, `Quick picks`, `Night Drive`.
- `a.run` is inline and `a.sleep` returns at once. A background result is on screen after the click's frame. Real time passes only where a test plays real audio.

## Proof and skip reporting

- A UI proof has one screenshot per step and the matching line in `steps.txt`.
- A write proof (like, subscribe, save, playlist create, settings) also reads the second view. Assert the counters and `lastWrite` in `harness_test.go`, or reload the page and see the state again.
- Report the feature ID and entry point beside every artifact.
- Report an unreachable path with the attempted command and the unmet precondition. Do not call it verified through a different path.
- Native-only behavior is in [native-only.md](./native-only.md). A change there is never proven by the headless tester alone. Say what stays unverified.

## Features

- [Browse pages](./browse.md) covers Home, Explore, album, playlist and artist pages, and going back.
- [Podcasts](./podcast.md) covers opening a show, reading its episodes and resuming an episode.
- [Search](./search.md) covers the bar, suggestions, results, filters, and the next page.
- [Playback and queue](./playback-queue.md) covers playing, the queue, repeat, shuffle, seeking, failures, resume, and the mini player.
- [macOS notch player](./notch.md) covers the compact camera-side player, synced lyrics, fallback states and desktop setting.
- [Downloads](./downloads.md) covers downloading songs, albums and playlists, the Downloads page, and playing offline.
- [Account and library](./account-library.md) covers sign-in, like, subscribe, saving, and playlists.
- [Settings and shortcuts](./settings-shortcuts.md) covers theme, equaliser, shortcuts, history, and Last.fm.
- [Discord Rich Presence](./discord-presence.md) covers the settings, track activity, elapsed time, and clearing presence.
- [Page states](./page-states.md) covers loading, empty, failed and offline pages, and Retry.
- [Native-only behavior](./native-only.md) lists what no headless proof can show.
