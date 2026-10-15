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
	"strings"
	"testing"
	"time"
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
// goes away on the first read, as a window the user closed does.
func fakeBrowser(mode string, args []string) {
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

func TestSignInWindowWithoutABrowser(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if _, err := SignInWindow(context.Background(), WindowOptions{}); !errors.Is(err, ErrNoBrowser) && !chromiumInstalledAtFixedPath() {
		t.Errorf("err = %v, want ErrNoBrowser", err)
	}
}

// chromiumInstalledAtFixedPath is true on a machine where a browser sits at a
// fixed location that no PATH change hides.
func chromiumInstalledAtFixedPath() bool {
	_, ok := findChromium(context.Background())
	return ok
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

func TestBrowserKindNamesTheChromiumBrowsersOfEachSystem(t *testing.T) {
	for id, want := range map[string]string{
		"google-chrome.desktop":         "chrome",
		"org.chromium.Chromium.desktop": "chromium",
		"microsoft-edge.desktop":        "edge",
		"brave-browser.desktop":         "brave",
		"vivaldi-stable.desktop":        "vivaldi",
		"helium.desktop":                "helium",
		"helium-browser.desktop":        "helium",
		"net.imput.helium":              "helium",
		"firefox.desktop":               "",
		"com.google.chrome":             "chrome",
		"com.microsoft.edgemac":         "edge",
		"com.apple.safari":              "",
		"ChromeHTML":                    "chrome",
		"MSEdgeHTM":                     "edge",
		"BraveHTML":                     "brave",
		"FirefoxURL-308046B0AF4A39CB":   "",
		"":                              "",
	} {
		if got := browserKind(id); got != want {
			t.Errorf("browserKind(%q) = %q, want %q", id, got, want)
		}
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
	if got := macHTTPSHandler([]byte("not json")); got != "" {
		t.Errorf("bad input gave %q", got)
	}
}

func TestPreferringPutsTheDefaultBrowserFirst(t *testing.T) {
	all := []candidate{{"chrome", "a"}, {"edge", "b"}, {"brave", "c"}, {"edge", "d"}}
	got := preferring(all, "edge")
	if want := []string{"b", "d", "a", "c"}; len(got) != 4 || got[0].path != want[0] || got[1].path != want[1] || got[2].path != want[2] || got[3].path != want[3] {
		t.Errorf("preferring = %v, want order %v", got, want)
	}
	// A default that is not Chromium-based, or not installed, changes nothing.
	if got := preferring(all, ""); got[0].path != "a" || got[3].path != "d" {
		t.Errorf("no default changed the order: %v", got)
	}
}
