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

type browserFamily uint8

const (
	chromiumFamily browserFamily = iota
	firefoxFamily
)

type profileReader func(ctx context.Context, profile string) (string, error)

type browserWindow struct {
	read func(ctx context.Context) (string, error)
	quit func(ctx context.Context)
}

type candidate struct {
	kind        string
	family      browserFamily
	path        string
	profileRoot string
	flatpakID   string
	snapName    string
	desktopID   string
}

func findBrowser(ctx context.Context, canReadFirefox bool) (candidate, bool) {
	id := defaultBrowserID(ctx)
	for _, c := range preferring(browserCandidates(), id, defaultBrowserKind(ctx, id)) {
		if !canReadFirefox && c.family == firefoxFamily {
			continue
		}
		if c.installed(ctx) {
			return c, true
		}
	}
	return candidate{}, false
}

const systemQueryTimeout = 3 * time.Second

func installedFirefox(ctx context.Context) bool {
	for _, c := range browserCandidates() {
		if c.family == firefoxFamily && c.installed(ctx) {
			return true
		}
	}
	return false
}

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

func (b candidate) installed(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, systemQueryTimeout)
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

func browserKind(id string) string {
	id = strings.ToLower(id)
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

func defaultBrowserKind(ctx context.Context, id string) string {
	kind := browserKind(id)
	if runtime.GOOS != "windows" || kind != "firefox" {
		return kind
	}
	return windowsDefaultKind(kind, windowsDefaultExecutable(ctx, id))
}

func defaultBrowserID(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, systemQueryTimeout)
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
		fields := strings.Fields(string(out))
		if len(fields) == 0 {
			return ""
		}
		return strings.ToLower(fields[len(fields)-1])
	}
	return ""
}

func windowsDefaultKind(kind, executable string) string {
	switch strings.ToLower(executableBase(executable)) {
	case "zen.exe":
		return "zen"
	case "firefox.exe":
		return "firefox"
	}
	return kind
}

func windowsDefaultExecutable(ctx context.Context, id string) string {
	ctx, cancel := context.WithTimeout(ctx, systemQueryTimeout)
	defer cancel()
	out, err := proc.Command(ctx, "reg", "query", `HKCR\`+id+`\shell\open\command`, "/ve").Output()
	if err != nil {
		return ""
	}
	return commandExecutable(string(out))
}

func commandExecutable(output string) string {
	_, command, ok := strings.Cut(output, "REG_SZ")
	if !ok {
		return ""
	}
	command = strings.TrimSpace(command)
	if strings.HasPrefix(command, `"`) {
		end := strings.Index(command[1:], `"`)
		if end < 0 {
			return ""
		}
		return command[1 : end+1]
	}
	if end := strings.IndexAny(command, " \t\r\n"); end >= 0 {
		return command[:end]
	}
	return command
}

func executableBase(path string) string {
	if i := strings.LastIndexAny(path, `\/`); i >= 0 {
		return path[i+1:]
	}
	return path
}

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

type browserSpec struct {
	kind      string
	family    browserFamily
	commands  []string
	flatpakID string
	snapName  string
	macApp    string
	macExec   string
	winPaths  []string
}

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
			winPaths:  []string{filepath.Join("Zen Browser", "zen.exe")},
		},
	}
}

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

func userDataHome(home string) string {
	if data := os.Getenv(hostVariable("XDG_DATA_HOME")); data != "" {
		return data
	}
	return filepath.Join(home, ".local", "share")
}

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

func hostVariable(name string) string {
	if sandboxed() {
		return "HOST_" + name
	}
	return name
}

func sandboxed() bool { return os.Getenv("FLATPAK_ID") != "" }

func hostReachable(ctx context.Context) bool {
	if !sandboxed() {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, systemQueryTimeout)
	defer cancel()
	return hostCommand(ctx, "true").Run() == nil
}

func hostCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	if !sandboxed() {
		return proc.Command(ctx, name, args...)
	}
	return proc.Command(ctx, "flatpak-spawn", append([]string{"--host", "--watch-bus", name}, args...)...)
}

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
