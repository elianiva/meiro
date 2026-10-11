package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const testCookie = "SAPISID=secret; SID=other"

func TestFileStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cookie.txt")
	store := newFileStore(path)

	if _, err := store.Load(ctx); !errors.Is(err, errNotSignedIn) {
		t.Fatalf("Load of an empty store = %v", err)
	}
	if err := store.Save(ctx, testCookie); err != nil {
		t.Fatal(err)
	}
	cookie, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != testCookie {
		t.Errorf("loaded %q", cookie)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("the token file is %v, want 0600", got)
	}
	if err := store.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx); !errors.Is(err, errNotSignedIn) {
		t.Errorf("Load after Delete = %v", err)
	}
	if err := store.Delete(ctx); err != nil {
		t.Errorf("deleting twice = %v", err)
	}
}
