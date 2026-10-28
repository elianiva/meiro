# macOS notch player

The camera housing has the current cover on its left and a decorative playback
indicator on its right. Either side opens a compact, rounded panel with cover
art on the left, track information and controls in the middle, and lyrics on
the right. The previous and next lines frame the active lyric.

## Sub-features

- `notch-compact`: cover and five animated bars, resting on pause, with a loading indicator while buffering.
- `notch-lyrics`: click either wing to expand/collapse; Escape collapses. The panel stays open when the pointer leaves.
- `notch-motion`: opening expands over 240 ms and closing contracts over 200 ms, anchored to the camera. Content fades and lifts slightly; the header cover crossfades with the collapse arrow. Rapid clicks reverse the current animation. Reduce Motion uses a 100 ms fade in place. Fading controls cannot be activated.
- `notch-transport`: Previous, Play/Pause and Next operate playback without collapsing the panel. Controls stay available while lyrics load or are unavailable.
- `notch-shape`: a 480 × 190 point expanded panel for a 180 × 32 point cutout, with 20 point corners and transparent pixels outside them. The native window clips to the same radius. A 76 point cover, single-line track metadata and two-line active lyrics stay in separate columns.
- `notch-progress`: the thin position slider shows elapsed and total time. Pointer release or keyboard changes seek; another player window cannot end its drag. Unknown duration disables seeking.
- `notch-sync`: line selection follows playback position and seeks; changing songs supersedes pending lyrics.
- `notch-fallback`: plain lyrics scroll independently; missing lyrics and failed requests have separate states, with Retry for failures.
- `notch-settings`: Settings > Desktop > Notch player persists, enabled by default. Only macOS shows the setting.
- `notch-only-desktop`: macOS has no track banners or menu bar icon, including with legacy settings. Settings offers Keep running when closed; hiding the main window does not turn the tray back on, and the Dock reopens the main window even while the notch is visible.
- `notch-artwork`: the active track's thumbnail is shown; a late cover for an older track cannot replace it. Missing covers use a neutral music-note placeholder.
- `notch-native`: only a built-in screen with an AppKit camera cutout gets a panel. Display changes reposition it or remove it.

## How to get to it (user POV)

On a MacBook with a notch, play a song. Click the cover or playback indicator
beside the camera. Use the three buttons below the track information to control
playback, or drag the progress bar to seek. Click the upper collapse arrow or
indicator, or press Escape, to collapse. The player remains
available when the main window is hidden with Keep running when closed. Reopen
the main window from the Dock. Search Settings for
`notch` to turn it off or back on.

## Headless proof recipe

Use `notchTestApp(t)` for a restored paused queue with a 180 × 32 point camera
cutout. Build `ui.NewTester(a.notchView, 284, 38)`. Click `Open notch lyrics`,
then resize the tester to `a.notch.bounds()` (480 × 190) and advance its clock
for the reveal. Supply timestamped lines through `a.lyricsSource`.

Drive `Position` in the mini player with Home/End; assert that the notch shows
the corresponding lines without changing the main lyric scroll, tab or open
state. Click the notch's `Next` and `Previous`, check the song's lyrics and
assert that the panel stays open. Defer background work to
prove an obsolete lookup cannot replace the current song. Test plain, missing
and failed lyrics; click `Retry lyrics` for the latter. Click `Show notch player`
and reload its saved file. Play an album track backed by a generated tone,
then pause and resume from the notch to prove that its controls affect real
playback and that the indicator animates only while playback is active.
Assert transparent pixels at all four corners after applying the theme.
Assert that long title and artist names stay between the cover and lyrics,
and that long lyrics never overlap transport controls. Drive `Notch position`
with Home/End and a pointer drag, check the playback position and displayed
time, and render the mini player during the drag to prove it stays independent.
Check the three-line lyric context after seeking and that reduced motion keeps
the same content and layout without requiring an animation to finish.

For motion, advance the tester clock through intermediate frames when opening
and closing, and record both geometry and pixels. The black camera housing
must stay opaque, the panel stays centred at the screen edge, and the content
fades without reflowing. Reverse a close through the indicator, then close
again with Escape. The native bounds stay expanded until closing finishes,
then return to 284 × 38; transport controls are absent after completion and
disabled during the fade. Repeat with Reduce Motion: geometry does not
interpolate, content stays in place, and the final footprint shrinks only after
the fade. Disabling the notch while it animates must remove it immediately.

For desktop cleanup, load a legacy settings file with `showTray` and
`notifyTrack` enabled. Search Settings for `desktop`; the old notification and
tray controls must be absent on macOS. Click `Keep running in background` and
read the saved file: background operation is enabled, tray and banners are not.
Other platforms retain their desktop controls. For cover identity, delay the
first track's artwork, click Next, let the new artwork arrive, then release the
old response and assert that the new cover stays. Next to a track with no cover
must show the neutral placeholder.

## Gotchas

- Tests render the exact native UI but cannot prove window placement, menu-bar level, focus, Spaces/fullscreen, or screen hotplug. These require macOS and a physical notched display.
- Camera dimensions come from AppKit in logical points, not bitmap pixels or a hard-coded MacBook size. Test negative monitor origins too.
- The notch shares fetched lyrics, but not the full player's scroll or Synced/Static presentation preference.
- No microphone, audio capture, or FFT is used. The bars are a decorative playback indicator and respect Reduce Motion.
- `m3.Provide` resets the root background. Set it transparent after providing the theme, or a rectangular backing remains behind the rounded panel.
- Expanded mode replaces the small header cover with a collapse arrow; artwork is displayed once, below the camera. It requests a larger thumbnail for Retina rendering.
- Synced rows use lyric indices inside the track's identity for position transitions, so a new song cannot inherit the previous song's animation state.
- The native window provides an expanded transparent canvas during motion. UI animation changes the painted rounded panel, and a guarded completion resizes the native window to its compact footprint. The callback reads the latest target so rapid reversals cannot be closed by an old completion.
- `AnimateTo` respects Reduce Motion by snapping geometry; `AnimateColor` preserves the short opacity fade. The panel background itself never fades.
