# Native-only behavior

These behaviors need a real window, device, account or network. The headless tester cannot show them. The orb Desktop covers some of them (see below). A change that touches one reports it as unverified unless it was run where the dependency exists.

## Sub-features

- `native-window` covers the OS window, title bar, vibrancy, tray, and the mini player window.
- `native-notch` covers macOS camera cutout placement, rounded window clipping and transparency during resizing, hit testing after the closing animation releases its larger canvas, keyboard focus, menu-bar level, fullscreen/Spaces, Dock reactivation while only the notch is visible, and connecting or disconnecting displays. A physical notched MacBook is needed.
- `native-audio` covers the real sound card, device selection, and volume.
- `native-media-keys` covers MPRIS on Linux and Now Playing on macOS (`systemmedia/`).
- `native-notifications` covers Windows track notifications and their cover icons.
- `native-youtube` covers real InnerTube responses, real sign-in, and region or age blocks.
- `native-ytdlp` covers resolving real audio URLs and reading browser cookies.
- `native-deeplink` covers `meiro://` links and single instance behavior.
- `native-update` covers the updater and release packaging.

## How to get to it (user POV)

- In an Amp orb, run `scripts/doctor.sh`; it attempts `amp orb desktop ensure` and checks the tools `.agents/setup` installs. Then run `scripts/native.sh start` and inspect the real native window. Do not treat the initial absence of a Desktop environment file as proof that native verification is unavailable.
- On a laptop, run `bazel run //:dev` (live reload) with `ffmpeg` and `yt-dlp` on `PATH`.
- On Windows, enable track notifications in Settings → Desktop, then play a track and inspect the notification's cover icon.
- Install a release build from `scripts/package-linux.sh` or the release workflow.

## Verification boundaries

- **Orb Desktop path.** `native-window` (real labwc window, fullscreen), `native-audio` (a `Meiro` stream on the Desktop null sink with position advancing), signed-out live `native-youtube`, and `native-ytdlp` resolving a real stream can be checked there. Search, click, inspect screenshots, and use `pactl list sink-inputs` for audio evidence.
- **Windows notifications.** The Windows cross-build type-checks the notification icon path, but only a Windows run can confirm the shell displays cover art rather than a white square.
- **Known limits; report rather than rediscover.** MPRIS cannot be verified in this orb because it has no user D-Bus session. Never sign in with a real account. Brand-channel switching/identity and playback-history reporting therefore remain fake-session tests here; say that fake tests cover the code/request path but no real-account flow was exercised. Do not imply otherwise.
- **Not verifiable on this orb.** The tray icon is unavailable in the orb Desktop, so neither the wordmark nor O mark appearance can be checked here. Also not verifiable: macOS Now Playing, deep links on another OS, the updater, macOS and Windows rendering, and real sound-card output.
- **No native screen recorder is installed.** `scripts/native.sh capture-loop [interval-seconds] [duration-seconds] [name]` is the standard timed screenshot fallback and writes timestamps alongside PNG frames. Successive `shot` captures with step notes are also fine. Do not stitch the stills into an MP4 or call it a real recording.

- **What does run.** `systemmedia` has its own unit tests. `go test ./systemmedia/ ./player/ ./internal/...` exercises them without a device.
- **Notch Open Meiro.** On a notched Mac, hide the main window, expand the notch and click Open Meiro; repeat with the window minimized. Verify the existing window is restored and focused while the notch animates back to compact size and playback continues. After closing, the notch must release its larger hit area and leave keyboard focus on the main window. Headless tests cannot prove these OS effects.
- **Notch cover opens Now Playing.** Repeat the hidden/minimized checks using the large cover. The main window must redraw immediately on Now Playing, including when already visible, and keep focus after the notch closes. Verify the hand cursor, tooltip and keyboard focus ring on the cover. Headless tests verify the view and playback state, but cannot prove native focus, GPU rendering or hit testing across windows.
- **Notch pointer departure.** Move the pointer out of the native panel and wait two seconds, including while playback is paused. Check re-entry cancels the countdown, progress dragging outside keeps the panel open until release, and switching focus away after keyboard use starts a new delay. Verify the animated close releases its larger hit area without stealing focus. Native mouse-exit delivery, cross-window drag capture and display relocation require a notched Mac.
- **Cross-build.** `bazel build --config=darwin-arm64 //...` and the other `--config` targets in `.bazelrc` type-check a platform you cannot run.
- **Everything else.** Write a short checklist in the PR, with the OS, what you ran and what you saw. Do not claim it from the headless proof.

## Gotchas

- The headless window has no title bar and draws with the software renderer. Layout is real. GPU, DPI and font fallback on other platforms are not.
- Never use a personal YouTube account or cookies in a proof, or put them in an artifact.
- The Desktop output is software rendered and its audio is a null sink. Nobody hears it. The tray icon is unavailable there, and the app logs that and goes on.
- Do not change the Desktop's output mode. It crashes the stream the user watches.
