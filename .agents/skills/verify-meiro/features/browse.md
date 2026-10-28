# Browse pages

A user opens the pages in the rail, follows an item into an album, playlist or artist page, and goes back to where they were.

## Sub-features

- `browse-pages` draws every route with its seeded content.
- `browse-open` follows an item from Home into its page.
- `browse-back` restores the scroll position of the page left.
- `browse-album-cover` loads an album's own artwork when its header is nested in the primary tab, including navigation from the player's album credit.
- `browse-new-route` covers a page added to `routes` in `routes.go`.

## How to get to it (user POV)

- Click `Home`, `Explore`, `Library` or `Settings` in the rail.
- Press `Cmd+1`, `Cmd+2`, `Cmd+3` or `Cmd+,`.
- Click an album, playlist or artist card on any page.
- Open a `meiro://` link (see [native-only.md](./native-only.md)).

## Driving it with the headless tester

Preconditions:

- `scripts/doctor.sh` passes.
- Start from `a := newTestApp(t)`, `a.router.Push("/home")`, `tt := ui.NewTester(a.view, 1000, 700)`.

- **Every page.** Push each path in `TestEveryPageLoadsAndDraws` and call `p.step`. `Home` shows `Quick picks` and `Ambient One`. `/explore` shows `Moods` and `Night Drive`. `/album/MPREb_test` shows `Album Track One`.
- **Open an item.** From `/home`, `p.click("Deep Focus", "album")`. `a.router.Path()` starts with `/album/` and `Album Track One` shows.
- **Show more.** On Home, click `Show more`. The next sections appear below the existing feed, and the request count increases once.
- **Go back.** Verified for album: click `Deep Focus` on `/home`, then `a.router.Back()` and `p.step("back")`. `Quick picks` shows again. For scroll position, mirror `TestGoingBackRestoresWhereAPageWasScrolled`, which uses `/settings`, `tt.Scroll(500, 300, 0, 1500)` and `a.list.Visible()`.
- **New route.** Add the route's path to the table in `TestEveryPageLoadsAndDraws` style, push it, and show its heading in the screenshot.
- **Album cover from a credit.** Use `tonesApp(t, 600)`, return `r.file` from `r.url`, and supply `redArt` through `a.thumbs` (close both caches). Open `/playlist/VLPL_album_cover`, click `Linked Track`, then `Go to album: After Hours` from either the bar or Now Playing. The route is `/album/MPREb_nested`, the page shows `Album Track One`, and its artwork is `https://img.test/album.png`. Wait for the center of `More options for After Hours` to turn red; do not prefetch the artwork in the test. See `TestAlbumCoverLoadsFromPlayerMetadata`. As a control, open `Deep Focus` from Home: its legacy header still loads.

## Gotchas

- `/library` and `/recap` need `signedInApp(t)`. Signed out they show a sign-in prompt, which is also worth a screenshot.
- Compare the heading, not the path. A route that failed to match draws the `Not found` page at a valid-looking path.
- `a.router.Push` is the test's shortcut for navigation. Also click one real entry point per proof.
- Current album responses put `musicResponsiveHeaderRenderer` in the primary tab's section list. Its `thumbnail` is the album cover; `straplineThumbnail` is an artist portrait. Never fill missing page art from tracks or related albums.
