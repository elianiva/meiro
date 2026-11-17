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

var ErrNoBrowser = errors.New("no Chrome, Edge, Brave, Helium, Vivaldi, Chromium, Firefox or Zen browser was found")

var ErrNeedsHostAccess = errors.New("the Flatpak may not start programs outside its sandbox")

const HostAccessCommand = "flatpak override --user --talk-name=org.freedesktop.Flatpak com.elianiva.meiro"

var errFirefoxNeedsYtDlp = errors.New("signing in with Firefox or Zen needs yt-dlp")

var ErrWindowClosed = errors.New("the sign-in window was closed before you finished signing in")

var errWindowGone = errors.New("the sign-in window is gone")

const SignInTimeout = 5 * time.Minute

const signInURL = "https://accounts.google.com/ServiceLogin?service=youtube&uilel=3&continue=https%3A%2F%2Fwww.youtube.com%2Fsignin%3Faction_handle_signin%3Dtrue%26app%3Ddesktop%26next%3Dhttps%253A%252F%252Fmusic.youtube.com%252F"

type firefoxRetry struct {
	recheck time.Duration
	grace   time.Duration
}

const (
	browserQuitTimeout = 2 * time.Second
	firefoxReadRecheck = 5 * time.Second
	firefoxReadGrace   = 30 * time.Second
)

type WindowOptions struct {
	Executable string
	URL        string
	Poll       time.Duration
	Settle     time.Duration

	family      browserFamily
	profileRoot string
	read        profileReader
	retry       firefoxRetry
}

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

func (o WindowOptions) browser(ctx context.Context, canReadFirefox bool) (candidate, bool) {
	if o.Executable != "" {
		return candidate{family: o.family, path: o.Executable, profileRoot: o.profileRoot}, true
	}
	return findBrowser(ctx, canReadFirefox)
}

func firefoxProfileCookie(ctx context.Context, profile string) (string, error) {
	return ytDlpCookie(ctx, "firefox:"+profile)
}

func ytDlpAvailable() bool {
	_, err := proc.Tool("yt-dlp")
	return err == nil
}

const firefoxCookiePageSize = 32768

func writeFirefoxPreferences(profile, kind string) error {
	prefs := `user_pref("browser.aboutwelcome.enabled", false);
user_pref("browser.shell.checkDefaultBrowser", false);
user_pref("browser.startup.homepage_override.mstone", "ignore");
user_pref("network.cookie.db.maxWalBytes", ` + strconv.Itoa(firefoxCookiePageSize) + `);
`
	if kind == "zen" {
		prefs += "user_pref(\"zen.welcome-screen.seen\", true);\n"
	}
	return os.WriteFile(filepath.Join(profile, "user.js"), []byte(prefs), 0o600)
}

func cookieDatabaseModTime(profile string) time.Time {
	var latest time.Time
	for _, name := range []string{"cookies.sqlite", "cookies.sqlite-wal"} {
		if info, err := os.Stat(filepath.Join(profile, name)); err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}

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

func chromiumWindowArgs(profile, url string) []string {
	args := []string{
		"--user-data-dir=" + profile,
		"--remote-debugging-port=0",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-sync",
		"--disable-blink-features=AutomationControlled",
	}
	switch runtime.GOOS {
	case "linux":
		args = append(args, "--password-store=basic")
	case "darwin":
		args = append(args, "--use-mock-keychain")
	case "windows":
		args = append(args, "--edge-skip-compat-layer-relaunch")
	}
	return append(args, url)
}

func firefoxWindowArgs(profile, url string) []string {
	return []string{"-profile", profile, "-no-remote", url}
}

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

func browserHoldsProfile(path string) bool {
	for _, name := range []string{"lock", "SingletonLock"} {
		if _, err := os.Lstat(filepath.Join(path, name)); err == nil {
			return true
		}
	}
	return false
}

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
				closeCtx, stop := context.WithTimeout(ctx, browserQuitTimeout)
				tools.closeBrowser(closeCtx)
				stop()
				_ = ws.Close()
			},
		}, nil
	case firefoxFamily:
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

const (
	removeAttempts   = 20
	removeRetryDelay = 250 * time.Millisecond
)

func removeAll(dir string) {
	for range removeAttempts {
		if os.RemoveAll(dir) == nil {
			return
		}
		time.Sleep(removeRetryDelay)
	}
}
