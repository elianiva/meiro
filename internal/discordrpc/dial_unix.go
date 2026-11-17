//go:build darwin || linux

package discordrpc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type socketDialFunc func(ctx context.Context, path string) (net.Conn, error)

func dialDiscord(ctx context.Context) (net.Conn, error) {
	return dialDiscordCandidates(ctx, discordSocketDirs(
		os.Getenv("XDG_RUNTIME_DIR"),
		[]string{os.Getenv("TMPDIR"), os.Getenv("TMP"), os.Getenv("TEMP"), "/tmp"},
	), dialUnix)
}

func discordSocketDirs(runtimeDir string, fallbacks []string) []string {
	var dirs []string
	add := func(dir string) {
		if dir != "" && !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	if runtimeDir != "" {
		add(runtimeDir)
		for _, appID := range directoryNames(filepath.Join(runtimeDir, "app")) {
			add(filepath.Join(runtimeDir, "app", appID))
		}
		for _, appID := range directoryNames(filepath.Join(runtimeDir, ".flatpak")) {
			add(filepath.Join(runtimeDir, ".flatpak", appID, "xdg-run"))
		}
		for _, name := range directoryNames(runtimeDir) {
			if strings.HasPrefix(name, "snap.") {
				add(filepath.Join(runtimeDir, name))
			}
		}
	}
	for _, dir := range fallbacks {
		add(dir)
	}
	return dirs
}

func directoryNames(path string) []string {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names
}

func dialDiscordCandidates(ctx context.Context, directories []string, dial socketDialFunc) (net.Conn, error) {
	var lastErr error
	for _, directory := range directories {
		for index := range 10 {
			path := filepath.Join(directory, "discord-ipc-"+string(rune('0'+index)))
			conn, err := dial(ctx, path)
			if err == nil {
				return conn, nil
			}
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
	}
	if lastErr == nil {
		return nil, net.ErrClosed
	}
	return nil, lastErr
}

func dialUnix(ctx context.Context, path string) (net.Conn, error) {
	return (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "unix", path)
}
