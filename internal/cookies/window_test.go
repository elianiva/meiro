package cookies

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elianiva/meiro/internal/proc"
)

// TestMain lets the test binary stand in for the browser: the sign-in window
// starts it with the flags of a real one.
func TestMain(m *testing.M) {
	if mode := os.Getenv("MEIRO_FAKE_BROWSER"); mode != "" {
		fakeBrowser(mode, os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

// fakeBrowser serves the one DevTools command the sign-in uses. "signin"
// shows a signed-out page for two reads and then a signed-in one; "quit"
// goes away on the first read, as a window the user closed does. "firefox"
// writes the cookie database of a profile and waits to be killed;
// "firefox-quit" exits at once, or after MEIRO_FAKE_BROWSER_WAIT exists.
func fakeBrowser(mode string, args []string) {
	if mode == "firefox" || mode == "firefox-quit" {
		profile := valueAfter(args, "-profile")
		if profile == "" {
			os.Exit(2)
		}
		// The reader only passes its database gate once this file exists.
		if err := os.WriteFile(filepath.Join(profile, "cookies.sqlite"), nil, 0o600); err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("MEIRO_FAKE_BROWSER_PROFILE"), []byte(profile), 0o600); err != nil {
			os.Exit(2)
		}
		if mode == "firefox-quit" {
			if wait := os.Getenv("MEIRO_FAKE_BROWSER_WAIT"); wait != "" {
				for {
					if _, err := os.Stat(wait); err == nil {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			os.Exit(0)
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	var profile string
	for _, arg := range args {
		if v, ok := strings.CutPrefix(arg, "--user-data-dir="); ok {
			profile = v
		}
	}
	if err := os.WriteFile(os.Getenv("MEIRO_FAKE_BROWSER_PROFILE"), []byte(profile), 0o600); err != nil {
		os.Exit(2)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(2)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	active := "0\n/devtools/browser/fake"
	active = strings.Replace(active, "0", itoa(port), 1)
	if err := os.WriteFile(filepath.Join(profile, "DevToolsActivePort"), []byte(active), 0o600); err != nil {
		os.Exit(2)
	}
	conn, err := ln.Accept()
	if err != nil {
		os.Exit(2)
	}
	r := bufio.NewReader(conn)
	req, err := http.ReadRequest(r)
	if err != nil {
		os.Exit(2)
	}
	_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + wsAccept(req.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n"))
	ws := &wsConn{conn: conn, r: r}
	reads := 0
	for {
		raw, err := ws.readMessage()
		if err != nil {
			os.Exit(0)
		}
		var msg struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(raw, &msg)
		switch msg.Method {
		case "Storage.getCookies":
			reads++
			if mode == "quit" {
				os.Exit(0)
			}
			cookies := []map[string]any{{"name": "NID", "value": "x", "domain": ".google.com", "expires": -1}}
			if reads > 2 {
				cookies = append(cookies,
					map[string]any{"name": "SAPISID", "value": "live", "domain": ".youtube.com", "expires": 4102444800},
					map[string]any{"name": "SID", "value": "session", "domain": ".youtube.com", "expires": -1},
					map[string]any{"name": "OLD", "value": "gone", "domain": ".youtube.com", "expires": 10},
					map[string]any{"name": "LOGIN_INFO", "value": "li", "domain": "music.youtube.com", "expires": -1})
			}
			reply, _ := json.Marshal(map[string]any{"id": msg.ID, "result": map[string]any{"cookies": cookies}})
			_ = ws.writeText(reply)
		case "Browser.close":
			reply, _ := json.Marshal(map[string]any{"id": msg.ID, "result": map[string]any{}})
			_ = ws.writeText(reply)
			os.Exit(0)
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// valueAfter is the argument after name, or "".
func valueAfter(args []string, name string) string {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func fakeWindow(t *testing.T, mode string) WindowOptions {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEIRO_FAKE_BROWSER", mode)
	t.Setenv("MEIRO_FAKE_BROWSER_PROFILE", filepath.Join(t.TempDir(), "profile-path"))
	return WindowOptions{Executable: exe, Poll: 10 * time.Millisecond, Settle: 30 * time.Millisecond}
}

func fakeFirefoxWindow(t *testing.T, mode string, read profileReader) WindowOptions {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEIRO_FAKE_BROWSER", mode)
	t.Setenv("MEIRO_FAKE_BROWSER_PROFILE", filepath.Join(t.TempDir(), "profile-path"))
	return WindowOptions{
		Executable: exe,
		family:     firefoxFamily,
		read:       read,
		Poll:       10 * time.Millisecond,
		Settle:     30 * time.Millisecond,
	}
}

func TestSignInWindowWaitsForTheSessionThenDeletesTheProfile(t *testing.T) {
	options := fakeWindow(t, "signin")
	got, err := SignInWindow(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	// The signed-out reads are skipped, the expired cookie and the other
	// site's cookie are left out, and the subdomain's cookie is kept.
	if want := "SAPISID=live; SID=session; LOGIN_INFO=li"; got != want {
		t.Errorf("header = %q, want %q", got, want)
	}
	profile, err := os.ReadFile(os.Getenv("MEIRO_FAKE_BROWSER_PROFILE"))
	if err != nil || len(profile) == 0 {
		t.Fatalf("the browser was not started with a profile: %v", err)
	}
	if _, err := os.Stat(string(profile)); !os.IsNotExist(err) {
		t.Errorf("the profile %s was left behind: %v", profile, err)
	}
}

func TestSignInWindowReportsAClosedWindow(t *testing.T) {
	options := fakeWindow(t, "quit")
	if _, err := SignInWindow(context.Background(), options); !errors.Is(err, ErrWindowClosed) {
		t.Errorf("err = %v, want ErrWindowClosed", err)
	}
}

func TestSignInWindowStopsWhenCancelled(t *testing.T) {
	options := fakeWindow(t, "signin")
	options.Settle = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := SignInWindow(ctx, options); err == nil {
		t.Fatal("a cancelled sign-in succeeded")
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("cancel took %v to stop the window", time.Since(start))
	}
}

func TestSignInWindowFirefoxReadsTheSessionFromACookieDatabase(t *testing.T) {
	calls := 0
	var firstRead time.Time
	options := fakeFirefoxWindow(t, "firefox", func(_ context.Context, profile string) (string, error) {
		calls++
		if firstRead.IsZero() {
			firstRead = time.Now()
		}
		if _, err := os.Stat(filepath.Join(profile, "cookies.sqlite")); err != nil {
			t.Errorf("the reader saw no cookie database: %v", err)
		}
		if _, err := os.Stat(filepath.Join(profile, "user.js")); err != nil {
			t.Errorf("the reader saw no Firefox preferences: %v", err)
		}
		return "SAPISID=live; SID=session", nil
	})
	got, err := SignInWindow(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if want := "SAPISID=live; SID=session"; got != want {
		t.Errorf("header = %q, want %q", got, want)
	}
	// The store does not change after the first read, so yt-dlp runs once
	// and the cached header still waits out the settle time.
	if calls != 1 {
		t.Errorf("the reader ran %d times, want 1 while the store is unchanged", calls)
	}
	if waited := time.Since(firstRead); waited < options.Settle {
		t.Errorf("the session came back after %v, want at least the settle time %v", waited, options.Settle)
	}
	profile, err := os.ReadFile(os.Getenv("MEIRO_FAKE_BROWSER_PROFILE"))
	if err != nil || len(profile) == 0 {
		t.Fatalf("the browser was not started with a profile: %v", err)
	}
	if !strings.Contains(string(profile), "signin-") {
		t.Errorf("the browser got %q, want a private profile", profile)
	}
	if _, err := os.Stat(string(profile)); !os.IsNotExist(err) {
		t.Errorf("the profile %s was left behind: %v", profile, err)
	}
}

func TestSignInWindowRereadsACookieStoreThatLooksUnchanged(t *testing.T) {
	calls := 0
	options := fakeFirefoxWindow(t, "firefox", func(context.Context, string) (string, error) {
		calls++
		if calls == 1 {
			return "", nil
		}
		return "SAPISID=live; SID=session", nil
	})
	options.retry.recheck = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := SignInWindow(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	if want := "SAPISID=live; SID=session"; got != want {
		t.Errorf("header = %q, want %q", got, want)
	}
	if calls < 2 {
		t.Errorf("the reader ran %d times, want a second look at an unchanged store", calls)
	}
}

func TestSignInWindowReportsAClosedWindowDuringARead(t *testing.T) {
	started := make(chan struct{}, 1)
	hold := filepath.Join(t.TempDir(), "hold")
	t.Setenv("MEIRO_FAKE_BROWSER_WAIT", hold)
	options := fakeFirefoxWindow(t, "firefox-quit", func(ctx context.Context, _ string) (string, error) {
		started <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	})
	done := make(chan error, 1)
	go func() {
		_, err := SignInWindow(context.Background(), options)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the reader never ran")
	}
	// Only now does the window end, with the reader still blocked.
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrWindowClosed) {
			t.Errorf("err = %v, want ErrWindowClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the sign-in waited for a reader that was blocked")
	}
}

func TestSignInWindowReportsAStuckFirefoxRead(t *testing.T) {
	readErr := errors.New("yt-dlp could not read the profile")
	var calls atomic.Int32
	options := fakeFirefoxWindow(t, "firefox", func(context.Context, string) (string, error) {
		calls.Add(1)
		return "", readErr
	})
	options.retry.grace = 30 * time.Millisecond
	_, err := SignInWindow(context.Background(), options)
	if !errors.Is(err, readErr) {
		t.Fatalf("err = %v, want the reader's error", err)
	}
	if errors.Is(err, ErrWindowClosed) {
		t.Errorf("err = %v, a read failure must not look like a closed window", err)
	}
	if calls.Load() < 2 {
		t.Errorf("the reader ran %d times, want a retry before the report", calls.Load())
	}
}

func TestFirefoxPreferencesTurnOffFirstRunPages(t *testing.T) {
	profile := t.TempDir()
	if err := writeFirefoxPreferences(profile, "firefox"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(profile, "user.js"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "zen.welcome-screen") {
		t.Errorf("a Firefox profile got a Zen preference: %s", data)
	}
	if !strings.Contains(string(data), `user_pref("network.cookie.db.maxWalBytes", 32768);`) {
		t.Errorf("the Firefox profile would keep cookies out of the database: %s", data)
	}
	if err := writeFirefoxPreferences(profile, "zen"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(profile, "user.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `user_pref("zen.welcome-screen.seen", true);`) {
		t.Errorf("the Zen profile would show its welcome screen: %s", data)
	}
	if !strings.Contains(string(data), `user_pref("network.cookie.db.maxWalBytes", 32768);`) {
		t.Errorf("the Zen profile would keep cookies out of the database: %s", data)
	}
}

func TestPrivateProfileKeepsAProfileABrowserHolds(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	held := filepath.Join(root, signinProfilePrefix+"held")
	plain := filepath.Join(root, signinProfilePrefix+"plain")
	for _, dir := range []string{held, plain} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(held, "lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * proc.StaleAge)
	for _, dir := range []string{held, plain} {
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	b := candidate{kind: "firefox", family: firefoxFamily, profileRoot: root}
	if _, err := b.privateProfile(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Errorf("the ordinary stale profile %s survived the sweep: %v", plain, err)
	}
	if _, err := os.Stat(held); err != nil {
		t.Errorf("the profile a browser holds was swept: %v", err)
	}
}

func TestSignInWindowKillsTheBrowserWrapperTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the process-group kill is a Unix mechanism")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "data")
	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv("MEIRO_FAKE_BROWSER", "firefox")
	t.Setenv("MEIRO_FAKE_BROWSER_PROFILE", filepath.Join(t.TempDir(), "profile-path"))
	t.Setenv("MEIRO_TEST_BROWSER", exe)
	t.Setenv("MEIRO_TEST_MARKER", marker)

	script := filepath.Join(t.TempDir(), "browser")
	body := "#!/bin/sh\n" +
		"while true; do echo alive >> \"$MEIRO_TEST_MARKER\"; sleep 0.05; done &\n" +
		"exec \"$MEIRO_TEST_BROWSER\" \"$@\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}

	options := WindowOptions{
		Executable:  script,
		family:      firefoxFamily,
		profileRoot: root,
		read: func(context.Context, string) (string, error) {
			return "SAPISID=live; SID=session", nil
		},
		Poll:   10 * time.Millisecond,
		Settle: 30 * time.Millisecond,
	}
	if _, err := SignInWindow(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	profile, err := os.ReadFile(os.Getenv("MEIRO_FAKE_BROWSER_PROFILE"))
	if err != nil || len(profile) == 0 {
		t.Fatalf("the browser was not started with a profile: %v", err)
	}
	if filepath.Dir(string(profile)) != root {
		t.Errorf("profile %s is not under the profile root %s", profile, root)
	}
	if _, err := os.Stat(string(profile)); !os.IsNotExist(err) {
		t.Errorf("the profile %s was left behind: %v", profile, err)
	}

	markerSize := func() int64 {
		t.Helper()
		info, err := os.Stat(marker)
		if err != nil {
			t.Fatalf("the wrapper's marker file: %v", err)
		}
		return info.Size()
	}
	first := markerSize()
	time.Sleep(200 * time.Millisecond)
	if grew := markerSize(); grew != first {
		t.Errorf("the wrapper's background loop wrote %d bytes after the sign-in returned", grew-first)
	}
}

func TestWebSocketCarriesLargeMessages(t *testing.T) {
	a, b := net.Pipe()
	client := &wsConn{conn: a, r: bufio.NewReader(a), client: true}
	server := &wsConn{conn: b, r: bufio.NewReader(b)}
	for _, size := range []int{5, 125, 126, 65535, 65536, 200000} {
		message := []byte(strings.Repeat("é", size/2) + strings.Repeat("x", size%2))
		go func() { _ = client.writeText(message) }()
		got, err := server.readMessage()
		if err != nil || string(got) != string(message) {
			t.Fatalf("size %d: got %d bytes, %v", size, len(got), err)
		}
	}
}

func TestYouTubeHeaderTakesTheLaterCookieOfAName(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	got := youtubeHeader([]devtoolsCookie{
		{Name: "A", Value: "1", Domain: ".youtube.com", Expires: -1},
		{Name: "B", Value: "2", Domain: "notyoutube.com"},
		{Name: "A", Value: "3", Domain: "music.youtube.com", Expires: 2_000_000},
	}, now)
	if got != "A=3" {
		t.Errorf("header = %q", got)
	}
}

func TestFindBrowserTakesTheFlatpakInstallTheDefaultNames(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("xdg-settings and the Flatpak export path are Linux")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	for name, body := range map[string]string{
		"xdg-settings": "#!/bin/sh\necho org.mozilla.firefox.desktop\n",
		"flatpak":      "#!/bin/sh\n",
		"firefox":      "#!/bin/sh\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	export := filepath.Join(home, ".local", "share", "flatpak", "exports", "bin", "org.mozilla.firefox")
	if err := os.MkdirAll(filepath.Dir(export), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(export, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	b, ok := findBrowser(context.Background(), true)
	if !ok {
		t.Fatal("findBrowser found no browser")
	}
	if b.path != export {
		t.Errorf("findBrowser = %s, want the Flatpak install %s", b.path, export)
	}
	if want := filepath.Join(home, ".var", "app", "org.mozilla.firefox", "data"); b.profileRoot != want {
		t.Errorf("profileRoot = %s, want %s", b.profileRoot, want)
	}
}

func TestFindBrowserSkipsAnUninstalledFlatpakApp(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("xdg-settings and the Flatpak export path are Linux")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	for name, body := range map[string]string{
		"xdg-settings": "#!/bin/sh\necho org.mozilla.firefox.desktop\n",
		"flatpak":      "#!/bin/sh\nexit 1\n",
		"firefox":      "#!/bin/sh\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	export := filepath.Join(home, ".local", "share", "flatpak", "exports", "bin", "org.mozilla.firefox")
	if err := os.MkdirAll(filepath.Dir(export), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(export, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	b, ok := findBrowser(context.Background(), true)
	if !ok {
		t.Fatal("findBrowser found no browser")
	}
	if b.path != "firefox" {
		t.Errorf("findBrowser picked %s, want the native firefox", b.path)
	}
	if b.flatpakID != "" || b.snapName != "" {
		t.Errorf("findBrowser picked a packaged install %+v, want native", b)
	}
}

func TestFindBrowserSkipsFirefoxWithoutYtDlp(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the browser table is discovered with xdg-settings on Linux")
	}
	t.Run("findBrowser takes chromium over an unreadable firefox", func(t *testing.T) {
		bin := t.TempDir()
		t.Setenv("PATH", bin)
		t.Setenv("HOME", t.TempDir())
		for name, body := range map[string]string{
			"xdg-settings": "#!/bin/sh\necho firefox.desktop\n",
			"firefox":      "#!/bin/sh\n",
			"chromium":     "#!/bin/sh\n",
		} {
			if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		b, ok := findBrowser(context.Background(), false)
		if !ok {
			t.Fatal("findBrowser found no browser")
		}
		if b.kind != "chromium" {
			t.Errorf("findBrowser picked %s, want chromium", b.kind)
		}
	})

	t.Run("a firefox with no yt-dlp says what it needs", func(t *testing.T) {
		bin := t.TempDir()
		t.Setenv("PATH", bin)
		t.Setenv("HOME", t.TempDir())
		marker := filepath.Join(t.TempDir(), "started")
		t.Setenv("MEIRO_TEST_MARKER", marker)
		if err := os.WriteFile(filepath.Join(bin, "firefox"),
			[]byte("#!/bin/sh\necho started >> \"$MEIRO_TEST_MARKER\"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := SignInWindow(context.Background(), WindowOptions{Poll: 10 * time.Millisecond, Settle: 30 * time.Millisecond})
		if !errors.Is(err, errFirefoxNeedsYtDlp) {
			t.Errorf("err = %v, want errFirefoxNeedsYtDlp", err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Errorf("the sign-in started Firefox without yt-dlp: %v", err)
		}
	})

	t.Run("a chromium window opens without yt-dlp", func(t *testing.T) {
		bin := t.TempDir()
		t.Setenv("PATH", bin)
		t.Setenv("HOME", t.TempDir())
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		firefoxMarker := filepath.Join(t.TempDir(), "firefox-started")
		t.Setenv("MEIRO_TEST_MARKER", firefoxMarker)
		if err := os.WriteFile(filepath.Join(bin, "firefox"),
			[]byte("#!/bin/sh\necho started >> \"$MEIRO_TEST_MARKER\"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("MEIRO_FAKE_BROWSER", "signin")
		t.Setenv("MEIRO_FAKE_BROWSER_PROFILE", filepath.Join(t.TempDir(), "profile-path"))
		t.Setenv("MEIRO_TEST_BROWSER", exe)
		wrapper := "#!/bin/sh\nexec \"$MEIRO_TEST_BROWSER\" \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, "chromium"), []byte(wrapper), 0o700); err != nil {
			t.Fatal(err)
		}
		options := WindowOptions{Poll: 10 * time.Millisecond, Settle: 30 * time.Millisecond}
		got, err := SignInWindow(context.Background(), options)
		if err != nil {
			t.Fatal(err)
		}
		if want := "SAPISID=live; SID=session; LOGIN_INFO=li"; got != want {
			t.Errorf("header = %q, want %q", got, want)
		}
		if _, err := os.Stat(firefoxMarker); !os.IsNotExist(err) {
			t.Errorf("the sign-in started Firefox: %v", err)
		}
	})
}

func TestLinuxCandidatesCoverNativeFlatpakAndSnapInstalls(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Flatpak and Snap install kinds are Linux installs")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	bin := t.TempDir()
	for _, name := range []string{"flatpak", "snap"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)

	byPath := map[string]candidate{}
	for _, c := range browserCandidates() {
		byPath[c.path] = c
	}

	flatpaks := []struct {
		kind   string
		family browserFamily
		id     string
	}{
		{"chrome", chromiumFamily, "com.google.Chrome"},
		{"chromium", chromiumFamily, "org.chromium.Chromium"},
		{"edge", chromiumFamily, "com.microsoft.Edge"},
		{"brave", chromiumFamily, "com.brave.Browser"},
		{"vivaldi", chromiumFamily, "com.vivaldi.Vivaldi"},
		{"firefox", firefoxFamily, "org.mozilla.firefox"},
		{"zen", firefoxFamily, zenFlatpak},
	}
	for _, want := range flatpaks {
		for _, root := range []string{
			filepath.Join(home, ".local", "share", "flatpak", "exports", "bin"),
			"/var/lib/flatpak/exports/bin",
		} {
			path := filepath.Join(root, want.id)
			c, ok := byPath[path]
			if !ok {
				t.Errorf("no Flatpak %s candidate at %s", want.kind, path)
				continue
			}
			if c.kind != want.kind || c.family != want.family {
				t.Errorf("Flatpak candidate at %s is kind %s family %v, want %s and %v", path, c.kind, c.family, want.kind, want.family)
			}
			if wantRoot := filepath.Join(home, ".var", "app", want.id, "data"); c.profileRoot != wantRoot {
				t.Errorf("Flatpak %s profileRoot = %s, want %s", want.kind, c.profileRoot, wantRoot)
			}
			if wantID := strings.ToLower(want.id) + ".desktop"; c.desktopID != wantID {
				t.Errorf("Flatpak %s desktopID = %s, want %s", want.kind, c.desktopID, wantID)
			}
		}
	}

	snaps := []struct {
		kind   string
		family browserFamily
		name   string
	}{
		{"chromium", chromiumFamily, "chromium"},
		{"firefox", firefoxFamily, "firefox"},
	}
	for _, want := range snaps {
		path := filepath.Join("/snap/bin", want.name)
		c, ok := byPath[path]
		if !ok {
			t.Errorf("no Snap %s candidate at %s", want.kind, path)
			continue
		}
		if c.kind != want.kind || c.family != want.family {
			t.Errorf("Snap candidate at %s is kind %s family %v, want %s and %v", path, c.kind, c.family, want.kind, want.family)
		}
		if wantRoot := filepath.Join(home, "snap", want.name, "common"); c.profileRoot != wantRoot {
			t.Errorf("Snap %s profileRoot = %s, want %s", want.kind, c.profileRoot, wantRoot)
		}
		if wantID := want.name + "_" + want.name + ".desktop"; c.desktopID != wantID {
			t.Errorf("Snap %s desktopID = %s, want %s", want.kind, c.desktopID, wantID)
		}
	}

	for _, name := range []string{
		"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
		"microsoft-edge", "microsoft-edge-stable", "brave-browser", "brave",
		"vivaldi", "vivaldi-stable", "helium", "helium-browser",
		"firefox", "zen", "zen-browser",
	} {
		c, ok := byPath[name]
		if !ok {
			t.Errorf("no native candidate for %s", name)
			continue
		}
		if c.profileRoot != "" || c.desktopID != "" {
			t.Errorf("native candidate %s carries a profile root or desktop file: %+v", name, c)
		}
	}
}

// TestFindBrowserKeepsChromiumFirstWhenNothingIsDefaulted checks the Linux
// fallback order: with no default browser to prefer, a Flatpak Chromium wins
// over a native Zen.
func TestFindBrowserKeepsChromiumFirstWhenNothingIsDefaulted(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Flatpak and native candidates are Linux installs")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	for name, body := range map[string]string{
		"zen":     "#!/bin/sh\n",
		"flatpak": "#!/bin/sh\n[ \"$1 $2\" = \"info org.chromium.Chromium\" ]\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	export := filepath.Join(home, ".local", "share", "flatpak", "exports", "bin", "org.chromium.Chromium")
	if err := os.MkdirAll(filepath.Dir(export), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(export, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	// No xdg-settings on PATH, so there is no default to prefer.
	b, ok := findBrowser(context.Background(), true)
	if !ok {
		t.Fatal("findBrowser found no browser")
	}
	if b.path != export {
		t.Errorf("findBrowser picked %s, want the Flatpak Chromium %s", b.path, export)
	}
}

// TestFindBrowserInAFlatpakAsksTheHost checks that, inside a Flatpak, the
// browsers and the default are read on the host through flatpak-spawn, and
// that the profile goes where the host browser can see it.
func TestFindBrowserInAFlatpakAsksTheHost(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Flatpak is Linux")
	}
	home := t.TempDir()
	cache := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FLATPAK_ID", "com.elianiva.meiro")
	t.Setenv("XDG_CACHE_HOME", cache)
	sandbox := t.TempDir()
	host := t.TempDir()
	t.Setenv("PATH", sandbox)
	scripts := map[string]string{
		filepath.Join(sandbox, "flatpak-spawn"): "#!/bin/sh\n[ \"$1 $2\" = \"--host --watch-bus\" ] || exit 99\nshift 2\nPATH=" + host + ":/bin:/usr/bin exec \"$@\"\n",
		filepath.Join(host, "xdg-settings"):     "#!/bin/sh\necho chromium.desktop\n",
		filepath.Join(host, "google-chrome"):    "#!/bin/sh\n",
		filepath.Join(host, "chromium"):         "#!/bin/sh\n",
	}
	for path, body := range scripts {
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	b, ok := findBrowser(context.Background(), true)
	if !ok {
		t.Fatal("findBrowser found no browser on the host")
	}
	if b.path != "chromium" {
		t.Errorf("findBrowser picked %s, want the host's default chromium", b.path)
	}
	if b.profileRoot != cache {
		t.Errorf("profileRoot = %q, want the cache directory the host shares, %s", b.profileRoot, cache)
	}
}

// TestSignInWindowInAFlatpakWithoutHostAccessAsksForIt checks that a Flatpak
// that may not reach the host says so, rather than that no browser exists.
func TestSignInWindowInAFlatpakWithoutHostAccessAsksForIt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Flatpak is Linux")
	}
	t.Setenv("FLATPAK_ID", "com.elianiva.meiro")
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if err := os.WriteFile(filepath.Join(bin, "flatpak-spawn"), []byte("#!/bin/sh\necho 'Portal call failed: org.freedesktop.DBus.Error.ServiceUnknown' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := SignInWindow(context.Background(), WindowOptions{}); !errors.Is(err, ErrNeedsHostAccess) {
		t.Errorf("err = %v, want ErrNeedsHostAccess", err)
	}
}

// TestLinuxCandidatesUseXDGDataHome checks a user Flatpak install under a
// custom XDG_DATA_HOME is found at the path Flatpak actually uses.
func TestLinuxCandidatesUseXDGDataHome(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Flatpak user exports are Linux")
	}
	home := t.TempDir()
	data := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", data)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "flatpak"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	export := filepath.Join(data, "flatpak", "exports", "bin", "org.mozilla.firefox")
	for _, c := range linuxCandidates(browserTable()) {
		if c.path != export {
			continue
		}
		if want := filepath.Join(home, ".var", "app", "org.mozilla.firefox", "data"); c.profileRoot != want {
			t.Errorf("profileRoot = %s, want %s", c.profileRoot, want)
		}
		return
	}
	t.Errorf("no Flatpak candidate at %s", export)
}

// TestDarwinAndWindowsCandidatesKeepTheTableOrder checks the fallback order
// the browserCandidates comment promises: the Chromium-based rows come first,
// whatever install root a browser lives in.
func TestDarwinAndWindowsCandidatesKeepTheTableOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func([]browserSpec) []candidate
		env   func(*testing.T)
	}{
		{"darwin", darwinCandidates, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
		}},
		{"windows", windowsCandidates, func(t *testing.T) {
			t.Setenv("ProgramFiles", t.TempDir())
			t.Setenv("ProgramFiles(x86)", t.TempDir())
			t.Setenv("LocalAppData", t.TempDir())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.env(t)
			table := browserTable()
			rank := map[string]int{}
			for i, spec := range table {
				rank[spec.kind] = i
			}
			last := 0
			seenFirefox := false
			for _, c := range tc.build(table) {
				if rank[c.kind] < last {
					t.Fatalf("%s is out of table order", c.path)
				}
				last = rank[c.kind]
				if c.family == firefoxFamily {
					seenFirefox = true
					continue
				}
				if seenFirefox {
					t.Fatalf("Chromium-family %s follows a Firefox-family candidate", c.path)
				}
			}
		})
	}
}

func TestFirefoxProfilesSkipSignInProfiles(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, signinProfilePrefix+"crashed")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "cookies.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := firefoxProfiles([]string{root}); len(got) != 0 {
		t.Errorf("firefoxProfiles = %v, want the sign-in profile skipped", got)
	}
}

func TestMacHTTPSHandlerIsTheAppForHTTPS(t *testing.T) {
	prefs := `{"LSHandlers":[{"LSHandlerURLScheme":"http","LSHandlerRoleAll":"org.mozilla.firefox"},
		{"LSHandlerContentType":"public.html","LSHandlerRoleAll":"com.apple.safari"},
		{"LSHandlerURLScheme":"https","LSHandlerRoleAll":"com.brave.browser"}]}`
	if got := macHTTPSHandler([]byte(prefs)); got != "com.brave.browser" {
		t.Errorf("handler = %q", got)
	}
	if got := macHTTPSHandler([]byte(`{"LSHandlers":[]}`)); got != "" {
		t.Errorf("no choice gave %q", got)
	}
	empty := `{"LSHandlers":[{"LSHandlerURLScheme":"https"},{"LSHandlerURLScheme":"https","LSHandlerRoleAll":"com.google.chrome"}]}`
	if got := macHTTPSHandler([]byte(empty)); got != "com.google.chrome" {
		t.Errorf("handler with an empty first entry = %q", got)
	}
	if got := macHTTPSHandler([]byte("not json")); got != "" {
		t.Errorf("bad input gave %q", got)
	}
}

func TestPreferringPutsTheDefaultBrowserFirst(t *testing.T) {
	all := []candidate{
		{kind: "chrome", path: "a"},
		{kind: "edge", path: "b"},
		{kind: "brave", path: "c"},
		{kind: "edge", path: "d"},
	}
	got := preferring(all, "", "edge")
	if want := []string{"b", "d", "a", "c"}; !samePaths(got, want) {
		t.Errorf("preferring = %v, want order %v", got, want)
	}
	// A default that is not one of these, or not installed, changes nothing.
	if got := preferring(all, "", ""); !samePaths(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("no default changed the order: %v", got)
	}

	// The exact .desktop file the system named wins over another install of
	// the same browser; without one, every candidate of the named kind moves
	// up and keeps its order.
	installs := []candidate{
		{kind: "firefox", path: "native"},
		{kind: "firefox", path: "flatpak", desktopID: "org.mozilla.firefox.desktop"},
		{kind: "firefox", path: "snap", desktopID: "firefox_firefox.desktop"},
		{kind: "chrome", path: "chrome"},
	}
	got = preferring(installs, "org.mozilla.firefox.desktop", "firefox")
	if want := []string{"flatpak", "native", "snap", "chrome"}; !samePaths(got, want) {
		t.Errorf("a Flatpak default gave %v, want order %v", got, want)
	}
	got = preferring(installs, "firefox_firefox.desktop", "firefox")
	if want := []string{"snap", "native", "flatpak", "chrome"}; !samePaths(got, want) {
		t.Errorf("a Snap default gave %v, want order %v", got, want)
	}
	got = preferring(installs, "firefox.desktop", "firefox")
	if want := []string{"native", "flatpak", "snap", "chrome"}; !samePaths(got, want) {
		t.Errorf("a native default gave %v, want order %v", got, want)
	}
}

func TestStaleAgeOutlivesTheSignInTimeout(t *testing.T) {
	if proc.StaleAge <= 2*SignInTimeout {
		t.Errorf("proc.StaleAge = %v, want more than twice SignInTimeout %v", proc.StaleAge, SignInTimeout)
	}
}

// samePaths compares the paths of candidates in order.
func samePaths(got []candidate, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i, c := range got {
		if c.path != want[i] {
			return false
		}
	}
	return true
}
