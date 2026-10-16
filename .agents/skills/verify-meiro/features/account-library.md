# Account and library

A user signs in, sees their library, likes tracks, subscribes to artists, saves albums and playlists, and makes or edits playlists. A write that YouTube refuses is taken back.

## Sub-features

- `account-signin` signs in with a browser window, an imported browser session, or a pasted Cookie header.
- `account-restore` restores the session at start and clears it on sign out.
- `account-rejected` asks to sign in again when YouTube refuses the session.
- `library-save` saves and removes an album or playlist, from the page and from its card.
- `library-like` likes and unlikes from the track menu and the player heart.
- `library-subscribe` subscribes and unsubscribes on an artist page.
- `library-playlist` creates a playlist, adds a song, and sorts or searches a playlist page.
- `library-refused` undoes the visible change when the write returns 400.

## How to get to it (user POV)

- Click the account button, then `Sign in`, `Sign in with a browser window`, or `Import from a browser`.
- Click `Library`, then `New playlist`.
- Hover a track row and click `More options for <title>`, then `Like` or `Add to playlist`.
- Click `Subscribe` on an artist page. Click `Save to library` on an album.

## Driving it with the headless tester

Preconditions:

- Use `signedInApp(t)`. For sign-in flows start from `newTestApp()`.
- Call `resetFakes()` first. It zeroes the like, subscribe and refuse counters and resets the fake library. Also `libraryLoads.Store(0)` and `playlistCreates.Store(0)` when you assert on them.

- **Save.** On `/album/MPREb_test`, `p.click("Save to library", "saved")`. The button reads `Remove from library`. Verified: `written().endpoint` ends in `/like/like`, because saving an album is a like write.
- **Like.** `p.openMenu("Album Track One", "menu")`, then `p.click("Like", "liked")`. `likeWrite()` returns `/like/like` and `vid-3`. Verified: after Save and Like in one test, `likeWrites.Load()` is `2`.
- **Subscribe.** On `/artist/UCartist`, click `Subscribe`. It reads `Subscribed` and `subscribeWrites.Load()` is `1`.
- **New playlist.** On `/library`, `p.click("New playlist", "dialog")`, type a name, `p.click("Create", "created")`. `playlistCreates.Load()` is `1`.
- **Search a playlist page.** On `/playlist/VLPL_songs` the control reads `Search in this playlist`. Clicking it opens the bar in place: `tt.Find("Search in this playlist")` must overlap the `Play` row, not a row of its own above it. The bar grows and shrinks through widths between the button's and its own, so pressing `Close the search` must show widths between the two on the way back. Typing `ameiro` narrows the rows to the 2 songs whose title matches, while `a.page.playable` keeps all 3, and a match plays from its own place in the whole playlist.
- **Refused.** `resetFakes()`, `refuseWrites.Store(true)`, then click `Save to library`. Verified: the button still reads `Save to library`.
- **Sign in.** Mirror `TestSignInWithABrowserWindow` and `TestFailedBrowserImportOffersPastingTheCookieHeader`.

## Gotchas

- A signed-out app shows no like, save or subscribe buttons. Prove that too (`TestSignedOutHasNoLike`).
- The fake accepts any `SAPISID=test` cookie. It says nothing about real YouTube auth. See [native-only.md](./native-only.md).
- Counters and `lastWrite` are process globals. Do not use `t.Parallel` in a proof that reads them.
- Use the counters, `written()`, `likeWrite()` and `subscriptionWrite()` as the second view of a write. A button label alone does not prove the request was sent.
- Saving an album and liking a track both count in `likeWrites`. Reset or compare a delta.
