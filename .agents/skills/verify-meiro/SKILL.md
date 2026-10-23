---
name: verify-meiro
description: Proves Meiro changes with headless UI scenarios and, when needed, the real orb Desktop window, screenshots, request counters and before/after runs. Use when adding or changing a feature or fix in this repo, when asked to prove or demo it end to end, or before opening a PR that changes what a user sees.
---

# Verify Meiro

Meiro is a native desktop music app (MyGo UI, Go). Use the headless tester for repeatable behavior proofs and the orb Desktop for the real native window. `ui.NewTester` draws real app pixels, `fakeMusic` stands in for YouTube, and playback decodes real audio with ffmpeg on a null device. A proof is a scratch Go test that drives the app and records a screenshot for each step.

Read `AGENTS.md` and `../mygo-maintenance/SKILL.md` for the code rules. This skill is for the evidence.

## When you add a feature

1. Open `features/README.md`. Find the feature file your change touches, or add one (same four H2s as the others) and a line in the index.
2. Run `scripts/doctor.sh`.
3. Copy `templates/zz_proof_test.go.tmpl` to `zz_proof_<feature>_test.go` in the repo root and replace only `TestProofFeature` with the scenario from the feature file's recipe. `prove.sh` supplies the recorder helpers automatically; use the harness constructors with their `*testing.T` argument, for example `newTestApp(t)`.
4. Run `scripts/prove.sh --before base --label <feature> 'TestProof'`. Read the screenshots. Fix the app or the scenario until the after run passes for the right reason.
5. Keep the part that users or other code rely on as a normal test in the existing `*_test.go` files, per `AGENTS.md`. Delete the scratch proof. Update the feature file with what you learned.
6. Add a `CHANGELOG.md` entry when the change is user visible.
7. For every user-visible change, also run `scripts/native.sh start` and inspect the real Desktop window; the headless proof complements, not replaces, that native check. This is mandatory for native rendering, window behavior, and behavior the tester cannot show. Do not declare native verification impossible until `doctor.sh` has attempted to ensure the Desktop. Report with the evidence under Evidence below.

## Doctor

`scripts/doctor.sh` bootstraps and checks the environment: it downloads MyGo if missing, fetches complete Git history when the checkout is shallow, attempts `amp orb desktop ensure`, checks ffmpeg and that `./youtube` compiles, then checks Desktop tools (grim, wtype, wlrctl, yt-dlp). Run it first, and again when a proof behaves oddly. A `FAIL` line blocks headless proofs. A note about the Desktop means native availability was not established; resolve it or report the specific ensure failure rather than claiming the Desktop is unavailable.

## Launch

There is no process to start. Each proof builds a fresh app in memory and a window with `ui.NewTester(a.view, 1000, 700)`. State starts empty, with settings and history in a temp dir where the scenario sets one.

Use the constructors that exist, passing the test handle as required.

- `newTestApp(t)` is a signed-out app on the fake YouTube.
- `signedInApp(t)` has a saved session. Call `resetFakes()` before it.
- `playbackApp(t)` resolves streams through a fake resolver `r`. Set `r.file` for real audio.
- `stateApp(t, s, signedIn)` lets a test switch YouTube to offline, 500 or empty.

## Drive

Click what the user clicks, by visible label. `p.click(label, step)` clicks and records. `p.openMenu(title, step)` hovers a row and opens its menu. `tt.Type`, `tt.Key`, `tt.RightClick` and `tt.Scroll` do the rest. Do not call `a.play`, `a.enqueue` and the like to act. Read them to assert.

Labels, seed titles and recipes for each feature are in `features/`. Existing tests are the best source of exact steps. Search them for the label you need (`grep -n 'Click("' *_test.go`).

A feature that needs a response the fake does not have gets a new canned response in `harness_test.go`, in the same change. Keep the fake honest. A fake that cannot fail cannot prove failure handling, so also cover the failing case with `refuseWrites`, `switchable.mode` or `r.err`.

## Evidence

A proof the reader can trust has all of these.

- The action and the result. One screenshot per step from `p.step`, not only the last frame. Look at the images. A capture you did not read verifies nothing.
- A second view of every side effect. For a write, assert the counter and `written()`. For settings or history, read the file. For playback, read `a.player.Position()` after real audio and `a.history`.
- Before and after. `scripts/prove.sh --before base` runs the same scratch tests on a clean worktree of the merge base. For a fix, before must fail for the reason the bug report gives. For a feature, before fails because the feature is absent. Say so, and show both screenshots side by side.
- A control. One case that should not change, such as the signed-out app that must show no like button, or the page that stays unchanged.
- The full suite. Run `scripts/checks.sh` once for race tests (which cover the plain tests), lint, and format; when Go files were added, moved or deleted, run `scripts/checks.sh gazelle test lint format`. It streams the active phase's log. If Bazel is unavailable it runs `go test ./... -count=1` and `go vet ./...`. Say which ran. Add `--race` to `prove.sh` for threading changes.
- What stays unproven. `features/native-only.md` lists it. Name each item your change touches.

Artifacts are in `.amp/in/artifacts/verify-meiro/<label>/{before,after}/` with `NN-step.png`, `steps.txt` (every text on screen per step), `go-test.log` and `exit-code`. The directory is gitignored. Embed the screenshots that matter in the final answer.

## Cleanup

Delete `zz_proof*_test.go` from the repo root. `.gitignore` already ignores it, but a stray file still breaks `bazel test` if listed and confuses the next agent. `prove.sh` removes its own worktree. It never removes artifacts. Do not delete the artifacts directory.

## Native app

The orb Desktop (labwc, Wayland, a null audio sink) runs the real window against live YouTube Music, signed out. `doctor.sh` attempts `amp orb desktop ensure`; use this real window for a final look at every user-visible change instead of settling for a headless render. `.agents/setup` installs grim, wtype, wlrctl and yt-dlp.

```sh
scripts/native.sh start          # builds, starts the Desktop, launches Meiro on empty data, fullscreen
scripts/native.sh shot home      # read the PNG, then pick pixels from it
scripts/native.sh capture-loop 1 10 search-flow  # timed screenshot fallback; not a video recording
scripts/native.sh click 980 45   # pixels of the last screenshot
scripts/native.sh type yorushika
scripts/native.sh key Return     # also ctrl+k, Tab, space
scripts/native.sh stop
```

Rules learned the hard way.

- Take a screenshot before every click and read the coordinates from it. Layout moves between runs.
- Never change the output mode or scale (`wlr-randr`). It crashes the Desktop tab's stream. If that happens, kill the `labwc` process and run `amp orb desktop ensure`.
- Wait after each action. `native.sh` already sleeps 0.5 to 0.7 s per input. Add a sleep for network results.
- Prove sound by the stream, not by ear. `. ~/.cache/amp/desktop/environment; pactl list sink-inputs` shows a `Meiro` input with `Corked: no`, and the player position in the screenshot advances.
- Meiro runs with `PATH` including `~/.local/bin` (for yt-dlp) and fresh `XDG_*` dirs under `.amp/in/native/data`. Its log is `.amp/in/native/data/config/meiro/logs/meiro.log`.
- Never sign in with a real account here. The artifacts hold everything on screen.
- MPRIS cannot be verified in the orb because there is no user D-Bus session. Real account sign-in is prohibited. Consequently channel switching, brand-channel identity and playback-history reporting stay on the fake-session path here; fake tests verify the code path/request shape, not the real-account integration. State this limitation directly instead of rediscovering it or implying a real-session check passed.
- There is no native screen recorder in the orb. Use `native.sh capture-loop` or successive `native.sh shot` captures with step notes as the standard fallback. Do not stitch screenshots into an MP4 or describe them as a real screen recording.
- Real YouTube changes. Do not assert on seeded titles. Assert on structure and on behavior.

A user can watch the same window in the Desktop tab. `features/native-only.md` lists what still needs another OS.

## Helpers

- `scripts/doctor.sh` bootstraps MyGo/history, attempts Desktop ensure, prints `ok`, `note` or `FAIL` lines and exits 1 on a blocker.
- `scripts/prove.sh [--before REF|base] [--label NAME] [--race] TEST_REGEX` runs scratch tests with `PROOF_DIR` set and prints the pass or fail lines, the exit code and the artifact path. Exit status is the after run's status.
- `scripts/checks.sh [test|lint|format|build|gazelle]...` runs the named phases (default `test lint format`), streaming each phase's log, then prints one summary plus failure tails.
- `scripts/native.sh start|shot|capture-loop|click|rclick|type|key|status|stop` drives the real window on the Desktop. Screenshots go to `.amp/in/artifacts/verify-meiro/native/`.
- `templates/zz_proof_test.go.tmpl` is the scenario starter. `prove.sh` injects `templates/zz_proof_helpers_test.go.tmpl` into both working and before worktrees, so a scratch test does not need to duplicate helper code.
