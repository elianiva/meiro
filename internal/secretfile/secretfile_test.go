package secretfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// keychain is a credential store held in memory. err, when set, is what
// every call fails with, as a locked or missing store does. The first Get
// signals entered, when set, and waits for release, as a store waiting for
// the user does.
type keychain struct {
	secrets  map[string][]byte
	err      error
	entered  chan struct{}
	released chan struct{}
}

func (k *keychain) Get(key string) ([]byte, error) {
	if k.entered != nil {
		close(k.entered)
		k.entered = nil
		<-k.released
	}
	if k.err != nil {
		return nil, k.err
	}
	v, ok := k.secrets[key]
	if !ok {
		return nil, mygo.ErrSecretNotFound
	}
	return v, nil
}

func (k *keychain) Set(key string, value []byte) error {
	if k.err != nil {
		return k.err
	}
	if len(value) > mygo.MaxSecretSize {
		return errors.New("too large for the credential store")
	}
	if k.secrets == nil {
		k.secrets = map[string][]byte{}
	}
	k.secrets[key] = value
	return nil
}

// cookie is larger than the credential store keeps, as a whole browser
// session's Cookie header can be.
var cookie = "SAPISID=abc; " + strings.Repeat("x", 4*mygo.MaxSecretSize)

func TestAnUnencryptedCredentialIsEncryptedInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookie.txt")
	if err := os.WriteFile(path, []byte(cookie+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &File{Path: path, Keychain: &keychain{}, Name: "cookie"}
	ctx := context.Background()
	for range 2 {
		if got, err := f.Load(ctx); err != nil || got != cookie {
			t.Fatalf("Load = %d bytes, %v; want the saved cookie", len(got), err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "SAPISID") {
		t.Error("the file still holds the cookie in plain text")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}

	// A store that lost the key, or whose user dismissed its unlock prompt,
	// is reported rather than taken for a sign-out.
	other := &File{Path: path, Keychain: &keychain{}, Name: "cookie"}
	if _, err := other.Load(ctx); err == nil || errors.Is(err, ErrNotSaved) {
		t.Errorf("without its key the file loaded %v; want an error", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("a missing key removed the file: %v", err)
	}
}

func TestAStoreThatRefusesKeepsTheSignIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookie.txt")
	if err := os.WriteFile(path, []byte(cookie), 0o600); err != nil {
		t.Fatal(err)
	}
	k := &keychain{err: errors.New("the keyring is locked")}
	f := &File{Path: path, Keychain: k, Name: "cookie"}
	ctx := context.Background()
	if got, err := f.Load(ctx); err != nil || got != cookie {
		t.Fatalf("Load = %d bytes, %v; want the saved cookie", len(got), err)
	}
	if err := f.Save(ctx, "SAPISID=new"); err == nil {
		t.Error("Save succeeded without the store")
	}
	if data, _ := os.ReadFile(path); string(data) != cookie {
		t.Errorf("the file holds %d bytes; want the old cookie, untouched", len(data))
	}

	k.err = nil
	if got, err := f.Load(ctx); err != nil || got != cookie {
		t.Fatalf("after unlocking, Load = %d bytes, %v", len(got), err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "SAPISID") {
		t.Error("an unlocked store did not encrypt the file")
	}
}

func TestWithoutAStoreTheFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "session.txt")
	f := &File{Path: path, Keychain: NoKeychain{}, Name: "lastfm-session"}
	ctx := context.Background()
	if err := f.Save(ctx, "KEY"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("stat = %v, %v; want a 0600 file", info, err)
	}
	if got, err := f.Load(ctx); err != nil || got != "KEY" {
		t.Fatalf("Load = %q, %v", got, err)
	}
	if err := f.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Load(ctx); !errors.Is(err, ErrNotSaved) {
		t.Errorf("Load after Delete = %v; want ErrNotSaved", err)
	}
}

func TestDeletingOneCredentialKeepsTheOther(t *testing.T) {
	k := &keychain{}
	dir := t.TempDir()
	signIn := &File{Path: filepath.Join(dir, "cookie.txt"), Keychain: k, Name: "credentials"}
	lastFM := &File{Path: filepath.Join(dir, "lastfm.txt"), Keychain: k, Name: "credentials"}
	ctx := context.Background()
	if err := signIn.Save(ctx, cookie); err != nil {
		t.Fatal(err)
	}
	if err := lastFM.Save(ctx, "KEY"); err != nil {
		t.Fatal(err)
	}
	if err := lastFM.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if err := lastFM.Delete(ctx); err != nil {
		t.Errorf("deleting twice: %v", err)
	}
	if _, err := lastFM.Load(ctx); !errors.Is(err, ErrNotSaved) {
		t.Errorf("Load after Delete = %v; want ErrNotSaved", err)
	}
	if got, err := signIn.Load(ctx); err != nil || got != cookie {
		t.Errorf("the other credential loads %d bytes, %v", len(got), err)
	}
}

func TestDeleteWaitsForAMigrationInProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.txt")
	if err := os.WriteFile(path, []byte("OLD"), 0o600); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	k := &keychain{entered: entered, released: make(chan struct{})}
	ctx := context.Background()
	loaded := make(chan struct{})
	go func() {
		defer close(loaded)
		_, _ = (&File{Path: path, Keychain: k, Name: "s"}).Load(ctx)
	}()
	<-entered // the migration is asking the store for the key
	deleted := make(chan error)
	go func() { deleted <- (&File{Path: path, Keychain: k, Name: "s"}).Delete(ctx) }()
	time.Sleep(50 * time.Millisecond) // a Delete that does not wait is done by now
	close(k.released)
	<-loaded
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the session came back after Delete: %v", err)
	}
}
