// Package secretfile keeps a credential in a file of the user data
// directory, encrypted with a key the system's credential store holds. The
// store keeps only small secrets, and a browser session is larger, so the
// file holds the credential and the store holds what opens it.
package secretfile

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/egoist/mygo"

	"github.com/elianiva/meiro/internal/fsutil"
)

// ErrNotSaved reports that nothing is saved.
var ErrNotSaved = errors.New("nothing saved")

// Keychain is the system's credential store: mygo.Secrets in the app.
type Keychain interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
}

// NoKeychain is a system without a credential store: a File keeps its
// credential in plain text, readable by its user only.
type NoKeychain struct{}

func (NoKeychain) Get(string) ([]byte, error) { return nil, errors.ErrUnsupported }
func (NoKeychain) Set(string, []byte) error   { return errors.ErrUnsupported }

// mu runs one Load, Save or Delete at a time. Load may rewrite the file, and
// Files are copied by value, so the lock cannot be a field.
var mu sync.Mutex

// File is one credential. Its key is kept in Keychain under Name, which
// several Files may share: macOS asks the user once per key after each
// update of an app without an Apple-issued Team ID. Where the
// system has no credential store, the file holds the credential in plain
// text, readable by its user only, as every version before this one did.
// Creating a File does no work, so it is cheap on the main thread; its
// methods may wait for the system to ask the user something, such as to
// unlock the keyring.
type File struct {
	Path     string
	Keychain Keychain
	Name     string
}

// sealed starts a file whose credential is encrypted. A credential in plain
// text never starts with it: it is a Cookie header or a Last.fm session key.
var sealed = []byte("meiro-sealed-v1\n")

// Load reads the credential. One an earlier version left in plain text is
// encrypted in its place; when that fails, the plain text stays and is
// still returned, so nobody is signed out by the move.
func (f *File) Load(context.Context) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	data, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotSaved
	}
	if err != nil {
		return "", err
	}
	if rest, ok := bytes.CutPrefix(data, sealed); ok {
		return f.open(rest)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", ErrNotSaved
	}
	if err := f.seal(value); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		log.Printf("%s stays unencrypted: %v", f.Path, err)
	}
	return value, nil
}

// Save keeps a credential, encrypted unless the system has no credential
// store. A store that refuses, such as one the user would not unlock, fails
// the save rather than leaving the credential in plain text.
func (f *File) Save(_ context.Context, value string) error {
	mu.Lock()
	defer mu.Unlock()
	err := f.seal(value)
	if errors.Is(err, errors.ErrUnsupported) {
		return fsutil.WriteFileAtomic(f.Path, []byte(value), 0o600)
	}
	return err
}

// Delete forgets the credential. The key stays, for the other Files that
// share it; alone it opens nothing.
func (f *File) Delete(context.Context) error {
	mu.Lock()
	defer mu.Unlock()
	if err := os.Remove(f.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// keySize is the length of an AES-256 key.
const keySize = 32

// seal writes value encrypted with the credential's key, making the key
// when the store has none.
func (f *File) seal(value string) error {
	key, err := f.Keychain.Get(f.Name)
	if errors.Is(err, mygo.ErrSecretNotFound) || err == nil && len(key) != keySize {
		key = make([]byte, keySize)
		_, _ = rand.Read(key)
		if err := f.Keychain.Set(f.Name, key); err != nil {
			return err
		}
		// Read it back: a file encrypted with a key the store did not keep
		// could never be opened.
		if kept, err := f.Keychain.Get(f.Name); err != nil || !bytes.Equal(kept, key) {
			return errors.Join(errors.New("the credential store did not keep the key"), err)
		}
	} else if err != nil {
		return err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	_, _ = rand.Read(nonce)
	data := append(bytes.Clone(sealed), nonce...)
	data = aead.Seal(data, nonce, []byte(value), []byte(f.Name))
	return fsutil.WriteFileAtomic(f.Path, data, 0o600)
}

// open decrypts what follows the sealed header.
func (f *File) open(data []byte) (string, error) {
	key, err := f.Keychain.Get(f.Name)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		// A store whose user dismissed the unlock prompt can say this too, so
		// it is reported, and the file stays for when the store has the key.
		return "", fmt.Errorf("the key of %s is not in the system credential store", f.Path)
	}
	if err != nil {
		return "", fmt.Errorf("read the key of %s from the system credential store: %w", f.Path, err)
	}
	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}
	if len(data) < aead.NonceSize() {
		return "", fmt.Errorf("%s is cut short", f.Path)
	}
	nonce, ciphertext := data[:aead.NonceSize()], data[aead.NonceSize():]
	value, err := aead.Open(nil, nonce, ciphertext, []byte(f.Name))
	if err != nil {
		return "", fmt.Errorf("%s does not open with its key: %w", f.Path, err)
	}
	return string(value), nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
