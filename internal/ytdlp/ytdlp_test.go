package ytdlp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveUsesNodeAndNeverBun(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"node", "bun"} {
		writeTool(t, filepath.Join(dir, name))
	}
	writeTool(t, filepath.Join(dir, "yt-dlp"))
	t.Setenv("PATH", dir)
	resetRuntime := func() {
		jsRuntime.mu.Lock()
		jsRuntime.found, jsRuntime.name, jsRuntime.args = false, "", nil
		jsRuntime.mu.Unlock()
	}
	resetRuntime()
	t.Cleanup(resetRuntime)

	_, _, run, err := Resolve(context.Background(), "video", "cookie=value")
	if err != nil {
		t.Fatal(err)
	}
	if run.JSRuntime != "node" {
		t.Errorf("with node and bun on PATH, yt-dlp ran with %q, want node", run.JSRuntime)
	}

	if err := os.Remove(filepath.Join(dir, "node")); err != nil {
		t.Fatal(err)
	}
	resetRuntime()
	_, _, run, err = Resolve(context.Background(), "video", "cookie=value")
	if err != nil {
		t.Fatal(err)
	}
	if run.JSRuntime == "bun" {
		t.Errorf("with only bun on PATH, yt-dlp ran with %q", run.JSRuntime)
	}
}

func writeTool(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho https://example.com/audio\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
