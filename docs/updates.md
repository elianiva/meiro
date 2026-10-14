# Signed auto-updates

Meiro updates itself from signed releases. The app already contains the
update window (the updater's native UI) and the Updates card in Settings;
this note is what turns them on for a release.

MyGo signs each platform build with a key pair, and the app checks new
downloads against the public key baked into it. Without the key, `mygo
build` skips the update files and the Updates card says the copy updates
another way.

## One-time setup

Create the key pair:

```sh
go tool mygo keygen
```

It writes `mygo-update.key` (secret) and `mygo-update.pub` (public) to
`mygo/update-keys` in the configuration directory. Keep the secret key out
of the repository: in a password manager and in CI secrets. Losing it
strands installed apps; leaking it lets anyone ship code to users.

Point `mygo.json` at the public key and where releases are published:

```json
{
  "version": "0.1.0",
  "updates": {
    "publicKey": "…the contents of mygo-update.pub…",
    "github": "elianiva/meiro",
    "tagPrefix": "desktop-v"
  }
}
```

`tagPrefix` keeps desktop releases apart from other releases in the same
repository. Desktop releases are tagged `desktop-v0.1.0` and must not be
the repository's latest release; the app finds the newest tag with the
prefix through the GitHub API instead.

## Releasing

1. Add a `## 0.2.0` section to [`CHANGELOG.md`](../CHANGELOG.md). It
   becomes the release notes of the update window.
2. Build and upload:

   ```sh
   bazel run //:dist -- darwin-universal
   go tool mygo build -upload
   ```

   This signs the archives, writes delta updates from the last versions,
   and uploads everything with the installers to a draft release.
3. Publish the release without marking it latest:

   ```sh
   gh release edit desktop-v0.2.0 --draft=false --latest=false
   ```

Apps check once a day, show the notes, and offer to install, skip, or be
reminded later. The Updates card also offers automatic background
installation. Development builds (`go run`, `mygo dev`) and package
installs never check; the card says why.

## Deep links

The `meiro` URL scheme in `mygo.json` is registered with the system by the
installers: `meiro://play?v=VIDEO_ID` starts a video,
`meiro://search?q=QUERY` opens the search page. A second launch hands its
links to the running app through the single-instance lock.
