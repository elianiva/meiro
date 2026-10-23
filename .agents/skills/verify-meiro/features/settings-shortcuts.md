# Settings and shortcuts

A user changes the theme and equaliser, rebinds shortcuts, clears history, opens the logs, reports a problem, and connects Last.fm. Settings survive a restart.

## Sub-features

- `settings-theme` switches `Dark` and a seed colour such as `Rose`.
- `settings-persist` writes `settings.json` and reads it back.
- `settings-eq` toggles `Equaliser on`, picks a preset, and changes slider gains live without restarting the current stream.
- `settings-shortcuts` shows, records, conflicts and resets shortcuts, and runs the defaults.
- `keyboard-navigation` covers Tab and Shift+Tab through row and card actions, menu arrows/Home/End/Escape, Shift+F10 context menus, player tabs, queue selection/activation, and keyboard sliders.
- `settings-history` clears listening history with `Clear history`, then `Delete all plays`.
- `settings-support` opens `Open logs folder` and `Report a problem` with a prefilled issue.
- `settings-lastfm` connects, pauses and disconnects Last.fm, and scrobbles qualifying plays.
- `settings-top-bar` keeps global music search and the account out of the app-owned top bar; Settings has a playlist-style search button that filters settings in place, while Stats has no search entry point.
- `settings-progress` keeps the wavy progress bar on by default and no longer offers a preference or performance warning in Settings.
- `settings-tray` selects the wordmark or the low-key O mark on macOS and Linux, and keeps that choice after restart.

## How to get to it (user POV)

- Click `Settings` in the rail or press `Cmd+,`.
- Hold `Cmd` over the app to see the shortcut hints.
- Use Tab to reach controls, Enter or Space to activate them, and Shift+F10 for an item's menu. Use arrows in menus and player tabs; Escape closes a menu and returns focus.

## Driving it with the headless tester

Preconditions:

- `a := newTestApp(t)`, `a.saver.Path = filepath.Join(t.TempDir(), "settings.json")`, `a.router.Push("/settings")`, a window of 1000x900.

- **Theme.** Verified. `p.click("Dark", "dark")`, `p.click("Rose", "rose")`. Then `a.saver.Save(&a.settings)`, `a.saver.Wait()` and read the file. Mirror `TestSettingsPersistAcrossRestarts`.
- **Restart.** Build a second `newTestApp(t)` and `settings.Load(path)`. The mode and seed came back.
- **Shortcut.** Mirror `TestRecordingANewShortcut`. Press the key with `tt.Key(mods, key)`, never by calling the action.
- **Keyboard-only navigation.** Mirror `TestKeyboardMenusKeepFocusAndRunActions`, `TestKeyboardPlayerTabsAndQueue`, `TestKeyboardOpensTheCoverMenu` and `TestKeyboardVolumeIsSaved` in `shortcuts_test.go`. The `tabTo` helper only sends Tab; verify the next action's focus explicitly. Test volume at 920 and 1280 pixels: the narrow popup must survive focus moving from Mute to Volume. `TestSeekingMovesThePlayerOffTheMainThread` checks Home and Right against the real decoder position, not only the scrubber value.
- **Top bar.** Verified by `TestSettingsSearchFiltersSettingsInPlace`. `/settings` has a `Search settings` icon button that expands into a local filter; searching `Discord` shows Discord Rich Presence without unrelated cards, an unmatched query shows an empty state, and clearing restores the full list. `/stats` has no search entry point. Global music search remains on `/home`.
- **Progress bar.** `TestSettingsDoNotOfferWavyProgressPreference` confirms Settings no longer offers the wave toggle or its warning. All player scrubbers use the shared `a.scrubber` with the wave enabled.
- **Tray icon.** `TestTrayIconStyleCanBeChangedAndSavedInSettings` checks the Wordmark default, changes to `O mark`, reads the saved setting, then restores Wordmark. The two resources are optically centered vector exports; native tray rendering still needs macOS and Linux panel checks.
- **History.** `p.click("Clear history", "confirm")`, `p.click("Delete all plays", "cleared")`. `a.history` is empty and the file on disk is too.
- **Last.fm.** Mirror `TestConnectingPausingAndDisconnectingInSettings`. Set `MEIRO_LASTFM_API_KEY` and `MEIRO_LASTFM_SECRET` in the test environment only when the test needs a key, with a fake Last.fm transport.

## Gotchas

- Settings write through a debounced `Writer`. Call `Save` and `Wait` before you read the file.
- Shortcut tests press `ui.Cmd` and let the app map it, as `shortcuts_test.go` does. Copy that.
- `tt.Focused("Track a")` also reports true when the enclosing queue list has focus. Use Home then Down to select a specific queue row; do not treat that label alone as proof of which row is selected.
- Give popover panels stable keys before building their children. A focus tooltip can change the order of overlays; without a key, tabbing from Mute to Volume rebuilds the slider under a different identity and restores focus to Mute.
- The session key must not appear in `settings.json` (`TestConnectingPausingAndDisconnectingInSettings`). Grep the file in a Last.fm proof.
- Never put a real cookie, key or token in a proof artifact. `steps.txt` records every text on screen.
