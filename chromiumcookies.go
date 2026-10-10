package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1" //nolint:gosec // Chromium derives its cookie key with PBKDF2-HMAC-SHA1
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A Chromium browser encrypts its cookies with a key it keeps in the macOS
// keychain. yt-dlp finds that key under the name "<browser> Safe Storage",
// which browsers with a key of their own, Helium among them, do not use; so
// for those the app reads the cookie database itself.

// keychainKey names a browser's key in the macOS keychain.
type keychainKey struct{ service, account string }

// chromiumCookie reads the cookie of the YouTube session in a Chromium
// profile: the profile's directory, and where its browser keeps the key.
func chromiumCookie(ctx context.Context, profile string, key keychainKey) (string, error) {
	aesKey, err := chromiumKey(ctx, key)
	if err != nil {
		return "", err
	}
	return readChromiumCookies(ctx, profile, aesKey)
}

// readChromiumCookies reads the YouTube cookies of a profile, decrypting them
// with the key of the browser.
func readChromiumCookies(ctx context.Context, profile string, aesKey []byte) (string, error) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		return "", errors.New("importing needs sqlite3 on PATH")
	}
	database := ""
	for _, name := range []string{"Cookies", filepath.Join("Network", "Cookies")} {
		if _, err := os.Stat(filepath.Join(profile, name)); err == nil {
			database = filepath.Join(profile, name)
			break
		}
	}
	if database == "" {
		return "", fmt.Errorf("%s has no cookie database", profile)
	}
	// The browser holds its database open, and writes recent cookies to a
	// journal beside it: both are read from a copy.
	directory, err := mkdirTemp("meiro-cookies-")
	if err != nil {
		return "", err
	}
	defer removeAll(directory)
	copied := filepath.Join(directory, "Cookies")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		err := copyFile(database+suffix, copied+suffix)
		if errors.Is(err, os.ErrNotExist) && suffix != "" {
			continue
		}
		if err != nil {
			return "", err
		}
	}

	query := func(statement string) ([]byte, error) {
		out, err := command(ctx, sqlite, "-readonly", "-json", copied, statement).Output()
		if err != nil {
			return nil, fmt.Errorf("read the cookie database: %w", err)
		}
		return out, nil
	}
	version := 0
	if out, err := query("SELECT value FROM meta WHERE key = 'version'"); err == nil {
		var rows []struct{ Value string }
		if json.Unmarshal(out, &rows) == nil && len(rows) > 0 {
			version, _ = strconv.Atoi(rows[0].Value)
		}
	}
	out, err := query("SELECT host_key AS host, name, hex(encrypted_value) AS encrypted, value, expires_utc AS expires FROM cookies WHERE host_key LIKE '%youtube.com'")
	if err != nil {
		return "", err
	}
	var rows []struct {
		Host      string
		Name      string
		Encrypted string
		Value     string
		Expires   int64
	}
	if len(bytes.TrimSpace(out)) > 0 {
		if err := json.Unmarshal(out, &rows); err != nil {
			return "", fmt.Errorf("read the cookie database: %w", err)
		}
	}

	now := time.Now()
	var pairs []string
	seen := map[string]int{}
	for _, row := range rows {
		host := strings.TrimPrefix(row.Host, ".")
		if host != "youtube.com" && !strings.HasSuffix(host, ".youtube.com") {
			continue
		}
		// Chromium counts expiry in microseconds since 1601.
		if row.Expires > 0 && time.Unix(row.Expires/1_000_000-11_644_473_600, 0).Before(now) {
			continue
		}
		value := row.Value
		if value == "" && row.Encrypted != "" {
			encrypted, err := hex.DecodeString(row.Encrypted)
			if err != nil {
				continue
			}
			plain, ok := decryptChromium(aesKey, encrypted, row.Host, version)
			if !ok {
				continue
			}
			value = plain
		}
		pair := row.Name + "=" + value
		if i, ok := seen[row.Name]; ok {
			pairs[i] = pair
			continue
		}
		seen[row.Name] = len(pairs)
		pairs = append(pairs, pair)
	}
	return strings.Join(pairs, "; "), nil
}

// chromiumKey derives the key a Chromium browser on macOS encrypts its
// cookies with from the password it keeps in the keychain, which asks the
// user before it answers.
func chromiumKey(ctx context.Context, key keychainKey) ([]byte, error) {
	out, err := command(ctx, "security", "find-generic-password", "-w", "-s", key.service, "-a", key.account).Output()
	if err != nil {
		return nil, fmt.Errorf("the keychain did not give the browser's key: %w", err)
	}
	return deriveChromiumKey(strings.TrimRight(string(out), "\n"))
}

// deriveChromiumKey is the key Chromium encrypts cookies with on macOS, from
// the password in the keychain. The values are from Chromium's
// os_crypt_mac.mm.
func deriveChromiumKey(password string) ([]byte, error) {
	return pbkdf2.Key(sha1.New, password, []byte("saltysalt"), 1003, 16)
}

// decryptChromium decrypts one cookie value of a Chromium browser on macOS:
// "v10", then AES-128-CBC with a fixed IV. From version 24 of the database
// the plaintext starts with the SHA-256 of the cookie's host.
func decryptChromium(key, encrypted []byte, host string, version int) (string, bool) {
	if len(encrypted) < 3 || string(encrypted[:3]) != "v10" {
		return "", false
	}
	data := encrypted[3:]
	block, err := aes.NewCipher(key)
	if err != nil || len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return "", false
	}
	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, bytes.Repeat([]byte{' '}, aes.BlockSize)).CryptBlocks(plain, data)
	pad := int(plain[len(plain)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(plain) {
		return "", false
	}
	plain = plain[:len(plain)-pad]
	if version >= 24 {
		sum := sha256.Sum256([]byte(host))
		if len(plain) < len(sum) || !bytes.Equal(plain[:len(sum)], sum[:]) {
			return "", false
		}
		plain = plain[len(sum):]
	}
	return string(plain), true
}

// copyFile streams src into a new private file at dst, so a large cookie
// database is never held in memory.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
