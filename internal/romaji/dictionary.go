package romaji

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Where the IPA dictionary comes from, pinned to the commit of
// kagome-dict/ipa v1.2.6 that Meiro was tested with. A dictionary that is
// already on disk is never downloaded again.
const (
	dictionaryCommit = "8de365d1e0586f102f35f18c16e9935b9eb6ca81"
	dictionarySHA256 = "19bacdfe1a33ba50054d77192f062f1696089a48a9d8e20863b716ff4af6adf3"
	dictionaryLimit  = 32 << 20
	dictionaryWait   = 2 * time.Minute
)

// dictionaryURLs are tried in order: a CDN first, then the repository itself.
var dictionaryURLs = []string{
	"https://cdn.jsdelivr.net/gh/ikawaha/kagome-dict@" + dictionaryCommit + "/ipa/ipa.dict",
	"https://raw.githubusercontent.com/ikawaha/kagome-dict/" + dictionaryCommit + "/ipa/ipa.dict",
}

// Dictionary is the Japanese dictionary a Romanizer reads, kept as a file
// because it is too large to ship in the app. It downloads the file the first
// time it is needed and keeps the loaded Romanizer while the app runs.
type Dictionary struct {
	path   string
	urls   []string
	sha256 string
	client *http.Client

	mu        sync.Mutex
	romanizer *Romanizer
}

// DefaultDictionary is the IPADIC dictionary kept at path. The file is
// downloaded from the pinned kagome-dict release when it is not there.
func DefaultDictionary(path string) *Dictionary {
	return &Dictionary{path: path, urls: dictionaryURLs, sha256: dictionarySHA256}
}

// Cached reports whether the dictionary is already on disk, so a caller can
// say that the first turn of the setting downloads it. It does not say that
// the file is valid; the load says that.
func (d *Dictionary) Cached() bool {
	if d == nil || d.path == "" {
		return false
	}
	_, err := os.Stat(d.path)
	return err == nil
}

// Lines romanizes the lines of one song, reading or downloading the dictionary
// first if it is not in memory yet. It is safe to call from more than one
// goroutine; only one of them downloads and reads the dictionary.
func (d *Dictionary) Lines(ctx context.Context, lines []string) (map[string]string, error) {
	r, err := d.ensure(ctx)
	if err != nil {
		return nil, err
	}
	return r.Lines(lines), nil
}

// Unload drops the loaded dictionary so its memory can go back to the system.
// The file stays on disk, so the next Lines call reads it again. It does
// nothing while a load is in progress.
func (d *Dictionary) Unload() {
	if d == nil || !d.mu.TryLock() {
		return
	}
	d.romanizer = nil
	d.mu.Unlock()
}

func (d *Dictionary) ensure(ctx context.Context) (*Romanizer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.romanizer != nil {
		return d.romanizer, nil
	}
	if d.path == "" {
		return nil, errors.New("romaji: the dictionary has no path")
	}
	if err := d.ensureFile(ctx); err != nil {
		return nil, err
	}
	r, err := NewFromFile(d.path)
	if err != nil {
		// A file left half-written by an older version, or damaged since it
		// was downloaded, is fetched again once.
		if remove := os.Remove(d.path); remove != nil && !errors.Is(remove, fs.ErrNotExist) {
			return nil, err
		}
		if err := d.ensureFile(ctx); err != nil {
			return nil, err
		}
		if r, err = NewFromFile(d.path); err != nil {
			return nil, err
		}
	}
	d.romanizer = r
	return r, nil
}

// ensureFile downloads the dictionary unless it is already on disk.
func (d *Dictionary) ensureFile(ctx context.Context) error {
	if _, err := os.Stat(d.path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.path), 0o755); err != nil {
		return err
	}
	if len(d.urls) == 0 {
		return errors.New("romaji: the dictionary has no download URL")
	}
	var errs []error
	for _, url := range d.urls {
		if err := d.download(ctx, url); err != nil {
			errs = append(errs, err)
			if ctx.Err() != nil {
				break
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("romaji: downloading the dictionary: %w", errors.Join(errs...))
}

// download fetches one copy of the dictionary to a temporary file, checks it,
// and moves it into place. A file that fails the check never replaces one that
// is already there.
func (d *Dictionary) download(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, dictionaryWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Meiro")
	client := d.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %s", url, resp.Status)
	}
	if resp.ContentLength > dictionaryLimit {
		return fmt.Errorf("%s: dictionary is %d bytes, larger than %d", url, resp.ContentLength, dictionaryLimit)
	}
	temp, err := os.CreateTemp(filepath.Dir(d.path), ".ipa-*.tmp")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temp.Name())
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(resp.Body, dictionaryLimit+1))
	if err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if n > dictionaryLimit {
		return fmt.Errorf("%s: dictionary is larger than %d bytes", url, dictionaryLimit)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, d.sha256) {
		return fmt.Errorf("%s: dictionary checksum is %s, want %s", url, got, d.sha256)
	}
	if err := os.Rename(temp.Name(), d.path); err != nil {
		// Another window may have downloaded it at the same time.
		if _, stat := os.Stat(d.path); stat == nil {
			return nil
		}
		return err
	}
	keep = true
	return nil
}
