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

// importSources lists the browsers to offer: the ones yt-dlp knows by name,
// and those it does not but that keep their profiles like Chromium does.
func importSources() []importSource {
	var sources []importSource
	for _, browser := range []struct{ label, id string }{
		{"Chrome", "chrome"}, {"Safari", "safari"}, {"Firefox", "firefox"}, {"Brave", "brave"}, {"Edge", "edge"},
	} {
		sources = append(sources, importSource{browser.label, browser.id, func(ctx context.Context) (string, error) {
			return firstSession(ctx, browser.label, []string{browser.id}, ytDlpCookie)
		}})
	}
	sources = append(sources, importSource{"Zen", "zen", func(ctx context.Context) (string, error) {
		return firstSession(ctx, "Zen", zenBrowserProfiles(), ytDlpCookie)
	}})
	if home, err := os.UserHomeDir(); err == nil && runtime.GOOS == "darwin" {
		dir := filepath.Join(home, "Library", "Application Support", "net.imput.helium")
		if profiles := chromiumProfiles(dir); len(profiles) > 0 {
			key := keychainKey{service: "Helium Storage Key", account: "Helium"}
			sources = append(sources, importSource{"Helium", "helium", func(ctx context.Context) (string, error) {
				return firstSession(ctx, "Helium", profiles, func(ctx context.Context, profile string) (string, error) {
					return chromiumCookie(ctx, profile, key)
				})
			}})
		}
	}
	return sources
}

// zenBrowserProfiles gives yt-dlp every Zen profile root it supports. Zen is
// Firefox-based, but yt-dlp does not know its data locations itself.
func zenBrowserProfiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	config, _ := os.UserConfigDir()
	roots := zenProfileRoots(home, config, runtime.GOOS)
	profiles := make([]string, 0, len(roots))
	for _, root := range roots {
		profiles = append(profiles, "firefox:"+root)
	}
	return profiles
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
	case "windows":
	default:
		roots = append(roots, filepath.Join(home, ".zen"))
	}
	if config != "" && goos != "darwin" {
		roots = append(roots, filepath.Join(config, "zen"))
	}
	unique := make(map[string]struct{}, len(roots))
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		if root == "" {
			continue
		}
		if _, ok := unique[root]; ok {
			continue
		}
		unique[root] = struct{}{}
		out = append(out, root)
	}
	return out
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
		for _, database := range []string{"Cookies", filepath.Join("Network", "Cookies")} {
			if _, err := os.Stat(filepath.Join(profile, database)); err == nil {
				profiles = append(profiles, profile)
				break
			}
		}
	}
	// ReadDir sorts by name, which puts "Default" first.
	return profiles
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
