# Discord Rich Presence

When enabled and Discord is running, Meiro shares the current track, artist,
cover, elapsed time and track length. Paused or stopped playback clears the
activity. The feature is off by default.

## Sub-features

- `discord-presence-settings` saves the opt-in switch. Meiro supplies its application ID in the app build.
- `discord-presence-playback` publishes the current track, artist, artwork, and start and end timestamps. Discord draws a time bar from them; the end is left out while the length is unknown. The member list shows every credited artist, joined with commas, and falls back to the track's byline when the item credits none.
- `discord-presence-clear` clears the activity on pause, stop, disable or app exit.
- `discord-presence-reconnect` retries when Discord is not running or its IPC connection closes.
- `discord-presence-client-discovery` finds standard IPC sockets for native clients, Flatpak app exports and per-app runtime paths, and Snap runtime paths without a fixed client-ID list.

## How to get to it (user POV)

- Open Settings → Desktop.
- Enable `Discord Rich Presence`.
- Play a track while a Discord desktop client with RPC enabled is running.

## Driving it with the headless tester

Preconditions:

- Use `newTestApp(t)` and navigate to `/settings`.
- The RPC protocol test uses an in-memory local pipe. It does not need Discord, credentials or network access.

- **Settings.** Enable `Discord Rich Presence`. The switch persists in `settings.json`; users do not enter an application ID.
- **Linux client sockets.** Discord uses `discord-ipc-N` sockets. Meiro checks the standard runtime path, Flatpak's `app/<id>` and `.flatpak/<id>/xdg-run` paths, and Snap's `snap.*` runtime paths. IDs are discovered from the runtime directory so new client IDs do not need code changes. Native package formats, including RPMs and AppImages, use the standard runtime socket.
- **Settings switch.** `tt.Click` on the label misses the switch, because the row label and the switch are separate elements. Find the row with `tt.Find("Discord Rich Presence")` and click `row.X+row.W+20` at the row's middle height. Read `a.settings.DiscordPresence` and the saved `settings.json` (`"discordPresence": true`, indented) as the second view.
- **Activity and keepalive.** `go test ./internal/discordrpc` runs the RPC handshake, verifies the `SET_ACTIVITY` payload, checks that the end timestamp is added once the track length arrives, confirms pause sends a null activity, and checks that an idle session answers Discord's PING with PONG.
- **Native Discord client.** Meiro's public application ID is bundled in `main.go`. Run Meiro and a client with RPC enabled on the same desktop, enable the setting, and play a public track. Confirm the client shows the track, artist and a time bar ending at the track's length, and that the member list reads the artist, then pause playback and confirm the activity clears.

## Gotchas

- An RPC-capable client must be running on the same machine. The activity is opt-in because it shares the track and artwork.
- The Discord Application ID is public and bundled in Meiro. Users do not need their own Discord app or ID. Discord may restrict an unapproved app to its listed testers.
- The IPC path differs by platform: Unix-domain sockets on macOS and Linux, named pipes on Windows.
- Flatpak socket locations include `$XDG_RUNTIME_DIR/app/<id>/discord-ipc-N` and `$XDG_RUNTIME_DIR/.flatpak/<id>/xdg-run/discord-ipc-N`; Snap clients can use `$XDG_RUNTIME_DIR/snap.<name>/discord-ipc-N`. Meiro discovers these paths by directory, not by a list of client names. A stale socket path can remain after a client stops; a successful socket connection alone does not prove that the client displayed the activity.
- Custom clients may require their own RPC setting (for example, Vesktop's **Enable Rich Presence via arRPC**) and Discord's **Activity Privacy → Share your detected activities with others** setting. Discord can accept `SET_ACTIVITY` while hiding the card when activity sharing is off; its `renderer_js.log` shows `[RPCServer:IPC] Socket Opened: N 1561435721456296087`, then `Socket Emit` and `Socket Message`. Some client-side RPC implementations have bugs that Meiro cannot fix.
