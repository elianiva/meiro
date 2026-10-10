package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/go-macos/keychain"
)

// Where the sign-in lives in the system's credential store.
const (
	secretService = "com.elianiva.meiro"
	secretAccount = "cookie"
)

var (
	// errNotSignedIn reports that no cookie is saved.
	errNotSignedIn = errors.New("not signed in")
	// errSecretNotFound reports that the system store holds no such secret.
	errSecretNotFound = errors.New("secret not found")
	// errNoCredentialStore reports that persistent credential storage is
	// unavailable on this system.
	errNoCredentialStore = errors.New("no system credential store")
)

// newCookieStore returns the store the app keeps its sign-in in: the
// operating system's credential store, with a file only its user can read
// as the fallback where file permissions protect it. It does no work itself,
// so it is cheap to call on the main thread.
func newCookieStore() (*keychainStore, error) {
	directory, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return nil, err
	}
	return &keychainStore{
		system:              secret{service: secretService, account: secretAccount},
		file:                newFileStore(filepath.Join(directory, "cookie.txt")),
		fileFallbackAllowed: runtime.GOOS != "windows",
	}, nil
}

// credentialStore is one secret of the operating system's credential store.
type credentialStore interface {
	// available reports whether the platform has a store to use.
	available() bool
	set(ctx context.Context, value string) error
	get(ctx context.Context) (string, error)
	// remove forgets the secret. A secret that is not there is not a failure.
	remove(ctx context.Context) error
}

// secret is a credential in the store the platform provides: the login
// keychain on macOS and the Secret Service on Linux.
type secret struct {
	service string
	account string
}

func (s secret) available() bool {
	if runtime.GOOS == "darwin" {
		return true
	}
	return s.program() != ""
}

// secretTool is where Linux's Secret Service is reached through, found once:
// the program does not move while the app runs, so the repeated lookups a
// credential-store call would otherwise make are needless work on the main
// thread.
var secretTool = sync.OnceValue(func() string {
	path, err := exec.LookPath("secret-tool")
	if err != nil {
		return ""
	}
	return path
})

// program returns the command the platform's store is reached through, and
// the empty string when it has none.
func (s secret) program() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	return secretTool()
}

func (s secret) set(ctx context.Context, value string) error {
	if runtime.GOOS == "darwin" {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := keychain.Set(s.service, s.account, []byte(value)); err != nil {
			return fmt.Errorf("save the sign-in in the system keychain: %w", err)
		}
		return nil
	}
	program := s.program()
	if program == "" {
		return errNoCredentialStore
	}
	command := command(ctx, program, "store", "--label="+s.service, "service", s.service, "account", s.account)
	command.Stdin = strings.NewReader(value)
	if err := command.Run(); err != nil {
		return fmt.Errorf("save the sign-in in the system keychain: %w", err)
	}
	return nil
}

func (s secret) get(ctx context.Context) (string, error) {
	if runtime.GOOS == "darwin" {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		value, err := keychain.Get(s.service, s.account)
		if errors.Is(err, keychain.ErrNotFound) {
			return "", errSecretNotFound
		}
		if err != nil {
			return "", fmt.Errorf("read the sign-in from the system keychain: %w", err)
		}
		if len(value) == 0 {
			return "", errSecretNotFound
		}
		return string(value), nil
	}
	program := s.program()
	if program == "" {
		return "", errNoCredentialStore
	}
	command := command(ctx, program, "lookup", "service", s.service, "account", s.account)
	output, err := command.Output()
	if err != nil {
		if missingSecret(err) {
			return "", errSecretNotFound
		}
		return "", fmt.Errorf("read the sign-in from the system keychain: %w", err)
	}
	value := strings.TrimRight(string(output), "\n")
	if value == "" {
		return "", errSecretNotFound
	}
	return value, nil
}

func (s secret) remove(ctx context.Context) error {
	if runtime.GOOS == "darwin" {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := keychain.Delete(s.service, s.account); err != nil && !errors.Is(err, keychain.ErrNotFound) {
			return fmt.Errorf("remove the sign-in from the system keychain: %w", err)
		}
		return nil
	}
	program := s.program()
	if program == "" {
		return nil
	}
	command := command(ctx, program, "clear", "service", s.service, "account", s.account)
	// Output captures secret-tool diagnostics in ExitError.Stderr, which is
	// needed to distinguish a missing secret from an operational failure.
	if _, err := command.Output(); err != nil && !missingSecret(err) {
		return fmt.Errorf("remove the sign-in from the system keychain: %w", err)
	}
	return nil
}

// missingSecret reports whether secret-tool failed because it holds no such
// secret. Its documented missing-secret response is exit status 1 with no
// diagnostic; other status-1 responses must remain errors.
func missingSecret(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	return exit.ExitCode() == 1 && len(strings.TrimSpace(string(exit.Stderr))) == 0
}

// cookieStore keeps the sign-in between runs.
type cookieStore interface {
	Load(ctx context.Context) (string, error)
	Save(ctx context.Context, cookie string) error
	Delete(ctx context.Context) error
}

// keychainStore keeps the sign-in, the Cookie header of the account's
// browser session, in the system's credential store. On platforms where file
// permissions protect it, it can use a file when no system store exists.
type keychainStore struct {
	system              credentialStore
	file                *fileStore
	fileFallbackAllowed bool
}

func (s *keychainStore) Load(ctx context.Context) (string, error) {
	systemAvailable := s.system.available()
	if systemAvailable {
		value, err := s.system.get(ctx)
		if err == nil {
			if err := s.file.Delete(ctx); err != nil {
				return "", fmt.Errorf("remove the obsolete sign-in file: %w", err)
			}
			return value, nil
		}
		if !errors.Is(err, errSecretNotFound) {
			// Do not read a weaker file copy when the system store refuses
			// access (for example, while it is locked). Make the user unlock
			// the store rather than silently downgrading credential protection.
			return "", err
		}
	}
	if !systemAvailable && !s.fileFallbackAllowed {
		return "", errNoCredentialStore
	}
	// Use the private-permissions file only on systems without a credential
	// store. A stale file is migrated when a store becomes available again.
	cookie, err := s.file.Load(ctx)
	if err != nil {
		return "", err
	}
	if systemAvailable {
		if err := s.system.set(ctx, cookie); err != nil {
			return "", err
		}
		if err := s.file.Delete(ctx); err != nil {
			return "", fmt.Errorf("remove the obsolete sign-in file: %w", err)
		}
		return cookie, nil
	}
	return cookie, nil
}

func (s *keychainStore) Save(ctx context.Context, cookie string) error {
	if s.system.available() {
		if err := s.system.set(ctx, cookie); err != nil {
			// Do not put a reusable browser session in a weaker file just
			// because the system store is locked or failed unexpectedly.
			return err
		}
		// The cookie does not belong in the file once the system store has it.
		if err := s.file.Delete(ctx); err != nil {
			return fmt.Errorf("remove the obsolete sign-in file: %w", err)
		}
		return nil
	}
	if !s.fileFallbackAllowed {
		return errNoCredentialStore
	}
	return s.file.Save(ctx, cookie)
}

// Delete removes the sign-in from both places, and reports every one that
// would not let go: a sign-out that leaves the session behind should say so.
func (s *keychainStore) Delete(ctx context.Context) error {
	var systemErr error
	if s.system.available() {
		systemErr = s.system.remove(ctx)
	}
	return errors.Join(systemErr, s.file.Delete(ctx))
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
