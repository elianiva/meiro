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

const commandWaitDelay = 5 * time.Second

const StaleAge = 2 * time.Hour

const tempRootPrefix = "meiro-tmp-"

func Tool(name string) (string, error) {
	if bundled, ok := bundledPath(name); ok {
		return bundled, nil
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return exec.LookPath(name)
}

func Bundled(name string) bool {
	_, ok := bundledPath(name)
	return ok
}

func bundledPath(name string) (string, bool) {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	resources, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return "", false
	}
	path := filepath.Join(resources, "bin", name)
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path, true
	}
	return "", false
}

func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	hardenCommand(cmd)
	cmd.WaitDelay = commandWaitDelay
	return cmd
}

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

func MkdirTemp(pattern string) (string, error) {
	root, err := tempRoot()
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(root, pattern)
}

func SweepStale(dir string, keep func(path string) bool, prefixes ...string) {
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
		path := filepath.Join(dir, entry.Name())
		if keep != nil && keep(path) {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			log.Printf("removing stale %s: %v", entry.Name(), err)
		}
	}
}

func SweepTemp() {
	if root, err := tempRoot(); err == nil {
		SweepStale(root, nil, "")
	}
}
