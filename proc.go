package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// commandWaitDelay bounds how long a cancelled command may keep Wait blocked
// on pipes that a surviving grandchild still holds open.
const commandWaitDelay = 5 * time.Second

// staleTempAge is how old an app-owned temporary entry must be before the
// startup sweep removes it. It is longer than any single yt-dlp run (30
// minutes), so work still running in another process is never touched.
const staleTempAge = 2 * time.Hour

// tempRootPrefix names the private per-user directory holding every
// temporary credential or scratch file the app creates.
const tempRootPrefix = "meiro-tmp-"

// command builds an exec.Cmd whose cancellation kills the whole process tree
// and whose Wait cannot hang on inherited pipes.
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
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

// mkdirTemp creates a private temporary directory under tempRoot.
func mkdirTemp(pattern string) (string, error) {
	root, err := tempRoot()
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(root, pattern)
}

// createTemp creates a private temporary file under tempRoot.
func createTemp(pattern string) (*os.File, error) {
	root, err := tempRoot()
	if err != nil {
		return nil, err
	}
	return os.CreateTemp(root, pattern)
}

// sweepStaleTemp removes entries of dir that are older than maxAge and whose
// name starts with one of prefixes. Entries newer than maxAge belong to work
// that may still be running, possibly in another process.
func sweepStaleTemp(dir string, maxAge time.Duration, prefixes ...string) {
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
		if err != nil || time.Since(info.ModTime()) < maxAge {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			log.Printf("removing stale %s: %v", entry.Name(), err)
		}
	}
}

// sweepTempRoot clears stale leftovers from a crashed run.
func sweepTempRoot() {
	if root, err := tempRoot(); err == nil {
		sweepStaleTemp(root, staleTempAge, "")
	}
}
