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

var ErrNotSaved = errors.New("nothing saved")

type Keychain interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
}

type NoKeychain struct{}

func (NoKeychain) Get(string) ([]byte, error) { return nil, errors.ErrUnsupported }
func (NoKeychain) Set(string, []byte) error   { return errors.ErrUnsupported }

var mu sync.Mutex

type File struct {
	Path     string
	Keychain Keychain
	Name     string
}

var sealed = []byte("meiro-sealed-v1\n")

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

func (f *File) Save(_ context.Context, value string) error {
	mu.Lock()
	defer mu.Unlock()
	err := f.seal(value)
	if errors.Is(err, errors.ErrUnsupported) {
		return fsutil.WriteFileAtomic(f.Path, []byte(value), 0o600)
	}
	return err
}

func (f *File) Delete(context.Context) error {
	mu.Lock()
	defer mu.Unlock()
	if err := os.Remove(f.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

const keySize = 32

func (f *File) seal(value string) error {
	key, err := f.Keychain.Get(f.Name)
	if errors.Is(err, mygo.ErrSecretNotFound) || err == nil && len(key) != keySize {
		key = make([]byte, keySize)
		_, _ = rand.Read(key)
		if err := f.Keychain.Set(f.Name, key); err != nil {
			return err
		}
		if err := f.verifyKeyKept(key); err != nil {
			return err
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

func (f *File) verifyKeyKept(key []byte) error {
	kept, err := f.Keychain.Get(f.Name)
	if err != nil || !bytes.Equal(kept, key) {
		return errors.Join(errors.New("the credential store did not keep the key"), err)
	}
	return nil
}

func (f *File) open(data []byte) (string, error) {
	key, err := f.Keychain.Get(f.Name)
	if errors.Is(err, mygo.ErrSecretNotFound) {
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
