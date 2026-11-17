package cookies

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/elianiva/meiro/internal/ytdlp"
)

type Source struct {
	Label string
	Read  func(ctx context.Context) (string, error)
}

func Find(sources []Source, label string) (Source, bool) {
	if label == "" {
		return Source{}, false
	}
	for _, source := range sources {
		if source.Label == label {
			return source, true
		}
	}
	return Source{}, false
}

type browserImport struct {
	label     string
	selectors func() []string
}

func Sources() []Source {
	var sources []Source
	for _, browser := range browserImports() {
		sources = append(sources, Source{browser.label, func(ctx context.Context) (string, error) {
			return firstSession(ctx, browser.label, browser.selectors(), ytDlpCookie)
		}})
	}
	if helium, ok := heliumSource(); ok {
		sources = append(sources, helium)
	}
	return sources
}

func heliumSource() (Source, bool) {
	dir := heliumDir()
	if dir == "" {
		return Source{}, false
	}
	profiles := chromiumProfiles(dir)
	if len(profiles) == 0 {
		return Source{}, false
	}
	if runtime.GOOS == "darwin" {
		key := keychainKey{service: "Helium Storage Key", account: "Helium"}
		return Source{"Helium", func(ctx context.Context) (string, error) {
			return firstSession(ctx, "Helium", profiles, func(ctx context.Context, profile string) (string, error) {
				return chromiumCookie(ctx, profile, key)
			})
		}}, true
	}
	selectors := profileSelectors(chromiumSpec("chromium", sessionKeyring()), profiles, false)
	return Source{"Helium", func(ctx context.Context) (string, error) {
		return firstSession(ctx, "Helium", selectors, ytDlpCookie)
	}}, true
}

func heliumDir() string {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, "Library", "Application Support", "net.imput.helium")
	case "linux":
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(userConfigDir(home), "net.imput.helium")
	}
	return ""
}

func ytDlpKeyring(env func(string) string) string {
	if kdeSession(env) {
		return ""
	}
	return "gnomekeyring"
}

func kdeSession(env func(string) string) bool {
	if env("KDE_FULL_SESSION") != "" || env("KDE_SESSION_VERSION") != "" {
		return true
	}
	for part := range strings.SplitSeq(env("XDG_CURRENT_DESKTOP"), ":") {
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

func sessionKeyring() string { return ytDlpKeyring(os.Getenv) }

func chromiumSpec(id, keyring string) string {
	if keyring == "" {
		return id
	}
	return id + "+" + keyring
}

func browserImports() []browserImport {
	return []browserImport{
		{"Chrome", func() []string { return chromiumBrowserSelectors("chrome") }},
		{"Safari", func() []string { return []string{"safari"} }},
		{"Firefox", firefoxBrowserSelectors},
		{"Brave", func() []string { return chromiumBrowserSelectors("brave") }},
		{"Edge", func() []string { return chromiumBrowserSelectors("edge") }},
		{"Zen", zenBrowserSelectors},
	}
}

func chromiumBrowserSelectors(browser string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{chromiumSpec(browser, sessionKeyring())}
	}
	config := userConfigDir(home)
	root := chromiumProfileRoot(browser, home, config, os.Getenv("LOCALAPPDATA"), runtime.GOOS)
	return profileSelectors(chromiumSpec(browser, sessionKeyring()), chromiumProfiles(root), true)
}

func firefoxBrowserSelectors() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{"firefox"}
	}
	config := userConfigDir(home)
	return profileSelectors("firefox", firefoxProfiles(firefoxProfileRoots(home, config, runtime.GOOS)), true)
}

func zenBrowserSelectors() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	config := userConfigDir(home)
	return profileSelectors("firefox", firefoxProfiles(zenProfileRoots(home, config, runtime.GOOS)), false)
}

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

const zenFlatpak = "app.zen_browser.zen"

const signinProfilePrefix = "signin-"

func zenProfileRoots(home, config, goos string) []string {
	var roots []string
	switch goos {
	case "linux":
		roots = append(roots,
			filepath.Join(home, ".zen"),
			filepath.Join(home, ".var", "app", zenFlatpak, ".zen"),
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
				if !entry.IsDir() || strings.HasPrefix(entry.Name(), signinProfilePrefix) {
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

func ytDlpCookie(ctx context.Context, spec string) (string, error) {
	return ytdlp.BrowserCookie(ctx, spec, func(netscape string) (string, error) {
		return fromNetscape(netscape, time.Now())
	})
}

func fromNetscape(text string, now time.Time) (string, error) {
	var pairs []string
	seen := map[string]int{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), len(text)+1)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
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
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read Netscape cookies: %w", err)
	}
	return strings.Join(pairs, "; "), nil
}
