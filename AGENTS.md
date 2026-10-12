# Working on Meiro

This file is for anyone changing the code, person or agent. It lists the
conventions the code keeps and the tools that enforce them. Most rules here
are checked by the compiler, `go vet` or golangci-lint (see `.golangci.yml`).
The ones that are not are marked *(review)*.

For MyGo itself (UI lifetime, element identity, threading, `ui.NewTester`),
read `.agents/skills/mygo-maintenance/SKILL.md` first.

## Checks

```sh
just test                 # go vet ./... and go test -race ./...
golangci-lint run ./...   # the stricter rules below
go tool gofumpt -l .      # formatting, also checked in CI
```

## Where code goes

```
main package    the app: its state, its pages and the views that draw them
youtube/        the InnerTube client; imports nothing of the app
player/         plays one stream through ffmpeg and the audio device
systemmedia/    MPRIS and macOS Now Playing
m3/             Material 3 components and the theme
internal/
  audio/        audio URL cache (URLs), on-disk audio cache (Cache), Probe
  artwork/      downloads, decodes and keeps artwork within a memory budget
  cookies/      browser session import, the pasted Cookie header, the store
  history/      listening history, and the stats summary built from it
  settings/     the user's settings, and the debounced Writer
  ytdlp/        running yt-dlp: resolving audio URLs, reading browser cookies
  proc/         finding bundled tools, hardened subprocesses, private temp files
  fsutil/       atomic writes
  lru/          a generic least-recently-used map
```

Anything that can work without the app's state belongs under `internal/`,
with its own tests. depguard keeps the layers apart: `youtube` imports no
`internal` package, and only `main`, `m3` and `internal/artwork` import the
UI toolkit.

## The main thread owns the app

`app` belongs to the main thread. Views read and change it there. Work off
the main thread hands its result back in one of three ways:

| The result... | Use |
| --- | --- |
| can go stale (a newer request or a navigation replaces it) | `startTask` / `startProgress` on a `task` field |
| cannot go stale (a write the user asked for) | `spawn` |
| is not needed by the UI at all (warming a cache, writing a file) | `a.run` |

Never compare generation numbers by hand and never write
`a.run(func() { ...; a.update(...) })`. A `task` cancels the work it
replaces and drops its result unseen, so a stale answer cannot reach the
screen. `task.busy()` is the "loading" flag; do not add a separate bool.
*(review)*

A `task` must not be copied: `go vet` reports it. To reset a struct that
holds one, reset its other fields and `stop()` the task.

Do not start goroutines in the main package outside `app.every`,
`app.run` and the task helpers. *(review)*

## Pages are routes

Every page is one entry in `routes` (`routes.go`): its pattern, history
title, top-bar heading, rail destination, queue name, loader, the work to
start when it opens, its view, and whether it needs an account. To add a page, add a `routeID` and a route.
Nothing else switches on paths. `exhaustive` points at every switch over
`routeID` that must learn about the new page.

Use `a.route()` to ask which page is shown (it is cached per path), and
`routeByID(id).path(...)` or `targetPath(target)` to build a path. Do not
compare `a.router.Path()` against string literals. *(review)*

## Types carry the rules

- **What an item opens** is `item.Target()` (`youtube.TargetKind`), and
  `item.IsTrack()` / `item.IsVideo()` / `item.ArtistID()`. Do not sniff ID
  prefixes outside `youtube/item.go`.
- **The queue** is a `queue`, changed only through its methods, which keep
  the current place, the play-next mark and the boundary of the generated
  tracks consistent. Read `a.queue.items`; do not write it. *(review)*
- **The account** is `a.account.session`: nil means signed out. There is no
  separate signed-in flag, cookie or client to keep in step. The page reload
  that follows a change goes through `useSession` / `endSession`.
- **Dialogs** that write are pointers, nil while closed. A late answer
  checks `a.newPlaylist == d`, which is identity, not a counter.
- **Enums** (`routeID`, `rowKind`, `panelTab`, `repeatMode`,
  `systemmedia.Loop`, `deepLinkAction`, …) are typed. Switches over them
  list every value (`exhaustive`, with `default` not counting).
- **The desktop** is `a.desktop`, nil in tests. Its methods do nothing on
  nil. App code calls them and never checks for nil itself.

## Views

- Building a view does no I/O and starts no work. Loads start from
  navigation (a route's `load` or `open`) and from handled clicks. See the
  MyGo skill. The one exception is artwork: `thumbs.Bitmap` fetches the
  pictures a frame draws, within its budget, and wakes the view when they
  land.
- The page's rows are rebuilt only when `page.rowsDirty` is set or
  `rowsKey` changes. Set `rowsDirty` whenever you change what a page loaded.
- Each `pageData` is owned by exactly one place, the page shown or the page
  cache, so its slices are reused in place. Never keep a page's rows or
  playable tracks past the frame. *(review)*

## Tests

- Drive the app the way a user does: `newTestApp()` and `ui.NewTester`,
  clicking by label. The fake YouTube (`harness_test.go`) and `noArtwork`
  keep tests off the network. Keep it that way.
- Background work runs inline in tests (`a.run`), and `a.sleep` returns at
  once, so a frame sees the result of the click before it.
- Logic with rules of its own (the queue, the LRU, the caches, settings)
  also gets direct unit tests in its package.

## Performance

`bench_test.go` measures whole frames of large pages (500-track album,
600-item library, a 500-track queue), the rows rebuild, and cached
navigation. Run it before and after a change that touches a view or the
page model:

```sh
go test -run '^$' -bench . -benchmem -count 10 . > new.txt
benchstat old.txt new.txt
```

A frame should not allocate per item of a page. Only the rows in view are
built.
