// Package proc runs the helper programs the app ships with, ffmpeg, yt-dlp
// and QuickJS, and keeps the private temporary files they work with.
package proc

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// commandWaitDelay bounds how long a cancelled command may keep Wait blocked
// on pipes that a surviving grandchild still holds open.
const commandWaitDelay = 5 * time.Second

// StaleAge is how old an app-owned temporary entry must be before a sweep
// removes it. It is longer than any single yt-dlp run (30 minutes), so work
// still running in another process is never touched.
const StaleAge = 2 * time.Hour

// tempRootPrefix names the private per-user directory holding every
// temporary credential or scratch file the app creates.
const tempRootPrefix = "meiro-tmp-"

// Tool finds a helper program: the copy that ships inside the app if there
// is one, so a download runs as it is, and otherwise the one on PATH, which
// is what a run from source uses.
func Tool(name string) (string, error) {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if resources, err := mygo.App.Path(mygo.PathResources); err == nil {
		bundled := filepath.Join(resources, "bin", name)
		if info, err := os.Stat(bundled); err == nil && !info.IsDir() {
			return bundled, nil
		}
	}
	return exec.LookPath(name)
}

// Command builds an exec.Cmd whose cancellation kills the whole process tree
// and whose Wait cannot hang on inherited pipes.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	hardenCommand(cmd)
	cmd.WaitDelay = commandWaitDelay
	return cmd
}

// tempRoot returns the private directory for temporary files, creating it
// with owner-only permissions. A path that is not a plain directory owned by
// this user is refused, so another user cannot pre-create it.
func tempRoot() (string, error) {
	root := filepath.Join(os.TempDir(), tempRootPrefix+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", root)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", err
	}
	return root, nil
}

// MkdirTemp creates a private temporary directory.
func MkdirTemp(pattern string) (string, error) {
	root, err := tempRoot()
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(root, pattern)
}

// CreateTemp creates a private temporary file.
func CreateTemp(pattern string) (*os.File, error) {
	root, err := tempRoot()
	if err != nil {
		return nil, err
	}
	return os.CreateTemp(root, pattern)
}

// SweepStale removes entries of dir that are older than StaleAge and whose
// name starts with one of prefixes. Newer entries belong to work that may
// still be running, possibly in another process.
func SweepStale(dir string, prefixes ...string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		matched := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(entry.Name(), prefix) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < StaleAge {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			log.Printf("removing stale %s: %v", entry.Name(), err)
		}
	}
}

// SweepTemp clears stale leftovers of a crashed run from the private
// temporary directory.
func SweepTemp() {
	if root, err := tempRoot(); err == nil {
		SweepStale(root, "")
	}
}
