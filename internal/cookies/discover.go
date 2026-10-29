package cookies

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/elianiva/meiro/internal/proc"
)

// browserFamily tells how the sign-in window reads the session of a browser.
type browserFamily uint8

const (
	// chromiumFamily reads the session over the Chrome DevTools Protocol.
	chromiumFamily browserFamily = iota
	// firefoxFamily reads the session from the browser's cookie store, the
	// same way importing from Firefox does.
	firefoxFamily
)

// profileReader reads the Cookie header of a Firefox-family profile.
type profileReader func(ctx context.Context, profile string) (string, error)

// browserWindow is a started sign-in window whose session can be read.
type browserWindow struct {
	// read returns the Cookie header of the session the window holds, ""
	// while the user is not signed in. A Chromium read fails when the
	// connection drops, which is how a window the user closed shows; a
	// Firefox read fails when the profile cannot be read.
	read func(ctx context.Context) (string, error)
	// quit lets the browser close politely before the deferred cancel stops
	// it. A browser with no remote close does nothing here.
	quit func(ctx context.Context)
}

// candidate is a browser that may open the sign-in window.
type candidate struct {
	// kind names the browser for default-browser preference.
	kind string
	// family tells how its session is read.
	family browserFamily
	// path is an absolute path, or a command to find on PATH.
	path string
	// profileRoot is the directory the throwaway profile is made under. ""
	// means this app's private temporary directory, which a native install
	// shares. A Flatpak or Snap browser needs a directory its sandbox sees:
	// Flatpak mounts ~/.var/app/<id> and Snap ~/snap/<name> at the same path
	// on both sides.
	profileRoot string
	// flatpakID and snapName name a packaged install, "" for a native one.
	flatpakID string
	snapName  string
	// desktopID is the .desktop file of this install on Linux, "" for a
	// native one. Flatpak exports <flatpakID>.desktop, Snap
	// <snapName>_<snapName>.desktop.
	desktopID string
}

// findBrowser is the browser to open the sign-in window in: the user's
// default browser when it is one of these, and otherwise the first installed.
// A Firefox-family browser is skipped unless canReadFirefox says its profile
// can be read.
func findBrowser(ctx context.Context, canReadFirefox bool) (candidate, bool) {
	id := defaultBrowserID(ctx)
	for _, c := range preferring(browserCandidates(), id, browserKind(id)) {
		if !canReadFirefox && c.family == firefoxFamily {
			continue
		}
		if c.installed(ctx) {
			return c, true
		}
	}
	return candidate{}, false
}

// installedFirefox reports whether any Firefox-family browser is installed.
func installedFirefox(ctx context.Context) bool {
	for _, c := range browserCandidates() {
		if c.family == firefoxFamily && c.installed(ctx) {
			return true
		}
	}
	return false
}

// browserCandidates lists the browsers of this system, Chromium-based ones
// first so a machine with no readable default keeps the browser it used
// before.
func browserCandidates() []candidate {
	table := browserTable()
	switch runtime.GOOS {
	case "darwin":
		return darwinCandidates(table)
	case "windows":
		return windowsCandidates(table)
	default:
		return linuxCandidates(table)
	}
}

// installed reports whether the browser is on this machine. An absolute path
// is checked as it is; anything else is a command on PATH. The package
// manager is asked as well, because an export or snap link can outlive an
// uninstall and must not win over a working native browser.
func (b candidate) installed(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if sandboxed() {
		if hostCommand(ctx, "sh", "-c", `command -v -- "$1"`, "sh", b.path).Run() != nil {
			return false
		}
	} else if filepath.IsAbs(b.path) {
		info, err := os.Stat(b.path)
		if err != nil || info.IsDir() {
			return false
		}
	} else if _, err := exec.LookPath(b.path); err != nil {
		return false
	}
	switch {
	case b.flatpakID != "":
		return hostCommand(ctx, "flatpak", "info", b.flatpakID).Run() == nil
	case b.snapName != "":
		return hostCommand(ctx, "snap", "list", b.snapName).Run() == nil
	}
	return true
}

// preferring puts the browser the default names first: an exact .desktop
// match, then every candidate of the named kind, then the rest in table
// order. The named kind moves up whole so a native install, a macOS bundle ID
// and a Windows ProgID all resolve, and so a gone Flatpak or Snap install
// does not hide the native one behind it.
func preferring(all []candidate, desktopID, kind string) []candidate {
	named := func(c candidate) bool { return desktopID != "" && c.desktopID == desktopID }
	out := make([]candidate, 0, len(all))
	for _, c := range all {
		if named(c) {
			out = append(out, c)
		}
	}
	for _, c := range all {
		if !named(c) && c.kind == kind {
			out = append(out, c)
		}
	}
	for _, c := range all {
		if !named(c) && c.kind != kind {
			out = append(out, c)
		}
	}
	return out
}

// browserKind names the browser an identifier belongs to, or "" for one this
// app cannot drive. The identifier is whatever the system calls its default
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
		{"firefox", "firefox"},
		{"zen", "zen"},
	} {
		if strings.Contains(id, kind.mark) {
			return kind.kind
		}
	}
	return ""
}

// defaultBrowserID asks the system which browser the user chose to open
// links. It returns the lowercased identifier the system uses: the .desktop
// file on Linux, the bundle ID on macOS, the ProgID on Windows, or "" when
// it cannot be read.
func defaultBrowserID(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "linux":
		out, err := hostCommand(ctx, "xdg-settings", "get", "default-web-browser").Output()
		if err != nil {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(string(out)))
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
		return strings.ToLower(macHTTPSHandler(out))
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
		return strings.ToLower(fields[len(fields)-1])
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
		if h.Scheme == "https" && h.App != "" {
			return h.App
		}
	}
	return ""
}

// browserSpec is one browser the sign-in window can start and every
// install of it this app knows. One row per browser.
type browserSpec struct {
	kind      string
	family    browserFamily // the zero value is chromiumFamily
	commands  []string      // the PATH names of a native install
	flatpakID string        // the Flatpak application ID, "" when none
	snapName  string        // the Snap name, "" when none
	macApp    string        // the app bundle name on macOS
	macExec   string        // its executable, when that differs from macApp
	winPaths  []string      // paths under each Windows install root
}

// browserTable lists every browser the sign-in window can start, the
// Chromium-based rows first so the no-default fallback keeps the browser it
// used before.
func browserTable() []browserSpec {
	return []browserSpec{
		{
			kind:      "chrome",
			commands:  []string{"google-chrome", "google-chrome-stable"},
			flatpakID: "com.google.Chrome",
			macApp:    "Google Chrome",
			winPaths:  []string{filepath.Join("Google", "Chrome", "Application", "chrome.exe")},
		},
		{
			kind:      "chromium",
			commands:  []string{"chromium", "chromium-browser"},
			flatpakID: "org.chromium.Chromium",
			snapName:  "chromium",
			macApp:    "Chromium",
			winPaths:  []string{filepath.Join("Chromium", "Application", "chrome.exe")},
		},
		{
			kind:      "edge",
			commands:  []string{"microsoft-edge", "microsoft-edge-stable"},
			flatpakID: "com.microsoft.Edge",
			macApp:    "Microsoft Edge",
			winPaths:  []string{filepath.Join("Microsoft", "Edge", "Application", "msedge.exe")},
		},
		{
			kind:      "brave",
			commands:  []string{"brave-browser", "brave"},
			flatpakID: "com.brave.Browser",
			macApp:    "Brave Browser",
			winPaths:  []string{filepath.Join("BraveSoftware", "Brave-Browser", "Application", "brave.exe")},
		},
		{
			kind:      "vivaldi",
			commands:  []string{"vivaldi", "vivaldi-stable"},
			flatpakID: "com.vivaldi.Vivaldi",
			macApp:    "Vivaldi",
			winPaths:  []string{filepath.Join("Vivaldi", "Application", "vivaldi.exe")},
		},
		{
			kind:     "helium",
			commands: []string{"helium", "helium-browser"},
			macApp:   "Helium",
			// Helium's folder is a best guess; it is not checked on a
			// Windows machine.
			winPaths: []string{
				filepath.Join("imput", "Helium", "Application", "chrome.exe"),
				filepath.Join("Helium", "Application", "chrome.exe"),
			},
		},
		{
			kind:      "firefox",
			family:    firefoxFamily,
			commands:  []string{"firefox"},
			flatpakID: "org.mozilla.firefox",
			snapName:  "firefox",
			macApp:    "Firefox",
			macExec:   "firefox",
			winPaths:  []string{filepath.Join("Mozilla Firefox", "firefox.exe")},
		},
		{
			kind:      "zen",
			family:    firefoxFamily,
			commands:  []string{"zen", "zen-browser"},
			flatpakID: zenFlatpak,
			macApp:    "Zen",
			macExec:   "zen",
		},
	}
}

// linuxCandidates builds the native, Flatpak and Snap installs of every row.
// The Flatpak and Snap tools gate their candidates, so a machine without
// them, and a test with an empty PATH, sees neither install kind.
func linuxCandidates(table []browserSpec) []candidate {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	userExports := ""
	if home != "" {
		userExports = filepath.Join(userDataHome(home), "flatpak", "exports", "bin")
	}
	_, flatpakErr := exec.LookPath("flatpak")
	_, snapErr := exec.LookPath("snap")
	hasFlatpak := flatpakErr == nil
	hasSnap := snapErr == nil
	// The sandbox shares neither the host's tools nor its /tmp. The host is
	// asked about each install, and a native browser gets its profile in the
	// cache directory, which is at the same path on both sides.
	nativeRoot := ""
	if sandboxed() {
		hasFlatpak, hasSnap = true, true
		nativeRoot, _ = os.UserCacheDir()
	}
	var out []candidate
	for _, spec := range table {
		for _, command := range spec.commands {
			out = append(out, candidate{kind: spec.kind, family: spec.family, path: command, profileRoot: nativeRoot})
		}
		if home == "" {
			continue
		}
		if hasFlatpak && spec.flatpakID != "" {
			for _, root := range []string{userExports, "/var/lib/flatpak/exports/bin"} {
				out = append(out, candidate{
					kind:        spec.kind,
					family:      spec.family,
					path:        filepath.Join(root, spec.flatpakID),
					profileRoot: filepath.Join(home, ".var", "app", spec.flatpakID, "data"),
					desktopID:   strings.ToLower(spec.flatpakID) + ".desktop",
					flatpakID:   spec.flatpakID,
				})
			}
		}
		if hasSnap && spec.snapName != "" {
			out = append(out, candidate{
				kind:        spec.kind,
				family:      spec.family,
				path:        filepath.Join("/snap/bin", spec.snapName),
				profileRoot: filepath.Join(home, "snap", spec.snapName, "common"),
				desktopID:   strings.ToLower(spec.snapName+"_"+spec.snapName) + ".desktop",
				snapName:    spec.snapName,
			})
		}
	}
	return out
}

// userDataHome is the directory Flatpak names XDG_DATA_HOME: its own variable
// when set, and the ~/.local/share default otherwise.
func userDataHome(home string) string {
	if data := os.Getenv(hostVariable("XDG_DATA_HOME")); data != "" {
		return data
	}
	return filepath.Join(home, ".local", "share")
}

// userConfigDir is where the user's browsers keep their profiles.
func userConfigDir(home string) string {
	if !sandboxed() {
		config, _ := os.UserConfigDir()
		return config
	}
	if config := os.Getenv(hostVariable("XDG_CONFIG_HOME")); config != "" {
		return config
	}
	return filepath.Join(home, ".config")
}

// hostVariable names the variable that holds the host's value of an XDG base
// directory. Inside a Flatpak the plain name points at this app's own
// directory, and Flatpak passes the host's value with a HOST_ prefix.
func hostVariable(name string) string {
	if sandboxed() {
		return "HOST_" + name
	}
	return name
}

// sandboxed reports whether this app runs in a Flatpak, where the user's
// browsers, their package managers and xdg-settings are on the host.
func sandboxed() bool { return os.Getenv("FLATPAK_ID") != "" }

// hostReachable reports whether hostCommand can run anything. A Flatpak
// reaches the host only when the user granted it org.freedesktop.Flatpak,
// which the bundle does not ask for.
func hostReachable(ctx context.Context) bool {
	if !sandboxed() {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return hostCommand(ctx, "true").Run() == nil
}

// hostCommand is proc.Command run where the user's browsers are. Inside a
// Flatpak it goes through flatpak-spawn, whose --watch-bus ends the host
// command when flatpak-spawn is killed.
func hostCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	if !sandboxed() {
		return proc.Command(ctx, name, args...)
	}
	return proc.Command(ctx, "flatpak-spawn", append([]string{"--host", "--watch-bus", name}, args...)...)
}

// darwinCandidates builds the app bundle install of every row under each
// Applications directory.
func darwinCandidates(table []browserSpec) []candidate {
	roots := []string{"/Applications"}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, "Applications"))
	}
	var out []candidate
	for _, spec := range table {
		binary := spec.macExec
		if binary == "" {
			binary = spec.macApp
		}
		for _, root := range roots {
			out = append(out, candidate{
				kind:   spec.kind,
				family: spec.family,
				path:   filepath.Join(root, spec.macApp+".app", "Contents", "MacOS", binary),
			})
		}
	}
	return out
}

// windowsCandidates builds every install of every row under each Windows
// install root.
func windowsCandidates(table []browserSpec) []candidate {
	var out []candidate
	for _, spec := range table {
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			root := os.Getenv(env)
			if root == "" {
				continue
			}
			for _, path := range spec.winPaths {
				out = append(out, candidate{
					kind:   spec.kind,
					family: spec.family,
					path:   filepath.Join(root, path),
				})
			}
		}
	}
	return out
}
