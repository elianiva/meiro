# Privacy and telemetry

Meiro asks once, on first run, whether it may send a single anonymous ping so
the project can count installs. The same card in Settings holds that answer
and a separate switch for crash reports, which is off by default.

## Sub-features

- `privacy-first-run` shows the notice once, sends one `install` event only
  on yes, and never asks again, whether the answer was yes or no.
- `privacy-settings` shows the answer under Settings, sends the ping when a
  declined answer is turned on later, and turns crash reports on and off.
- `privacy-crash-report` sends the crash dialog's redacted details to Sentry
  only while crash reports are on, and says so in the dialog.

## How to get to it (user POV)

- Launch Meiro with no settings file. The notice "Send one anonymous ping?" appears
  over the first page.
- Open Settings, search `telemetry`, and the Privacy card shows `Anonymous
  ping`, `Crash reports` and `What is sent`.

## Driving it with the headless tester

Preconditions: `a := newTestApp(t)`, and replace `a.capture` (PostHog) and
`a.sendCrash` (Sentry) with recorders: the tester never touches the network. A
build with no project key has no notice, and one with neither key nor DSN has
no card, so a recorder doubles as the key.

- **First run.** Call `a.startPrivacy()` before the first frame. The notice
  shows `One anonymous ping?`, `No thanks` and `Send once`. Click one of the
  two and read `a.settings` and the recorder. A second `newTestApp(t)` with
  `settings.Load(path)` must ask nothing.
- **Settings card.** Push `/settings`, click `Search settings`, type
  `telemetry`. Click `Anonymous install ping` to send a declined ping, and
  `Send crash reports` to toggle reports. The card shows `Sent` once the ping
  arrived.
- **Crash report.** With `a.settings.CrashReports = true`, call
  `a.crashed("loading a thing", "boom", stack, false)`; the `sendCrash`
  recorder gets one report, and the dialog says the details went to Sentry.

## Gotchas

- The ping is a single message. `UsagePingSent` is kept only once the message
  arrived, so a failed send is retried on the next launch, which asks nothing.
- Closing the notice with Escape or the scrim counts as `No thanks`.
- The ping row only exists while `a.capture` is set, and the crash row while
  `a.sendCrash` is. A release bakes the PostHog key and Sentry DSN in from
  `MEIRO_POSTHOG_KEY` and `MEIRO_SENTRY_DSN`; see `tools/dist.sh`.
- The Sentry project must have Prevent Storing of IP Addresses on (Settings,
  Security and Privacy). The app cannot set it, and the README says it is on.
