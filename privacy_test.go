package main

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/sentry"
	"github.com/elianiva/meiro/internal/settings"
)

// pingRecorder keeps the install pings the app would send.
type pingRecorder struct {
	events []string
	props  []map[string]any
}

func (r *pingRecorder) capture(event string, properties map[string]any) error {
	r.events = append(r.events, event)
	r.props = append(r.props, properties)
	return nil
}

// crashRecorder keeps the crash reports the app would send.
type crashRecorder struct {
	crashes []sentry.Crash
}

func (r *crashRecorder) capture(crash sentry.Crash) error {
	r.crashes = append(r.crashes, crash)
	return nil
}

// A new copy asks before it sends anything, sends the one ping when the user
// says yes, and never asks again.
func TestFirstRunAsksBeforeTheInstallPing(t *testing.T) {
	a := newTestApp(t)
	r := &pingRecorder{}
	a.capture = r.capture
	path := filepath.Join(t.TempDir(), "settings.json")
	a.saver.Path = path
	a.startPrivacy()
	if !a.privacy.ask {
		t.Fatal("the first run does not ask about the install ping")
	}

	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()
	for _, want := range []string{"Send one anonymous ping?", "No thanks", "Just ping", "Send details"} {
		if !tt.HasText(want) {
			t.Errorf("the notice does not show %q: %q", want, tt.Texts())
		}
	}
	if len(r.events) != 0 {
		t.Fatalf("the ping went out before the answer: %v", r.events)
	}

	if err := tt.Click("Send details"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(r.events) != 1 || r.events[0] != "install" {
		t.Fatalf("sent %v, want one install ping", r.events)
	}
	for _, key := range []string{"os", "arch", "version"} {
		if r.props[0][key] == nil {
			t.Errorf("the install ping lacks %q: %v", key, r.props[0])
		}
	}
	if a.settings.UsagePing == nil || !*a.settings.UsagePing || !a.settings.UsagePingDetails || !a.settings.UsagePingSent {
		t.Errorf("the answer came back as %+v", a.settings)
	}
	if a.privacy.ask {
		t.Error("the notice stayed up after the answer")
	}
	a.saver.Wait()

	// A later launch keeps the answer, asks nothing, and sends nothing.
	next := newTestApp(t)
	next.settings = settings.Load(path)
	next.capture = r.capture
	next.startPrivacy()
	if next.privacy.ask || len(r.events) != 1 {
		t.Errorf("the next launch asked again (%v) or sent again (%v)", next.privacy.ask, r.events)
	}
}

// The fallback sends the one message with nothing but its event name.
func TestJustPingSendsNoDetails(t *testing.T) {
	a := newTestApp(t)
	r := &pingRecorder{}
	a.capture = r.capture
	a.startPrivacy()
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()
	if err := tt.Click("Just ping"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(r.events) != 1 || r.events[0] != "install" {
		t.Fatalf("sent %v, want one install ping", r.events)
	}
	if len(r.props[0]) != 0 {
		t.Errorf("the fallback carries %v, want nothing", r.props[0])
	}
	if a.settings.UsagePingDetails || a.settings.UsagePing == nil || !*a.settings.UsagePing {
		t.Errorf("the answer came back as %+v", a.settings)
	}
}

func TestDecliningTheInstallPingSendsNothing(t *testing.T) {
	a := newTestApp(t)
	r := &pingRecorder{}
	a.capture = r.capture
	path := filepath.Join(t.TempDir(), "settings.json")
	a.saver.Path = path
	a.startPrivacy()
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()
	if err := tt.Click("No thanks"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(r.events) != 0 {
		t.Fatalf("a no sent %v", r.events)
	}
	if a.settings.UsagePing == nil || *a.settings.UsagePing || a.settings.UsagePingDetails || a.settings.UsagePingSent {
		t.Errorf("the answer came back as %+v", a.settings)
	}
	a.saver.Wait()

	next := newTestApp(t)
	next.settings = settings.Load(path)
	next.capture = r.capture
	next.startPrivacy()
	if next.privacy.ask || len(r.events) != 0 {
		t.Errorf("the next launch asked again (%v) or sent (%v)", next.privacy.ask, r.events)
	}
}

// A crash report only leaves the app when the user turned it on, and the
// dialog says where it went.
func TestCrashReportsFollowTheSwitch(t *testing.T) {
	a := newTestApp(t)
	r := &crashRecorder{}
	a.sendCrash = r.capture

	a.settings.CrashReports = false
	a.crashed("loading a thing", "boom", []byte("goroutine 1 [running]"), false)
	if a.crash.reported || len(r.crashes) != 0 {
		t.Fatalf("a crash was sent with reports off: reported=%v crashes=%v", a.crash.reported, r.crashes)
	}
	a.crash = nil

	a.settings.CrashReports = true
	a.crashed("loading a thing", "boom", []byte("goroutine 1 [running]"), false)
	if !a.crash.reported || len(r.crashes) != 1 {
		t.Fatalf("reported=%v crashes=%v, want one crash report", a.crash.reported, r.crashes)
	}
	if got := r.crashes[0]; got.Where != "loading a thing" || got.Message != "boom" {
		t.Errorf("report = %+v", got)
	}
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()
	if !tt.HasText("Crash reports are on") || !tt.HasText("to Sentry") {
		t.Errorf("the crash dialog does not say the report was sent: %q", tt.Texts())
	}
}

// The Settings card shows the messages, sends the ping when it is turned on
// there, and turns crash reports on and off.
func TestPrivacyCardControlsBothMessages(t *testing.T) {
	a := newTestApp(t)
	pings := &pingRecorder{}
	crashes := &crashRecorder{}
	a.capture = pings.capture
	a.sendCrash = crashes.capture
	no := false
	a.settings.UsagePing = &no
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()
	if err := tt.Click("Search settings"); err != nil {
		t.Fatal(err)
	}
	tt.Type("telemetry")
	tt.Frame()
	if !tt.HasText("Anonymous ping") || !tt.HasText("Crash reports") {
		t.Fatalf("the privacy card is missing: %q", tt.Texts())
	}

	if err := tt.Click("Anonymous install ping"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(pings.events) != 1 || pings.events[0] != "install" || !a.settings.UsagePingSent {
		t.Fatalf("turning the ping on sent %v (sent %v)", pings.events, a.settings.UsagePingSent)
	}
	if !tt.HasText("Sent") || !tt.HasText("ping only") {
		t.Errorf("the card does not show the ping went out: %q", tt.Texts())
	}
	if a.settings.UsagePingDetails {
		t.Error("turning the switch on sent the system details without asking for them")
	}

	if err := tt.Click("Send crash reports"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.settings.CrashReports {
		t.Error("the crash report switch did not turn on")
	}
	if err := tt.Click("Send crash reports"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.CrashReports {
		t.Error("the crash report switch did not turn off")
	}
}

// A build with neither a key nor a DSN sends nothing and never asks.
func TestABuildWithNoKeysIsSilent(t *testing.T) {
	a := newTestApp(t)
	a.capture, a.sendCrash = nil, nil
	a.startPrivacy()
	if a.privacy.ask {
		t.Fatal("a build that cannot send asked anyway")
	}
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()
	if tt.HasText("Anonymous ping") || tt.HasText("Crash reports") {
		t.Errorf("a build that cannot send shows the privacy card: %q", tt.Texts())
	}
}
