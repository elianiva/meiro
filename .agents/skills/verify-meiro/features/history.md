# Listening history

A user opens History from the navigation rail to see what they played, newest first. Signed out, it lists plays made in Meiro. Signed in, it also reads the YouTube Music account history and folds it into the same days.

## Sub-features

- `history-local` groups Meiro plays by day, with `Today`, `Yesterday` and the date for older days. A song played twice in one section appears once.
- `history-merge` reads `FEmusic_history` when signed in. A known shelf title (`Today`, `Yesterday`, `This week`, `Last week`, `This month`, `Last month`, `This year`, `Last year`, a month name) covers a date range, and local days in that range join the shelf. Local plays come before the account plays in the same section.
- `history-fallback` keeps the local plays when the account read fails or returns no shelves. A signed-out app makes no account read.
- `history-play` plays a row and queues the rest of the page, the same as any song list. The account history has video IDs, titles, artists, lengths and covers, so its rows play and queue like local rows.
- `history-clear` stays in Settings. Clearing removes the local plays. The account history is YouTube's and stays until it is cleared there.

## How to get to it (user POV)

- Click History in the navigation rail.
- Play a song, open History, and find it under Today.
- Sign in, play something in YouTube Music elsewhere, open History, and find it in the matching shelf.

## Driving it with the headless tester

Preconditions:

- Run `scripts/doctor.sh` first.
- Call `resetFakes()`, then use `signedInApp(t)` to get the canned `historyResponse`. Set `a.historyLoaded = true` and seed `a.history` so the local read has finished.
- `historyLoads` counts account reads and `historyFails` makes them answer HTTP 500.

- **Merge.** `TestHistoryPageMergesAccountAndLocalPlays` seeds a local play of `shared-today`, which the fake also puts in Today. It asserts one queue entry for that song, the Today/Yesterday/This week order, and that a local play from three days ago joins `This week`.
- **Signed out.** `TestHistoryPageStaysLocalWhenSignedOut` asserts the local rows and `historyLoads == 0`.
- **Account failure.** `TestHistoryPageKeepsLocalPlaysWhenYouTubeFails` asserts the local rows stay, with no `Retry` message.
- **Merge rules.** `TestMergeHistoryFoldsKnownShelvesAndKeepsTheRest` covers the date ranges, an unknown shelf title, an older local day, and the per-section dedupe.

## Gotchas

- YouTube's shelves give no time inside a day. Known labels map to ranges. An unknown label stays as its own section and is placed by its position in the server list.
- `browseParts` drops identical rows from one page, so the fake cannot prove repeated plays inside a shelf.
- Clicking a row with no working stream advances to the next row when the first resolution fails. Click the last row to assert the current track, or use a playback app with a real tone.
- `Clear history` in Settings empties the local file, not the account's history.
- Native check: signing in is prohibited in the orb, so the account read is proven only against the fake. Signed-out local history can be shown in the real window.
