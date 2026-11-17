package proc

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func ExtendPath() {
	if runtime.GOOS != "darwin" {
		return
	}
	home, _ := os.UserHomeDir()
	user := os.Getenv("USER")
	extra := []string{
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"/usr/local/bin",
		"/opt/local/bin",
		"/run/current-system/sw/bin",
		"/nix/var/nix/profiles/default/bin",
	}
	if user != "" {
		extra = append(extra, "/etc/profiles/per-user/"+user+"/bin")
	}
	if home != "" {
		extra = append(extra,
			filepath.Join(home, ".nix-profile/bin"),
			filepath.Join(home, ".local/bin"),
			filepath.Join(home, ".bun/bin"))
	}
	os.Setenv("PATH", extendedPath(os.Getenv("PATH"), extra))
}

func extendedPath(path string, extra []string) string {
	have := map[string]bool{}
	for _, dir := range filepath.SplitList(path) {
		have[dir] = true
	}
	parts := []string{}
	if path != "" {
		parts = append(parts, path)
	}
	for _, dir := range extra {
		if have[dir] {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		have[dir] = true
		parts = append(parts, dir)
	}
	return strings.Join(parts, string(os.PathListSeparator))
}
