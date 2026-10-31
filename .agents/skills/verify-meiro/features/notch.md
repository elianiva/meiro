# macOS notch player

The camera housing has the current cover on its left and a decorative playback
indicator on its right. Either side opens a compact, rounded panel with cover
art on the left, track information and controls in the middle, and lyrics on
the right. The previous and next lines frame the active lyric.

## Sub-features

- `notch-compact`: cover and five animated bars, resting on pause, with a loading indicator while buffering.
- `notch-clicks`: either compact wing accepts a full press/release, including a brief hold while Meiro gains focus. Releasing over the camera gap cancels the click and gives focus back. Repeated expand/collapse cycles remain clickable.
- `notch-cover-open`: the large cover in the expanded panel opens the current song's Now Playing view in the main window and collapses the notch. It preserves playback and paused state, uses a pointer and Open Now Playing tooltip, supports keyboard activation, and is disabled while fading. The small header cover still expands the notch.
- `notch-lyrics`: click either wing to expand/collapse; Escape collapses.
- `notch-auto-collapse`: after the pointer leaves the expanded panel for two seconds, it closes with the existing animation. Returning inside cancels the countdown. Hovering gaps or children counts as inside; dragging progress outside and keyboard focus on controls keep it open. Releasing the drag or moving keyboard focus out starts a fresh delay. Reopening, disabling or relocating the notch clears old deadlines.
- `notch-open`: the expanded header has a 16 point Open Meiro icon in a 32 point target beside collapse, outside the camera area. It brings the existing main window forward, restoring it if hidden or minimized, and collapses the notch through its normal closing animation while playback continues. It fades with the panel and is disabled while fading; compact mode has no extra button.
- `notch-motion`: opening expands over 240 ms and closing contracts over 200 ms, anchored to the camera. Content fades and lifts slightly; the header cover crossfades with the collapse arrow. Rapid clicks reverse the current animation. Reduce Motion uses a 100 ms fade in place. Fading controls cannot be activated.
- `notch-transport`: Previous, Play/Pause and Next operate playback without collapsing the panel. Controls stay available while lyrics load or are unavailable.
- `notch-shape`: a 480 × 190 point expanded panel for a 180 × 32 point cutout, with 20 point corners and transparent pixels outside them. The native window clips to the same radius. A 76 point cover, single-line track metadata and two-line active lyrics stay in separate columns.
- `notch-progress`: the thin position slider shows elapsed and total time. Pointer release or keyboard changes seek; another player window cannot end its drag. Unknown duration disables seeking.
- `notch-sync`: line selection follows playback position and seeks; changing songs supersedes pending lyrics.
- `notch-fallback`: plain lyrics scroll independently; missing lyrics and failed requests have separate states, with Retry for failures.
- `notch-settings`: Settings > Desktop > Notch player persists, enabled by default. Only macOS shows the setting. Turning it off immediately switches to the menu bar icon and the existing mini player; turning it back on removes that icon when a notched display is available. Editing `showNotch` in settings.json switches the same way without a restart.
- `notch-desktop-choice`: macOS uses the menu bar mini player when the notch is disabled or no built-in notched display is available. Display loss/reconnection swaps the player without changing the saved notch preference, queue, playback or explicit background choice. The player choice does not depend on whether a track is loaded. Legacy `showTray` cannot hide the fallback or duplicate an enabled notch. Track banners stay off. Other platforms retain their tray preference. Settings offers Keep running when closed; the Dock can also reopen the main window.
- `notch-artwork`: the active track's thumbnail is shown; a late cover for an older track cannot replace it. Missing covers use a neutral music-note placeholder.
- `notch-native`: only a built-in screen with an AppKit camera cutout gets a panel. Display changes reposition it or remove it.
- `notch-menu-bar`: the compact panel does not retain keyboard focus. Expanding opts in for Tab, seeking and Escape; collapsing or clicking outside releases native focus without swallowing the click. Wi-Fi, battery and date controls outside the panel remain usable.
- `notch-reopen`: with no saved close preference, an enabled notch player on a supported Mac makes closing the main window hide it. Open Meiro and the cover restore that same window; playback and the queue survive repeated close/reopen cycles. An explicit background preference wins. Turning background operation off makes close quit the whole app, including the notch.

## How to get to it (user POV)

On a MacBook with a notch, play a song. Click the cover or playback indicator
beside the camera. Use the three buttons below the track information to control
playback, or drag the progress bar to seek. Click the upper collapse arrow or
indicator, or press Escape, to collapse. The player remains
available when the main window is hidden with Keep running when closed. Reopen
the main window from the Open Meiro icon beside collapse or from the Dock. Search Settings for
`notch` to switch between the notch and menu bar mini player. With the notch
enabled but its display unavailable, Settings explains that the menu bar is
active until the display returns.
Click the large cover inside the expanded notch to open the current song in
Now Playing directly; the notch collapses as the main window comes forward.
Move the pointer away for two seconds to collapse automatically, or return
inside before the delay ends to keep it open. Keyboard focus and progress
dragging hold it open until the interaction ends.

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

For desktop choice, search Settings for `notch`, expand the panel and turn
`Show notch player` off. Verify the panel closes, Settings describes the menu
bar mini player, and the saved preference survives a fresh app. Turn it back
on and verify the compact player returns. Simulate display notifications via
`setNotchCutout`: losing the cutout selects the menu bar and clears expansion;
restoring it at a different origin selects the compact notch without rewriting
settings.json. `TestNotchDisplayChangesRestoreTheMenuBarWithoutSaving` covers
this transition and the paused queue. `TestNotchFallbackKeepsAudioAndMiniPlayerControls`
plays real generated audio, switches through Settings and display changes,
and pauses/resumes from the fallback mini player without resolving the song again.
`TestSettingsFileChangesSwitchTheMacDesktopPlayer` proves that editing only
`showNotch` also clears an expanded panel and switches the player immediately.
Use the previous commit for the before/after proof. Headless tests check the
chosen desktop player and its content; they cannot prove native tray creation,
popover anchoring or real lid/display notifications.

For desktop cleanup, load a legacy settings file with `showTray` and
`notifyTrack` enabled. Search Settings for `desktop`; the separate notification
and tray switches must be absent on macOS. Click `Keep running in background`
and read the saved file: background operation is enabled and banners remain
off. The menu bar follows the notch/display choice, not the stored `showTray`.
Other platforms retain their desktop controls. For cover identity, delay the
first track's artwork, click Next, let the new artwork arrive, then release the
old response and assert that the new cover stays. Next to a track with no cover
must show the neutral placeholder.

For Open Meiro, verify the compact notch has no extra button, expand, and
check the new target stays left of collapse without overlapping the camera.
Repeat with a wider cutout. Seek, then click Open Meiro and activate it with
Tab/Enter: the notch starts closing, keeps its native canvas until the animation
finishes, then returns to compact size. The track, position, paused state and
main page stay unchanged, and lyrics are not fetched again. Repeat with Reduce
Motion to verify the fade without resizing mid-transition. Reopen through the
indicator; the button and cached lyrics return. Capture the hover tooltip as well.
Use the preceding notch commit as the before reference: a base without the
notch cannot run this scenario. The headless desktop is nil, so only a native
macOS check can prove that the main window actually appears and takes focus.
For auto-collapse, use `TestNotchOpenMeiroCollapsesWithoutChangingPlayback` and
compare the proof with the preceding commit where Open Meiro left the panel open.

For pointer departure, use `TestNotchCollapsesTwoSecondsAfterPointerLeaves`:
expand, move inside a gap and wait five seconds (still open), move outside to
`(500, 230)` and wait 1.9 seconds (still open), then return inside to cancel.
Leave again for two seconds; the existing close animation must run before the
native footprint becomes compact. Repeat with Reduce Motion. Test dragging
the progress slider outside for longer than two seconds: it must not seek or
close until release. Focus the slider with Tab, move the pointer out, and keep
the panel open until `SetFocused(false)`, then wait a fresh two seconds.
`TestNotchAutoCollapseWaitsForDragAndKeyboard` and
`TestNotchReopeningDiscardsAutoCollapseDeadline` cover interaction and stale
wakeups. Use the preceding commit as the proof baseline.

For the cover action, click the compact cover first: only the notch expands.
Click the large cover (`Open Now Playing`) or focus it with Tab and press Enter.
The main window must show Now Playing with the current track and its controls,
even if it previously showed only the queue. Check that playback position,
queue, track and paused state are preserved, and the notch finishes collapsing.
Repeat while playing real generated audio, with Reduce Motion, and after closing
and reopening the panel. Clicking during either fade must do nothing. The plain
Open Meiro icon remains a control that restores the main page without opening
Now Playing. Use `TestNotchCoverOpensNowPlaying` and
`TestNotchCoverKeepsPlayingAudio`, and compare the proof with the previous commit.

## Gotchas

- Tests render the exact native UI but cannot prove window placement, menu-bar level, focus, Spaces/fullscreen, or screen hotplug. These require macOS and a physical notched display.
- Camera dimensions come from AppKit in logical points, not bitmap pixels or a hard-coded MacBook size. Test negative monitor origins too.
- The notch shares fetched lyrics, but not the full player's scroll or Synced/Static presentation preference.
- No microphone, audio capture, or FFT is used. The bars are a decorative playback indicator and respect Reduce Motion.
- `m3.Provide` resets the root background. Set it transparent after providing the theme, or a rectangular backing remains behind the rounded panel.
- Expanded mode replaces the small header cover with a collapse arrow; artwork is displayed once, below the camera. It requests a larger thumbnail for Retina rendering.
- The Open Meiro button uses its own 32 point target rather than the general icon button's larger hit area, so it cannot steal clicks from collapse. Expanded bounds reserve space on both sides even with a wide camera cutout.
- Synced rows use lyric indices inside the track's identity for position transitions, so a new song cannot inherit the previous song's animation state.
- The native window provides an expanded transparent canvas during motion. UI animation changes the painted rounded panel, and a guarded completion resizes the native window to its compact footprint. The callback reads the latest target so rapid reversals cannot be closed by an old completion.
- `AnimateTo` respects Reduce Motion by snapping geometry; `AnimateColor` preserves the short opacity fade. The panel background itself never fades.
- Auto-collapse uses `Context.Now` and `Context.After` on the notch UI thread. Wakeups recheck the current deadline; no timer callback retains UI elements. Mouse-acquired focus alone does not inhibit closing, while visible keyboard focus does. Slider drag capture, rather than pointer containment, determines when a seek ends.
- Never call AppKit's `resignKeyWindow` directly: it is a notification, not a focus-transfer operation. Release the nonactivating panel by ordering it out and immediately back without activation or native window animation. Local and global pointer monitors leave events unchanged and are removed when the panel is destroyed. Headless tests only guard the existing controls and animation; real macOS must verify focus transfer and menu-bar clicks.
- A deferred `Window.Update` can run between mouse down and mouse up. Releasing compact focus at that point sends a blur and clears MyGo's pressed control, making slightly held clicks fail. Track delivered button events per native window, defer passive focus release while a button is held, and retry after mouse-up dispatch. Do not rely on the hardware `pressedMouseButtons` snapshot for accessibility-generated events. `TestNotchCompactWingsCompleteHeldClicks` guards both controls, cancellation and repeated cycles; the native fixture is the regression proof for the focus race.
- `Show` cannot revive a destroyed MyGo window. Keep the main window alive via `PreventDefault` and `Hide`; do not let a surviving notch keep the process running with a dead main window. A macOS close configured to quit must use `App.Quit` so both windows close. `TestNotchBackgroundDefaultCanBeOverriddenAndSaved` drives the default and saved override through Settings; the native close/reopen fixture covers the OS lifetime.
