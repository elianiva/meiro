//go:build darwin || linux

package mcpcontrol

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

func endpoint() (string, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	socket := func(base string) string {
		return filepath.Join(base, "meiro-mcp-"+strconv.Itoa(os.Getuid()), "control.sock")
	}
	if base == "" && os.Getenv("FLATPAK_ID") != "com.elianiva.meiro" {
		base = "/tmp"
		if runtime.GOOS == "darwin" {
			if dir := os.TempDir(); filepath.IsAbs(dir) && len(socket(dir)) < 104 {
				base = dir
			}
		}
	}
	if !filepath.IsAbs(base) {
		return "", errors.New("use an absolute path for XDG_RUNTIME_DIR")
	}
	if os.Getenv("FLATPAK_ID") == "com.elianiva.meiro" {
		base = filepath.Join(base, "app", "com.elianiva.meiro")
	}
	path := socket(base)
	if len(path) >= 104 {
		return "", errors.New("use a shorter XDG_RUNTIME_DIR; the Meiro socket path is too long")
	}
	return path, nil
}

type lockedListener struct {
	net.Listener
	lock *os.File
}

func (l *lockedListener) Close() error {
	err := l.Listener.Close()
	_ = l.lock.Close()
	return err
}

func listen(ctx context.Context) (net.Listener, error) {
	path, err := endpoint()
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err := verifySocketPath(dir, unix.S_IFDIR|0o700); err != nil {
		return nil, err
	}

	lock, err := os.OpenFile(filepath.Join(dir, "listener.lock"), os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	listener, err := bind(ctx, path, lock)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	return &lockedListener{Listener: listener, lock: lock}, nil
}

func bind(ctx context.Context, path string, lock *os.File) (net.Listener, error) {
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, fmt.Errorf("could not lock the Meiro socket: %w", err)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("the Meiro socket path is occupied by another file")
		}
		conn, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(ctx, "unix", path)
		if err == nil {
			_ = conn.Close()
			return nil, errors.New("another Meiro instance is already listening")
		}
		if !errors.Is(err, unix.ECONNREFUSED) {
			return nil, err
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

func verifySocketPath(path string, mode int) error {
	var info unix.Stat_t
	if err := unix.Lstat(path, &info); err != nil {
		return err
	}
	if info.Uid != uint32(os.Getuid()) || int(info.Mode)&(unix.S_IFMT|0o7777) != mode {
		return fmt.Errorf("the Meiro socket path must belong to your user and have mode %04o: %s", mode&0o7777, path)
	}
	return nil
}

func dial(ctx context.Context) (net.Conn, error) {
	path, err := endpoint()
	if err != nil {
		return nil, err
	}
	if err := verifySocketPath(filepath.Dir(path), unix.S_IFDIR|0o700); err != nil {
		return nil, err
	}
	if err := verifySocketPath(path, unix.S_IFSOCK|0o600); err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	if err := verifyPeer(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (l *lockedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if verifyPeer(conn) == nil {
			return conn, nil
		}
		_ = conn.Close()
	}
}

func verifyPeer(conn net.Conn) error {
	socket, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("expected a Meiro Unix socket")
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return err
	}
	var uid uint32
	var peerErr error
	if err := raw.Control(func(fd uintptr) {
		uid, peerErr = peerUID(int(fd))
	}); err != nil {
		return err
	}
	if peerErr != nil {
		return peerErr
	}
	if uid != uint32(os.Getuid()) {
		return errors.New("the Meiro socket peer must be your user")
	}
	return nil
}
