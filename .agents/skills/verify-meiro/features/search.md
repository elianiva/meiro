# Search

A user types in the search bar, picks a suggestion or presses Enter, filters results, and scrolls to load more.

## Sub-features

- `search-suggest` shows suggestions and matching items while typing, with no search run.
- `search-run` shows results for a chosen suggestion or Enter, and remembers the query.
- `search-filter` narrows results with the `Songs` and `Albums` chips and `Clear filters`.
- `search-more` loads the next page when the list reaches its end.
- `search-menu` offers the item menu on right click.
- `search-elsewhere` starts a search from a page that is not `/search`.
- `search-episode-result` plays an episode top result whose page link carries the video ID.

## How to get to it (user POV)

- Click `Search` in the rail, then click the bar (`searchPlaceholder`).
- Press `Cmd+K` or `Cmd+F`.
- Type in the bar of another page.
- Click **Play** on an episode result to start that episode.

## Driving it with the headless tester

Preconditions:

- `searches.Store(0)` so the request counters start clean.
- `a.router.Push("/search")`, `tt := ui.NewTester(a.view, 1000, 700)`.

- **Suggest.** `p.click(searchPlaceholder, "focus")`, `tt.Type("yor")`, `p.step("typed")`. `Yorushika songs` shows and `searches.Load()` is `0`.
- **Run.** `p.click("Yorushika songs", "results")`. `Search Result Song` shows, `searches.Load()` is `1`, and `a.settings.Recent[0]` is `Yorushika songs`.
- **Filter.** Verified. `p.click("Songs", "songs-only")`. `searchBody()` contains `"params"`. `p.click("Clear filters", "cleared")`.
- **Next page.** Mirror `TestSearchLoadsTheNextPageItself`. `searchesMore.Load()` becomes `1` with no click.
- **Menu.** `tt.RightClick("Suggested Song")` and `tt.Menu()` lists the item actions.
- **Episode result.** `TestSearchEpisodeTopResultPlays` searches for `play episode result`, clicks **Play**, and checks that the resolver receives `UZ83quirqLA` from the episode page ID `MPEDUZ83quirqLA`.

## Gotchas

- Typing alone must not run a search. Assert the counter before Enter or a click.
- Settings has a separate `Search settings` button that expands into a local filter; it does not navigate to music search. Stats has no search entry point.
- `searchBody()` is the last request body. Read it right after the action.
- Results for an older query must not replace newer ones. Cover it with two queries in a row when you change the search task.
