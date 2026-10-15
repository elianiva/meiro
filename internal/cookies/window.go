package cookies

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/elianiva/meiro/internal/proc"
)

// ErrNoBrowser reports that no browser the sign-in window can use is
// installed.
var ErrNoBrowser = errors.New("no Chrome, Edge, Brave, Helium, Vivaldi or Chromium browser was found")

// ErrWindowClosed reports that the user closed the sign-in window before the
// sign-in finished.
var ErrWindowClosed = errors.New("the sign-in window was closed before you finished signing in")

// SignInTimeout is how long a sign-in window may stay open: time to type a
// password and a second factor.
const SignInTimeout = 5 * time.Minute

// WindowOptions are the settings of a sign-in window. The zero value is the
// real sign-in; tests change the rest.
type WindowOptions struct {
	// Executable is the browser to start; "" finds one on this machine.
	Executable string
	// URL is the page the window opens.
	URL string
	// Poll is how often the window is asked for its cookies.
	Poll time.Duration
	// Settle is how long the window stays open after the sign-in shows, so the
	// page can set the rest of its cookies.
	Settle time.Duration
}

// SignInWindow has the user sign in to YouTube Music in a browser window of
// its own and returns the Cookie header of that session. The window is a new,
// empty profile, so it holds nothing of the user's own browsing, and it is
// closed and deleted as soon as the session is read.
//
// The session is taken from a window that is never used again, on purpose.
// YouTube replaces the cookies of a session as long as a browser keeps it
// open, and the old ones stop working; a session read from the user's
// everyday browser goes stale in hours, one read from a window that is then
// closed does not.
func SignInWindow(ctx context.Context, options WindowOptions) (string, error) {
	if options.URL == "" {
		options.URL = "https://music.youtube.com"
	}
	if options.Poll == 0 {
		options.Poll = time.Second
	}
	if options.Settle == 0 {
		options.Settle = 3 * time.Second
	}
	exe := options.Executable
	if exe == "" {
		var ok bool
		if exe, ok = findChromium(ctx); !ok {
			return "", ErrNoBrowser
		}
	}
	profile, err := proc.MkdirTemp("signin-")
	if err != nil {
		return "", err
	}
	defer removeAll(profile)

	ctx, cancel := context.WithTimeout(ctx, SignInTimeout)
	defer cancel()
	cmd := proc.Command(ctx, exe, windowArgs(profile, options.URL)...)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start the browser: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	// Whatever happens, the window does not outlive the sign-in.
	defer func() {
		cancel()
		<-exited
	}()

	port, path, err := waitDebugPort(ctx, profile, exited)
	if err != nil {
		return "", err
	}
	ws, err := dialWS(ctx, net.JoinHostPort("127.0.0.1", port), path)
	if err != nil {
		return "", err
	}
	defer ws.Close()
	tools := &devtools{ws: ws}

	header, err := waitSignIn(ctx, tools, options, exited)
	if err != nil {
		return "", err
	}
	// A polite quit lets the browser write what it holds; the cookie is read
	// already, so a slow one is stopped by the deferred cancel.
	closeCtx, stop := context.WithTimeout(ctx, 2*time.Second)
	tools.closeBrowser(closeCtx)
	stop()
	return header, nil
}

// windowArgs are the browser's flags. The profile is new, so no first-run
// page, default-browser prompt or keyring prompt gets in the way of the
// sign-in. The window is not "incognito": a private window keeps its cookies
// in a separate store the debugging port does not read by default, and this
// profile is deleted just the same.
func windowArgs(profile, url string) []string {
	args := []string{
		"--user-data-dir=" + profile,
		"--remote-debugging-port=0",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-sync",
		// A browser with a debugging port reports navigator.webdriver as
		// true, which Google reads as software automation and answers with
		// "This browser or app may not be secure". Here a person signs in,
		// and the debugging port only reads the cookies afterwards, so the
		// page is told what a normal browser would say.
		"--disable-blink-features=AutomationControlled",
	}
	switch runtime.GOOS {
	case "linux":
		args = append(args, "--password-store=basic")
	case "darwin":
		args = append(args, "--use-mock-keychain")
	}
	return append(args, url)
}

// waitDebugPort waits for the browser to say where its debugging port is.
// The browser writes DevToolsActivePort into the profile: the port, then the
// path of the browser's endpoint.
func waitDebugPort(ctx context.Context, profile string, exited <-chan struct{}) (port, path string, err error) {
	file := filepath.Join(profile, "DevToolsActivePort")
	for {
		if data, readErr := os.ReadFile(file); readErr == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) >= 2 {
				if _, convErr := strconv.Atoi(strings.TrimSpace(lines[0])); convErr == nil {
					return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), nil
				}
			}
		}
		select {
		case <-exited:
			return "", "", errors.New("the browser quit before it opened the sign-in window")
		case <-ctx.Done():
			return "", "", windowContextError(ctx)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// waitSignIn polls the window's cookies until a session shows, then lets the
// page settle and reads them again.
func waitSignIn(ctx context.Context, tools *devtools, options WindowOptions, exited <-chan struct{}) (string, error) {
	var signedAt time.Time
	for {
		all, err := tools.cookies(ctx)
		switch {
		case err != nil && ctx.Err() != nil:
			return "", windowContextError(ctx)
		case err != nil:
			// The connection drops when the user closes the window.
			return "", ErrWindowClosed
		}
		if hasSession(all) {
			if signedAt.IsZero() {
				signedAt = time.Now()
			}
			if time.Since(signedAt) >= options.Settle {
				return youtubeHeader(all, time.Now()), nil
			}
		}
		select {
		case <-exited:
			return "", ErrWindowClosed
		case <-ctx.Done():
			return "", windowContextError(ctx)
		case <-time.After(options.Poll):
		}
	}
}

// hasSession tells whether the cookies hold a signed-in YouTube session.
func hasSession(all []devtoolsCookie) bool {
	for _, c := range all {
		if c.Name == "SAPISID" && c.Value != "" && strings.HasSuffix(strings.TrimPrefix(c.Domain, "."), "youtube.com") {
			return true
		}
	}
	return false
}

func windowContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errors.New("the sign-in window stayed open too long; try again")
	}
	return ctx.Err()
}

// removeAll deletes a profile folder. A browser that is still quitting, and on
// Windows its file locks, can hold files for a moment, so it tries again.
func removeAll(dir string) {
	for range 20 {
		if os.RemoveAll(dir) == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// candidate is a Chromium-based browser that may be installed, named by the
// kind of browser it is.
type candidate struct {
	kind string
	// path is an absolute path, or a command to find on PATH.
	path string
}

// findChromium is the Chromium-based browser to open the sign-in window in:
// the user's default browser when it is one, and otherwise the first one
// installed. Firefox and Safari are not offered: the sign-in reads the
// session over Chrome's debugging protocol.
func findChromium(ctx context.Context) (string, bool) {
	for _, c := range preferring(chromiumCandidates(), defaultBrowserKind(ctx)) {
		if filepath.IsAbs(c.path) {
			if info, err := os.Stat(c.path); err == nil && !info.IsDir() {
				return c.path, true
			}
			continue
		}
		if path, err := exec.LookPath(c.path); err == nil {
			return path, true
		}
	}
	return "", false
}

// preferring moves the candidates of one kind of browser to the front. The
// others keep their order, so a default browser that is not installed in a
// form this finds, or is not Chromium-based, still leaves a browser to use.
func preferring(all []candidate, kind string) []candidate {
	if kind == "" {
		return all
	}
	out := make([]candidate, 0, len(all))
	for _, c := range all {
		if c.kind == kind {
			out = append(out, c)
		}
	}
	for _, c := range all {
		if c.kind != kind {
			out = append(out, c)
		}
	}
	return out
}

// browserKind names the Chromium-based browser an identifier belongs to, or
// "" for any other. The identifier is whatever the system calls its default
// browser: a desktop file on Linux ("google-chrome.desktop"), a bundle ID on
// macOS ("com.google.chrome") or a ProgID on Windows ("ChromeHTML").
func browserKind(id string) string {
	id = strings.ToLower(id)
	// Chromium goes before Chrome: its names, unlike Chrome's, hold both.
	for _, kind := range []struct{ kind, mark string }{
		{"chromium", "chromium"},
		{"chrome", "chrome"},
		{"edge", "edge"},
		{"edge", "msedge"},
		{"brave", "brave"},
		{"vivaldi", "vivaldi"},
		{"helium", "helium"},
	} {
		if strings.Contains(id, kind.mark) {
			return kind.kind
		}
	}
	return ""
}

// defaultBrowserKind asks the system which browser the user chose to open
// links, and names its kind when it is Chromium-based. It gives "" when the
// default is another browser or cannot be read.
func defaultBrowserKind(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "linux":
		out, err := proc.Command(ctx, "xdg-settings", "get", "default-web-browser").Output()
		if err != nil {
			return ""
		}
		return browserKind(string(out))
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		plist := filepath.Join(home, "Library", "Preferences", "com.apple.LaunchServices", "com.apple.launchservices.secure.plist")
		out, err := proc.Command(ctx, "plutil", "-convert", "json", "-o", "-", "--", plist).Output()
		if err != nil {
			return ""
		}
		return browserKind(macHTTPSHandler(out))
	case "windows":
		out, err := proc.Command(ctx, "reg", "query", `HKCU\Software\Microsoft\Windows\Shell\Associations\UrlAssociations\https\UserChoice`, "/v", "ProgId").Output()
		if err != nil {
			return ""
		}
		// The answer is "    ProgId    REG_SZ    ChromeHTML": the ID is the
		// last word.
		fields := strings.Fields(string(out))
		if len(fields) == 0 {
			return ""
		}
		return browserKind(fields[len(fields)-1])
	}
	return ""
}

// macHTTPSHandler is the bundle ID of the app that opens https links, from
// the LaunchServices preferences converted to JSON. It is "" when the user
// never chose one, which leaves Safari.
func macHTTPSHandler(prefs []byte) string {
	var parsed struct {
		Handlers []struct {
			Scheme string `json:"LSHandlerURLScheme"`
			App    string `json:"LSHandlerRoleAll"`
		} `json:"LSHandlers"`
	}
	if json.Unmarshal(prefs, &parsed) != nil {
		return ""
	}
	for _, h := range parsed.Handlers {
		if h.Scheme == "https" {
			return h.App
		}
	}
	return ""
}

func chromiumCandidates() []candidate {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		var out []candidate
		for _, apps := range []string{"/Applications", filepath.Join(home, "Applications")} {
			for _, b := range []struct{ kind, name string }{
				{"chrome", "Google Chrome"},
				{"edge", "Microsoft Edge"},
				{"brave", "Brave Browser"},
				{"chromium", "Chromium"},
				{"vivaldi", "Vivaldi"},
				{"helium", "Helium"},
			} {
				out = append(out, candidate{b.kind, filepath.Join(apps, b.name+".app", "Contents", "MacOS", b.name)})
			}
		}
		return out
	case "windows":
		var out []candidate
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			root := os.Getenv(env)
			if root == "" {
				continue
			}
			out = append(out,
				candidate{"chrome", filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe")},
				candidate{"edge", filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe")},
				candidate{"brave", filepath.Join(root, "BraveSoftware", "Brave-Browser", "Application", "brave.exe")},
				candidate{"chromium", filepath.Join(root, "Chromium", "Application", "chrome.exe")},
				candidate{"vivaldi", filepath.Join(root, "Vivaldi", "Application", "vivaldi.exe")},
				// Helium's folder is a best guess; it is not checked on a
				// Windows machine.
				candidate{"helium", filepath.Join(root, "imput", "Helium", "Application", "chrome.exe")},
				candidate{"helium", filepath.Join(root, "Helium", "Application", "chrome.exe")})
		}
		return out
	default:
		return []candidate{
			{"chrome", "google-chrome"},
			{"chrome", "google-chrome-stable"},
			{"chromium", "chromium"},
			{"chromium", "chromium-browser"},
			{"edge", "microsoft-edge"},
			{"edge", "microsoft-edge-stable"},
			{"brave", "brave-browser"},
			{"brave", "brave"},
			{"vivaldi", "vivaldi"},
			{"vivaldi", "vivaldi-stable"},
			// Helium's own packages name the command helium, the AUR one
			// helium-browser.
			{"helium", "helium"},
			{"helium", "helium-browser"},
		}
	}
}
