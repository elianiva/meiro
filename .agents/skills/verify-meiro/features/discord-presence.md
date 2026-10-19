# Discord Rich Presence

When enabled and Discord is running, Meiro shares the current track, artist,
cover, elapsed time and track length. Paused or stopped playback clears the
activity. The feature is off by default.

## Sub-features

- `discord-presence-settings` saves the opt-in switch. Meiro supplies its application ID in the app build.
- `discord-presence-playback` publishes the current track, artist, artwork, and start and end timestamps. Discord draws a time bar from them; the end is left out while the length is unknown.
- `discord-presence-clear` clears the activity on pause, stop, disable or app exit.
- `discord-presence-reconnect` retries when Discord is not running or its IPC connection closes.

## How to get to it (user POV)

- Open Settings → Desktop.
- Enable `Discord Rich Presence`.
- Play a track while the Discord desktop client is running.

## Driving it with the headless tester

Preconditions:

- Use `newTestApp(t)` and navigate to `/settings`.
- The RPC protocol test uses an in-memory local pipe. It does not need Discord, credentials or network access.

- **Settings.** Enable `Discord Rich Presence`. The switch persists in `settings.json`; users do not enter an application ID.
- **Activity and keepalive.** `go test ./internal/discordrpc` runs the RPC handshake, verifies the `SET_ACTIVITY` payload, checks that the end timestamp is added once the track length arrives, confirms pause sends a null activity, and checks that an idle session answers Discord's PING with PONG.
- **Native Discord.** Meiro's public application ID is bundled in `main.go`. Run Meiro and Discord on the same desktop, enable the setting, and play a public track. Confirm Discord shows the track, artist and a time bar ending at the track's length, then pause playback and confirm the activity clears.

## Gotchas

- Discord must be running on the same machine. The activity is opt-in because it shares the track and artwork with Discord.
- The Discord Application ID is public and bundled in Meiro. Users do not need their own Discord app or ID. Discord may restrict an unapproved app to its listed testers.
- The IPC path differs by platform: Unix-domain sockets on macOS and Linux, named pipes on Windows.
