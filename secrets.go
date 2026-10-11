package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
)

// errNotSignedIn reports that no cookie is saved.
var errNotSignedIn = errors.New("not signed in")

// newCookieStore returns the store the app keeps its sign-in in: a file in
// the user's data directory that only its user can read. It does no work
// itself, so it is cheap to call on the main thread.
func newCookieStore() (*fileStore, error) {
	directory, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return nil, err
	}
	return newFileStore(filepath.Join(directory, "cookie.txt")), nil
}

// cookieStore keeps the sign-in between runs.
type cookieStore interface {
	Load(ctx context.Context) (string, error)
	Save(ctx context.Context, cookie string) error
	Delete(ctx context.Context) error
}

// fileStore keeps the cookie in a file only its user can read.
type fileStore struct {
	path string
}

func newFileStore(path string) *fileStore { return &fileStore{path: path} }

func (s *fileStore) Load(context.Context) (string, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", errNotSignedIn
	}
	if err != nil {
		return "", err
	}
	cookie := strings.TrimSpace(string(data))
	if cookie == "" {
		return "", errNotSignedIn
	}
	return cookie, nil
}

func (s *fileStore) Save(_ context.Context, cookie string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	// The cookie is a credential: keep it readable by this user only.
	return writeFileAtomic(s.path, []byte(cookie), 0o600)
}

func (s *fileStore) Delete(context.Context) error {
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
