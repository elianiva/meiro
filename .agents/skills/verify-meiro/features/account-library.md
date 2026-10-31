# Account and library

A user signs in, sees their library, likes tracks, subscribes to artists, saves albums and playlists, and makes or edits playlists. A write that YouTube refuses is taken back.

## Sub-features

- `account-signin` signs in with a browser window, an imported browser session, or a pasted Cookie header.
- `account-restore` restores the session at start and clears it on sign out.
- `account-rejected` asks to sign in again when YouTube refuses the session.
- `library-save` saves and removes an album or playlist, from the page and from its card.
- `library-like` likes and unlikes from the track menu and the player heart.
- `library-dislike` dislikes and removes a dislike from the track menu and player; a refused write restores the previous rating.
- `library-subscribe` subscribes and unsubscribes on an artist page.
- `library-playlist` creates a playlist, adds a song, sorts or searches a playlist page, and deletes one.
- `library-refused` undoes the visible change when the write returns 400.

## How to get to it (user POV)

- Click the account button, then `Sign in`, `Sign in with a browser window`, or `Import from a browser`.
- Click `Library`, then `New playlist`.
- Hover a track row and click `More options for <title>`, then `Like` or `Add to playlist`.
- Click `Subscribe` on an artist page. Click `Save to library` on an album.

## Driving it with the headless tester

Preconditions:

- Use `signedInApp(t)`. For the fake-session sign-in flow start from `newTestApp(t)`; real account sign-in is prohibited in the orb and stays unverified.
- Call `resetFakes()` first. It zeroes the like, subscribe, delete and refuse counters, turns off the duplicate-answer mode, and resets the fake library. Also `libraryLoads.Store(0)` and `playlistCreates.Store(0)` when you assert on them.

- **Save.** On `/album/MPREb_test`, `p.click("Save to library", "saved")`. The button reads `Remove from library`. Verified: `written().endpoint` ends in `/like/like`, because saving an album is a like write.
- **Like.** `p.openMenu("Album Track One", "menu")`, then `p.click("Like", "liked")`. `likeWrite()` returns `/like/like` and `vid-3`. Verified: after Save and Like in one test, `likeWrites.Load()` is `2`.
- **Subscribe.** On `/artist/UCartist`, click `Subscribe`. It reads `Subscribed` and `subscribeWrites.Load()` is `1`.
- **New playlist.** On `/library`, `p.click("New playlist", "dialog")`, type a name, `p.click("Create", "created")`. `playlistCreates.Load()` is `1`.
- **Add to playlist.** `p.openMenu("Ambient One", "menu")`, `p.click("Add to playlist", "picker")`, then `p.click("Add to Hype", "added")`. Verified: `written()` is one `/browse/edit_playlist` for `PLhype`, the picker closes, and the toast reads `Added “Ambient One” to “Hype”`. With `duplicateWrites.Store(true)` YouTube answers the add with its duplicate notice instead: the same clicks close the picker and toast `“Ambient One” is already in “Hype”`, with no failed status on screen (`TestAddingASongAlreadyInAPlaylist`).
- **Sort a playlist page.** On `/playlist/VLPL_songs` the control reads `Sort: Original order`, and the button after it reads `Sort descending`. Pressing it reverses the playlist (`song-three`, `song-two`, `song-one`), and pressing it again, now reading `Sort ascending`, restores YouTube Music's order. `p.click("Sort: Original order", "menu")` shows `Original order`, `Title`, `Artist` and `Duration`; choosing a field sorts by it and keeps the direction, and the button then offers the other one. Pressing the sort control again closes the menu it opened.
- **Search a playlist page.** On `/playlist/VLPL_songs` the control reads `Search in this playlist`. Clicking it opens the bar in place: `tt.Find("Search in this playlist")` must overlap the `Play` row, not a row of its own above it. The bar grows and shrinks through widths between the button's and its own, so pressing `Close the search` must show widths between the two on the way back. Typing `ameiro` narrows the rows to the 2 songs whose title matches, while `a.page.playable` keeps all 3, and a match plays from its own place in the whole playlist.
- **Suggestions under a playlist.** The fake's playlist page carries the continuation YouTube sends (`playlist-more`), and its answer is a shelf: the suggested songs are on the page as it opens, under a `Similar songs` heading, five at a time, while `a.page.playable` keeps only the playlist's own songs. Clicking a suggestion plays from the suggestions alone; `Show more` shows the next ones. Mirror `TestAPlaylistShowsItsSuggestionsFiveAtATime`.
- **Delete a playlist.** On `/library`, right-click the `Hype` card and choose `Delete playlist`. The dialog names the playlist (`“Hype” is deleted from your account, and this cannot be undone.`) and writes nothing until it is answered: `playlistDeletes.Load()` stays `0` after `Cancel`, which leaves the card where it was. `Delete` writes one `playlist/delete` with `playlistId` `PLhype`, takes the card off the page and shows `Deleted “Hype”`. The menu on a playlist page offers the same entry, and deleting the playlist that page shows leaves for the library.
- **Refused.** `resetFakes()`, `refuseWrites.Store(true)`, then click `Save to library`. Verified: the button still reads `Save to library`.
- **Sign in.** Mirror `TestSignInWithABrowserWindow` and `TestFailedBrowserImportOffersPastingTheCookieHeader`. A window shorter than the dialog scrolls it (`TestSignInDialogScrollsInAShortWindow`): the panel carries the label `dialog-panel`, and a text the panel clips keeps that label with a zero-height box until scrolling brings it into the panel.

## Gotchas

- A signed-out app shows no like, save or subscribe buttons. Prove that too (`TestSignedOutHasNoLike`).
- The fake accepts any `SAPISID=test` cookie. It says nothing about real YouTube auth. See [native-only.md](./native-only.md).
- Counters and `lastWrite` are process globals. Do not use `t.Parallel` in a proof that reads them.
- Use the counters, `written()`, `likeWrite()` and `subscriptionWrite()` as the second view of a write. A button label alone does not prove the request was sent.
- Saving an album and liking a track both count in `likeWrites`. Reset or compare a delta.

- **Large playlists.** `/playlist/VLPL_paginated` supplies 100 initial songs, a song-shelf continuation with 46 more, and a separate suggestions continuation. `TestAPlaylistLoadsSongsFromItsShelfBeforeSuggestions` checks all 146 playable songs, their last ID, and suggestions kept separate.
