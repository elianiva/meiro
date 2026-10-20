# Settings and shortcuts

A user changes the theme and equaliser, rebinds shortcuts, clears history, opens the logs, reports a problem, and connects Last.fm. Settings survive a restart.

## Sub-features

- `settings-theme` switches `Dark` and a seed colour such as `Rose`.
- `settings-persist` writes `settings.json` and reads it back.
- `settings-eq` toggles `Equaliser on` and picks a preset.
- `settings-shortcuts` shows, records, conflicts and resets shortcuts, and runs the defaults.
- `settings-history` clears listening history with `Clear history`, then `Delete all plays`.
- `settings-support` opens `Open logs folder` and `Report a problem` with a prefilled issue.
- `settings-lastfm` connects, pauses and disconnects Last.fm, and scrobbles qualifying plays.
- `settings-top-bar` leaves the music search and the account out of the top bar of the settings pages, and keeps both on a page of YouTube Music.

## How to get to it (user POV)

- Click `Settings` in the rail or press `Cmd+,`.
- Hold `Cmd` over the app to see the shortcut hints.

## Driving it with the headless tester

Preconditions:

- `a := newTestApp(t)`, `a.saver.Path = filepath.Join(t.TempDir(), "settings.json")`, `a.router.Push("/settings")`, a window of 1000x900.

- **Theme.** Verified. `p.click("Dark", "dark")`, `p.click("Rose", "rose")`. Then `a.saver.Save(&a.settings)`, `a.saver.Wait()` and read the file. Mirror `TestSettingsPersistAcrossRestarts`.
- **Restart.** Build a second `newTestApp(t)` and `settings.Load(path)`. The mode and seed came back.
- **Shortcut.** Mirror `TestRecordingANewShortcut`. Press the key with `tt.Key(mods, key)`, never by calling the action.
- **Top bar.** Verified by `TestSettingsPagesLeaveTheMusicSearchOutOfTheTopBar`. `tt.Find(searchPlaceholder)` is absent on `/settings` and `/stats`; the account button there is only the page's own card; `/home` keeps both. The account stays one click away as the settings' first card.
- **History.** `p.click("Clear history", "confirm")`, `p.click("Delete all plays", "cleared")`. `a.history` is empty and the file on disk is too.
- **Last.fm.** Mirror `TestConnectingPausingAndDisconnectingInSettings`. Set `MEIRO_LASTFM_API_KEY` and `MEIRO_LASTFM_SECRET` in the test environment only when the test needs a key, with a fake Last.fm transport.

## Gotchas

- Settings write through a debounced `Writer`. Call `Save` and `Wait` before you read the file.
- Shortcut tests press `ui.Cmd` and let the app map it, as `shortcuts_test.go` does. Copy that.
- The session key must not appear in `settings.json` (`TestLastFMSettingsAreKeptWithoutTheSessionKey`). Grep the file in a Last.fm proof.
- Never put a real cookie, key or token in a proof artifact. `steps.txt` records every text on screen.
