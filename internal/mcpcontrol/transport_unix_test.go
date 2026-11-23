//go:build darwin || linux

package mcpcontrol

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnixSocketLifecycle(t *testing.T) {
	data, err := os.MkdirTemp("/tmp", "meiro-mcp-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(data) })
	t.Setenv("XDG_RUNTIME_DIR", data)
	path, err := endpoint()
	if err != nil || !strings.HasPrefix(path, data+"/") {
		t.Fatalf("runtime path %q: %v", path, err)
	}
	if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := listen(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for file, expected := range map[string]os.FileMode{filepath.Dir(path): 0o700, path: 0o600} {
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != expected {
			t.Fatalf("permissions for %s: %v %v", file, info, err)
		}
	}
	if second, err := listen(t.Context()); err == nil {
		_ = second.Close()
		t.Fatal("second listener acquired an active socket")
	}
	conn, err := dial(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left after close: %v", err)
	}
}

func TestUnixSocketPath(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", "/tmp")
	t.Setenv("FLATPAK_ID", "")
	if path, err := endpoint(); err != nil || !strings.HasPrefix(path, "/tmp/meiro-mcp-") {
		t.Fatalf("fallback path %q: %v", path, err)
	}
	t.Setenv("FLATPAK_ID", "com.elianiva.meiro")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if path, err := endpoint(); err != nil || !strings.HasPrefix(path, "/run/user/1000/app/com.elianiva.meiro/") {
		t.Fatalf("Flatpak path %q: %v", path, err)
	}
	t.Setenv("XDG_RUNTIME_DIR", "/"+strings.Repeat("a", 104))
	if _, err := endpoint(); err == nil {
		t.Fatal("accepted a socket path that cannot fit")
	}
}
