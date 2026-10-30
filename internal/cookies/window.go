package cookies

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/elianiva/meiro/internal/proc"
)

// ErrNoBrowser reports that no browser the sign-in window can use is
// installed.
var ErrNoBrowser = errors.New("no Chrome, Edge, Brave, Helium, Vivaldi, Chromium, Firefox or Zen browser was found")

// ErrNeedsHostAccess reports that Meiro runs in a Flatpak that may not start
// programs on the host, so it can neither find nor open the user's browser.
// HostAccessCommand grants the permission.
var ErrNeedsHostAccess = errors.New("the Flatpak may not start programs outside its sandbox")

// HostAccessCommand gives this Flatpak the permission ErrNeedsHostAccess
// asks for.
const HostAccessCommand = "flatpak override --user --talk-name=org.freedesktop.Flatpak com.elianiva.meiro"

// errFirefoxNeedsYtDlp reports that a Firefox or Zen browser is installed,
// but yt-dlp is missing, so its session cannot be read.
var errFirefoxNeedsYtDlp = errors.New("signing in with Firefox or Zen needs yt-dlp")

// ErrWindowClosed reports that the user closed the sign-in window before the
// sign-in finished.
var ErrWindowClosed = errors.New("the sign-in window was closed before you finished signing in")

// errWindowGone marks a read that failed because the window is gone. A
// Chromium window reports it when its connection drops; a Firefox window
// is read from a file, and its process ending is what closes it.
var errWindowGone = errors.New("the sign-in window is gone")

// SignInTimeout is how long a sign-in window may stay open: time to type a
// password and a second factor.
const SignInTimeout = 5 * time.Minute

// signInURL is the Google sign-in page that returns to YouTube Music once the
// user signs in. It has the form of the Sign in link on youtube.com: the
// youtube.com/signin handler in "continue" is what sets the YouTube cookies. Opening it, not the YouTube Music home page, puts the
// sign-in form in front of the user at once.
const signInURL = "https://accounts.google.com/ServiceLogin?service=youtube&uilel=3&continue=https%3A%2F%2Fwww.youtube.com%2Fsignin%3Faction_handle_signin%3Dtrue%26app%3Ddesktop%26next%3Dhttps%253A%252F%252Fmusic.youtube.com%252F"

// firefoxRetry paces the Firefox profile read.
type firefoxRetry struct {
	// recheck is how long a read result of "not signed in" stays trusted
	// while the profile looks unchanged. A filesystem can record two writes
	// in one timestamp tick, so timestamps alone can miss a new cookie.
	recheck time.Duration
	// grace is how long a run of failed reads may last before the sign-in
	// reports the error. A profile that is briefly locked or slow recovers
	// on its own.
	grace time.Duration
}

const (
	firefoxReadRecheck = 5 * time.Second
	firefoxReadGrace   = 30 * time.Second
)

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

	// family is the family of the browser Executable names. It is
	// chromiumFamily, the zero value, for the Chromium browser a test
	// stands in with.
	family browserFamily
	// profileRoot is where the throwaway profile is made when Executable
	// names the browser, "" for this app's private temporary directory. A
	// test sets it to a directory it can check; a found candidate carries
	// its own.
	profileRoot string
	// read reads the Cookie header of a Firefox-family profile. It is nil
	// for the real sign-in, which reads the profile the way an import
	// does, through yt-dlp; tests replace it so they need no yt-dlp.
	read profileReader
	// retry paces the Firefox profile read. The zero value is the real
	// pacing; tests shorten it.
	retry firefoxRetry
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
		options.URL = signInURL
	}
	if options.Poll == 0 {
		options.Poll = time.Second
	}
	if options.Settle == 0 {
		options.Settle = 3 * time.Second
	}
	if options.retry.recheck == 0 {
		options.retry.recheck = firefoxReadRecheck
	}
	if options.retry.grace == 0 {
		options.retry.grace = firefoxReadGrace
	}
	if options.Executable == "" && !hostReachable(ctx) {
		return "", ErrNeedsHostAccess
	}
	read := options.read
	canReadFirefox := read != nil || ytDlpAvailable()
	b, ok := options.browser(ctx, canReadFirefox)
	if !ok {
		if !canReadFirefox && installedFirefox(ctx) {
			return "", errFirefoxNeedsYtDlp
		}
		return "", ErrNoBrowser
	}
	if b.family == firefoxFamily && read == nil {
		// A Firefox window is read the way an import is, through yt-dlp;
		// say so now, while there is no window to clean up yet.
		if _, err := proc.Tool("yt-dlp"); err != nil {
			return "", errFirefoxNeedsYtDlp
		}
		read = firefoxProfileCookie
	}
	profile, err := b.privateProfile()
	if err != nil {
		return "", err
	}
	defer removeAll(profile)

	if b.family == firefoxFamily {
		if err := writeFirefoxPreferences(profile, b.kind); err != nil {
			return "", err
		}
	}

	ctx, cancel := context.WithTimeout(ctx, SignInTimeout)
	defer cancel()
	cmd := hostCommand(ctx, b.path, b.windowArgs(profile, options.URL)...)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start the browser: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	// Whatever happens, the window does not outlive the sign-in. A Flatpak
	// wrapper execs the browser, so its sandbox stays in the process group
	// the deferred cancel kills.
	defer func() {
		cancel()
		<-exited
	}()

	w, err := b.open(ctx, profile, exited, read, options.retry)
	if err != nil {
		return "", err
	}
	header, err := waitSignIn(ctx, w.read, options, exited)
	if err != nil {
		return "", err
	}
	w.quit(ctx)
	return header, nil
}

// browser is the browser to open the sign-in window in: the executable the
// options name, which a test stands in with, or the one found on this
// machine. canReadFirefox says whether a Firefox-family browser's profile can
// be read, which a found browser must be able to do.
func (o WindowOptions) browser(ctx context.Context, canReadFirefox bool) (candidate, bool) {
	if o.Executable != "" {
		return candidate{family: o.family, path: o.Executable, profileRoot: o.profileRoot}, true
	}
	return findBrowser(ctx, canReadFirefox)
}

// firefoxProfileCookie reads a profile through yt-dlp, as an import does.
func firefoxProfileCookie(ctx context.Context, profile string) (string, error) {
	return ytDlpCookie(ctx, "firefox:"+profile)
}

// ytDlpAvailable reports whether yt-dlp is on this machine.
func ytDlpAvailable() bool {
	_, err := proc.Tool("yt-dlp")
	return err == nil
}

// writeFirefoxPreferences turns off the first-run pages a fresh profile would
// open over the sign-in page. Chromium gets the same from --no-first-run and
// --no-default-browser-check. Zen shows a welcome screen of its own until its
// preference is set.
//
// The cookie write-ahead log is capped at one page. Firefox keeps a fresh
// cookie in cookies.sqlite-wal until the log passes
// network.cookie.db.maxWalBytes, and yt-dlp copies only cookies.sqlite, so
// the sign-in would miss the cookie it just made. Firefox checkpoints every
// maxWalBytes / page size pages and its page size is 32768, so one page
// checkpoints after every write.
func writeFirefoxPreferences(profile, kind string) error {
	prefs := `user_pref("browser.aboutwelcome.enabled", false);
user_pref("browser.shell.checkDefaultBrowser", false);
user_pref("browser.startup.homepage_override.mstone", "ignore");
user_pref("network.cookie.db.maxWalBytes", 32768);
`
	if kind == "zen" {
		prefs += "user_pref(\"zen.welcome-screen.seen\", true);\n"
	}
	return os.WriteFile(filepath.Join(profile, "user.js"), []byte(prefs), 0o600)
}

// cookieDatabaseModTime is the later modification time of a Firefox profile's
// cookie database and its write-ahead log. The zero time means the profile has
// neither yet.
func cookieDatabaseModTime(profile string) time.Time {
	var latest time.Time
	for _, name := range []string{"cookies.sqlite", "cookies.sqlite-wal"} {
		if info, err := os.Stat(filepath.Join(profile, name)); err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}

// windowArgs are the flags that open url on a private profile.
func (b candidate) windowArgs(profile, url string) []string {
	switch b.family {
	case chromiumFamily:
		return chromiumWindowArgs(profile, url)
	case firefoxFamily:
		return firefoxWindowArgs(profile, url)
	default:
		panic("unknown browser family")
	}
}

// chromiumWindowArgs are the browser's flags. The profile is new, so no
// first-run page, default-browser prompt or keyring prompt gets in the way of
// the sign-in. The window is not "incognito": a private window keeps its
// cookies in a separate store the debugging port does not read by default,
// and this profile is deleted just the same.
func chromiumWindowArgs(profile, url string) []string {
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

// firefoxWindowArgs are the flags of a Firefox-family browser: a private
// profile, and a process of its own so a browser already running does not
// take the window over. Firefox writes no debugging port; its session is
// read from the cookie database the profile writes.
func firefoxWindowArgs(profile, url string) []string {
	return []string{"-profile", profile, "-no-remote", url}
}

// privateProfile creates the throwaway profile the sign-in window opens in.
// A native browser shares this app's private temporary directory. A Flatpak
// or Snap browser needs a profile under a directory its sandbox can see and
// reaches at the same path as the host.
func (b candidate) privateProfile() (string, error) {
	if b.profileRoot == "" {
		return proc.MkdirTemp(signinProfilePrefix)
	}
	if err := os.MkdirAll(b.profileRoot, 0o700); err != nil {
		return "", err
	}
	proc.SweepStale(b.profileRoot, browserHoldsProfile, signinProfilePrefix)
	return os.MkdirTemp(b.profileRoot, signinProfilePrefix)
}

// browserHoldsProfile reports whether a browser is still using the throwaway
// profile at path. Firefox writes a lock symlink and a Chromium browser
// writes SingletonLock; both go away when the browser exits. A profile a
// browser still holds must not be swept out from under it.
func browserHoldsProfile(path string) bool {
	for _, name := range []string{"lock", "SingletonLock"} {
		if _, err := os.Lstat(filepath.Join(path, name)); err == nil {
			return true
		}
	}
	return false
}

// open returns the way to read the session of a browser started on profile,
// once it is reachable. exited closes when the browser process ends. read is
// the Firefox-family profile reader; a Chromium window does not use it.
func (b candidate) open(ctx context.Context, profile string, exited <-chan struct{}, read profileReader, retry firefoxRetry) (*browserWindow, error) {
	switch b.family {
	case chromiumFamily:
		port, path, err := waitDebugPort(ctx, profile, exited)
		if err != nil {
			return nil, err
		}
		ws, err := dialWS(ctx, net.JoinHostPort("127.0.0.1", port), path)
		if err != nil {
			return nil, err
		}
		tools := &devtools{ws: ws}
		return &browserWindow{
			read: func(ctx context.Context) (string, error) {
				all, err := tools.cookies(ctx)
				if err != nil {
					return "", fmt.Errorf("%w: %w", errWindowGone, err)
				}
				if !hasSession(all) {
					return "", nil
				}
				return youtubeHeader(all, time.Now()), nil
			},
			quit: func(ctx context.Context) {
				// A polite quit lets the browser write what it holds; the
				// cookie is read already, so a slow one is stopped by the
				// deferred cancel.
				closeCtx, stop := context.WithTimeout(ctx, 2*time.Second)
				tools.closeBrowser(closeCtx)
				stop()
				_ = ws.Close()
			},
		}, nil
	case firefoxFamily:
		// The reader runs yt-dlp, which makes a request of its own, so it
		// runs when the profile's cookie store changes, or when no session
		// is cached and the recheck time has passed.
		var readAt time.Time
		var readTime time.Time
		var failedAt time.Time
		var cached string
		return &browserWindow{
			read: func(ctx context.Context) (string, error) {
				changedAt := cookieDatabaseModTime(profile)
				if changedAt.IsZero() {
					return "", nil
				}
				if !changedAt.After(readAt) && (cached != "" || time.Since(readTime) < retry.recheck) {
					return cached, nil
				}
				readTime = time.Now()
				// A window that cannot be read yet is not a closed window; its
				// process ending is what closes it. A failed read leaves readAt
				// alone so the next poll tries again, and only a run of failures
				// that outlasts the grace reports the error.
				header, err := readFirefox(ctx, read, profile, exited)
				if err != nil {
					if errors.Is(err, errWindowGone) {
						return "", err
					}
					if failedAt.IsZero() {
						failedAt = time.Now()
					}
					if time.Since(failedAt) >= retry.grace {
						return "", err
					}
					return "", nil
				}
				failedAt = time.Time{}
				readAt = changedAt
				cached = ""
				if strings.Contains(header, "SAPISID=") {
					cached = header
				}
				return cached, nil
			},
			quit: func(context.Context) {},
		}, nil
	default:
		panic("unknown browser family")
	}
}

// readFirefox runs the profile reader in the background while the poll loop
// keeps watching the window. yt-dlp makes a request of its own, so a slow
// one must not stop the loop from noticing a window the user closed.
func readFirefox(ctx context.Context, read profileReader, profile string, exited <-chan struct{}) (string, error) {
	type answer struct {
		header string
		err    error
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan answer, 1)
	go func() {
		header, err := read(ctx, profile)
		done <- answer{header, err}
	}()
	select {
	case got := <-done:
		return got.header, got.err
	case <-exited:
		return "", errWindowGone
	case <-ctx.Done():
		return "", ctx.Err()
	}
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

// waitSignIn polls the window's session until a signed-in one shows and stays
// for the settle time, then returns that Cookie header.
func waitSignIn(ctx context.Context, read func(context.Context) (string, error), options WindowOptions, exited <-chan struct{}) (string, error) {
	var signedAt time.Time
	for {
		header, err := read(ctx)
		switch {
		case err != nil && ctx.Err() != nil:
			return "", windowContextError(ctx)
		case errors.Is(err, errWindowGone):
			return "", ErrWindowClosed
		case err != nil:
			return "", err
		}
		if header != "" {
			if signedAt.IsZero() {
				signedAt = time.Now()
			}
			if time.Since(signedAt) >= options.Settle {
				return header, nil
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
