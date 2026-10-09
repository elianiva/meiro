package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// importSource is a browser the sign-in can take the session from.
type importSource struct {
	label, id string
	// read gives the Cookie header of the browser's YouTube session.
	read func(ctx context.Context) (string, error)
}

// browserImport describes the profile selectors of one browser. Its selectors
// are tried in order so a signed-in secondary profile wins over an unsigned
// default profile.
type browserImport struct {
	label, id string
	selectors func() []string
}

// importSources lists the browsers to offer and keeps profile discovery shared
// across Chromium- and Firefox-based browsers.
func importSources() []importSource {
	var sources []importSource
	for _, browser := range browserImports() {
		browser := browser
		sources = append(sources, importSource{browser.label, browser.id, func(ctx context.Context) (string, error) {
			return firstSession(ctx, browser.label, browser.selectors(), ytDlpCookie)
		}})
	}
	if helium, ok := heliumSource(); ok {
		sources = append(sources, helium)
	}
	return sources
}

// heliumSource offers Helium when one of its profiles is on this machine.
// yt-dlp does not know it by name. On macOS Helium keeps its cookie key under
// a name yt-dlp does not look under, so the app reads its database itself. On
// Linux it keeps its key, and its profiles, the way Chromium does, so yt-dlp
// reads them once it is told the profile and the keyring.
func heliumSource() (importSource, bool) {
	dir := heliumDir()
	if dir == "" {
		return importSource{}, false
	}
	profiles := chromiumProfiles(dir)
	if len(profiles) == 0 {
		return importSource{}, false
	}
	if runtime.GOOS == "darwin" {
		key := keychainKey{service: "Helium Storage Key", account: "Helium"}
		return importSource{"Helium", "helium", func(ctx context.Context) (string, error) {
			return firstSession(ctx, "Helium", profiles, func(ctx context.Context, profile string) (string, error) {
				return chromiumCookie(ctx, profile, key)
			})
		}}, true
	}
	selectors := profileSelectors(chromiumSpec("chromium", sessionKeyring()), profiles, false)
	return importSource{"Helium", "helium", func(ctx context.Context) (string, error) {
		return firstSession(ctx, "Helium", selectors, ytDlpCookie)
	}}, true
}

// heliumDir is where Helium keeps its profiles, and the empty string on a
// system it does not run on.
func heliumDir() string {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, "Library", "Application Support", "net.imput.helium")
	case "linux":
		config, err := os.UserConfigDir()
		if err != nil {
			return ""
		}
		return filepath.Join(config, "net.imput.helium")
	}
	return ""
}

// ytDlpKeyring names the keyring to tell yt-dlp to use, given the session's
// environment, and is the empty string when yt-dlp's own choice is right.
//
// yt-dlp reads the desktop environment to find the keyring that holds a
// Chromium browser's cookie key, and on a session it cannot place, a bare
// Wayland compositor for one, it settles on plain text: it never looks in a
// keyring, and reports that it could not decrypt the cookies. A browser on
// such a session keeps that key in the Secret Service, which is what to name.
// A KDE session keeps a wallet of its own, which yt-dlp does know how to open,
// and it names it in more than one variable, so any of them leaves the choice
// to yt-dlp.
func ytDlpKeyring(env func(string) string) string {
	if kdeSession(env) {
		return ""
	}
	return "gnomekeyring"
}

// kdeSession reports whether the environment names a KDE session, by the signs
// yt-dlp reads: KDE in XDG_CURRENT_DESKTOP, KDE_FULL_SESSION, or
// DESKTOP_SESSION, with KDE_SESSION_VERSION telling its wallets apart.
func kdeSession(env func(string) string) bool {
	if env("KDE_FULL_SESSION") != "" || env("KDE_SESSION_VERSION") != "" {
		return true
	}
	for _, part := range strings.Split(env("XDG_CURRENT_DESKTOP"), ":") {
		if part == "KDE" {
			return true
		}
	}
	switch env("DESKTOP_SESSION") {
	case "kde", "kde4", "kde-plasma", "plasma":
		return true
	}
	return false
}

// sessionKeyring is ytDlpKeyring for the session this app runs in.
func sessionKeyring() string { return ytDlpKeyring(os.Getenv) }

// chromiumSpec names a Chromium browser for yt-dlp, with the keyring that
// holds its cookie key.
func chromiumSpec(id, keyring string) string {
	if keyring == "" {
		return id
	}
	return id + "+" + keyring
}

func browserImports() []browserImport {
	return []browserImport{
		{"Chrome", "chrome", func() []string { return chromiumBrowserSelectors("chrome") }},
		{"Safari", "safari", func() []string { return []string{"safari"} }},
		{"Firefox", "firefox", firefoxBrowserSelectors},
		{"Brave", "brave", func() []string { return chromiumBrowserSelectors("brave") }},
		{"Edge", "edge", func() []string { return chromiumBrowserSelectors("edge") }},
		{"Zen", "zen", zenBrowserSelectors},
	}
}

func chromiumBrowserSelectors(browser string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{chromiumSpec(browser, sessionKeyring())}
	}
	config, _ := os.UserConfigDir()
	root := chromiumProfileRoot(browser, home, config, os.Getenv("LOCALAPPDATA"), runtime.GOOS)
	// yt-dlp does not find the key a Chromium browser encrypts its cookies
	// with under its own default on Linux, so the keyring that holds it goes
	// with the browser.
	return profileSelectors(chromiumSpec(browser, sessionKeyring()), chromiumProfiles(root), true)
}

func firefoxBrowserSelectors() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{"firefox"}
	}
	config, _ := os.UserConfigDir()
	return profileSelectors("firefox", firefoxProfiles(firefoxProfileRoots(home, config, runtime.GOOS)), true)
}

func zenBrowserSelectors() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	config, _ := os.UserConfigDir()
	return profileSelectors("firefox", firefoxProfiles(zenProfileRoots(home, config, runtime.GOOS)), false)
}

// profileSelectors gives yt-dlp one explicit profile at a time. The native
// selector remains as a fallback for browsers yt-dlp can locate itself.
func profileSelectors(browser string, profiles []string, fallback bool) []string {
	selectors := make([]string, 0, len(profiles)+1)
	for _, profile := range profiles {
		selectors = append(selectors, browser+":"+profile)
	}
	if fallback {
		selectors = append(selectors, browser)
	}
	return uniquePaths(selectors)
}

// chromiumProfileRoot returns the user-data directory that contains Default
// and Profile * directories for a Chromium-based browser.
func chromiumProfileRoot(browser, home, config, local, goos string) string {
	if config == "" && goos == "linux" {
		config = filepath.Join(home, ".config")
	}
	var path string
	switch goos {
	case "linux":
		path = map[string]string{
			"chrome": filepath.Join(config, "google-chrome"),
			"brave":  filepath.Join(config, "BraveSoftware", "Brave-Browser"),
			"edge":   filepath.Join(config, "microsoft-edge"),
		}[browser]
	case "darwin":
		path = map[string]string{
			"chrome": filepath.Join(home, "Library", "Application Support", "Google", "Chrome"),
			"brave":  filepath.Join(home, "Library", "Application Support", "BraveSoftware", "Brave-Browser"),
			"edge":   filepath.Join(home, "Library", "Application Support", "Microsoft Edge"),
		}[browser]
	case "windows":
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		path = map[string]string{
			"chrome": filepath.Join(local, "Google", "Chrome", "User Data"),
			"brave":  filepath.Join(local, "BraveSoftware", "Brave-Browser", "User Data"),
			"edge":   filepath.Join(local, "Microsoft", "Edge", "User Data"),
		}[browser]
	}
	return path
}

// firefoxProfileRoots lists the Firefox data roots supported by yt-dlp.
func firefoxProfileRoots(home, config, goos string) []string {
	if config == "" && goos == "linux" {
		config = filepath.Join(home, ".config")
	}
	var roots []string
	switch goos {
	case "linux":
		roots = append(roots,
			filepath.Join(config, "mozilla", "firefox"),
			filepath.Join(home, ".mozilla", "firefox"),
			filepath.Join(home, ".var", "app", "org.mozilla.firefox", "config", "mozilla", "firefox"),
			filepath.Join(home, ".var", "app", "org.mozilla.firefox", ".mozilla", "firefox"),
			filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox"),
		)
	case "darwin":
		roots = append(roots, filepath.Join(home, "Library", "Application Support", "Firefox", "Profiles"))
	case "windows":
		roots = append(roots, filepath.Join(config, "Mozilla", "Firefox", "Profiles"))
	}
	return uniquePaths(roots)
}

// zenProfileRoots lists the data roots Zen uses on each desktop platform.
func zenProfileRoots(home, config, goos string) []string {
	var roots []string
	switch goos {
	case "linux":
		roots = append(roots,
			filepath.Join(home, ".zen"),
			filepath.Join(home, ".var", "app", "app.zen_browser.zen", "zen"),
		)
	case "darwin":
		roots = append(roots, filepath.Join(home, "Library", "Application Support", "zen"))
	default:
		roots = append(roots, filepath.Join(home, ".zen"))
	}
	if config != "" && goos != "darwin" {
		roots = append(roots, filepath.Join(config, "zen"))
	}
	return uniquePaths(roots)
}

// firefoxProfiles returns all profiles below the browser's data roots that
// have a Firefox cookie database.
func firefoxProfiles(roots []string) []string {
	var profiles []string
	for _, root := range roots {
		if profileHasDatabase(root, "cookies.sqlite") {
			profiles = append(profiles, root)
		}
		for _, base := range []string{root, filepath.Join(root, "Profiles")} {
			entries, err := os.ReadDir(base)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				profile := filepath.Join(base, entry.Name())
				if profileHasDatabase(profile, "cookies.sqlite") {
					profiles = append(profiles, profile)
				}
			}
		}
	}
	return uniquePaths(profiles)
}

// chromiumProfiles returns the directories of the profiles in a Chromium
// browser's data directory that have a cookie database, the default profile
// first.
func chromiumProfiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var profiles []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name != "Default" && !strings.HasPrefix(name, "Profile ") {
			continue
		}
		profile := filepath.Join(dir, name)
		if profileHasDatabase(profile, "Cookies", filepath.Join("Network", "Cookies")) {
			profiles = append(profiles, profile)
		}
	}
	// ReadDir sorts by name, which puts "Default" first.
	return profiles
}

func profileHasDatabase(profile string, databases ...string) bool {
	for _, database := range databases {
		if _, err := os.Stat(filepath.Join(profile, database)); err == nil {
			return true
		}
	}
	return false
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

// firstSession tries each place a browser may keep its session, a profile or
// the browser itself, and returns the cookie of the first signed in.
func firstSession(ctx context.Context, label string, places []string, read func(ctx context.Context, place string) (string, error)) (string, error) {
	var failure error
	for _, place := range places {
		cookie, err := read(ctx, place)
		if err == nil && strings.Contains(cookie, "SAPISID=") {
			return cookie, nil
		}
		if err != nil {
			failure = err
		}
		if ctx.Err() != nil {
			return "", errors.New("importing took too long")
		}
	}
	if failure != nil {
		return "", failure
	}
	return "", fmt.Errorf("%s has no YouTube session; sign in at music.youtube.com there first", label)
}

// ytDlpCookie has yt-dlp read the cookies of one browser, or profile, and
// returns the Cookie header for YouTube they make.
func ytDlpCookie(ctx context.Context, spec string) (string, error) {
	path, err := toolPath("yt-dlp")
	if err != nil {
		return "", errors.New("importing needs yt-dlp on PATH")
	}
	directory, err := os.MkdirTemp("", "meiro-cookies-")
	if err != nil {
		return "", err
	}
	// The cookies are a credential: the directory goes as soon as they have
	// been read.
	defer removeAll(directory)
	file := filepath.Join(directory, "cookies.txt")
	// yt-dlp needs a URL to get as far as loading the cookies, and saves them
	// when it ends, whether or not it found anything to download.
	command := exec.CommandContext(ctx, path,
		"--cookies-from-browser", spec, "--cookies", file,
		"--skip-download", "--no-warnings", "--no-playlist", "--playlist-items", "0",
		"https://music.youtube.com/")
	output, runErr := command.CombinedOutput()
	if data, err := os.ReadFile(file); err == nil {
		if cookie := cookieFromNetscape(string(data), time.Now()); cookie != "" {
			return cookie, nil
		}
	}
	if runErr != nil {
		if reason := lastError(string(output)); reason != "" {
			return "", errors.New(reason)
		}
		return "", fmt.Errorf("yt-dlp failed: %w", runErr)
	}
	return "", nil
}

// lastError returns the message of the last error yt-dlp printed.
func lastError(output string) string {
	reason := ""
	for _, line := range strings.Split(output, "\n") {
		if message, ok := strings.CutPrefix(strings.TrimSpace(line), "ERROR:"); ok {
			reason = strings.TrimSpace(message)
		}
	}
	return reason
}

// cookieFromNetscape builds the Cookie header a request to
// music.youtube.com carries from a cookies.txt file: the cookies of
// youtube.com and its subdomains that have not expired.
func cookieFromNetscape(text string, now time.Time) string {
	var pairs []string
	seen := map[string]int{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// A cookie only HTTP can read is written as a comment with a marker.
		line = strings.TrimPrefix(line, "#HttpOnly_")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 7 {
			continue
		}
		domain := strings.TrimPrefix(fields[0], ".")
		if domain != "youtube.com" && !strings.HasSuffix(domain, ".youtube.com") {
			continue
		}
		if expiry, err := strconv.ParseInt(fields[4], 10, 64); err == nil && expiry > 0 && time.Unix(expiry, 0).Before(now) {
			continue
		}
		name, value := fields[5], fields[6]
		pair := name + "=" + value
		if i, ok := seen[name]; ok {
			pairs[i] = pair
			continue
		}
		seen[name] = len(pairs)
		pairs = append(pairs, pair)
	}
	return strings.Join(pairs, "; ")
}
