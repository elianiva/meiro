// Package audio gets the sound of a track to the player: it resolves the
// track's direct audio URL, remembers recent ones, and keeps the audio of
// recently played tracks on disk.
package audio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/elianiva/meiro/internal/fsutil"
	"github.com/elianiva/meiro/internal/proc"
)

// tempPrefix starts the name of the temporary directories downloads use.
const tempPrefix = ".audio-"

// DefaultLimit keeps a short run of recent songs for quick replay.
const DefaultLimit = 10

// cacheDir keeps cache files in an app-owned child directory, so choosing a
// parent folder does not change that folder's permissions.
func cacheDir(root string) string { return filepath.Join(root, "Meiro Audio Cache") }

// Open opens the cache inside the folder root, which must exist, keeping at
// most limit tracks. It returns the cache and root made absolute.
func Open(root string, limit int) (*Cache, string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, "", errors.New("cache directory is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("%q is not a directory", absolute)
	}
	cache, err := openDir(cacheDir(absolute), limit)
	if err != nil {
		return nil, "", err
	}
	return cache, absolute, nil
}

// Cache keeps completed audio files for recently played tracks. It is a
// private app cache, not a user-facing download library. Its methods are safe
// for concurrent use, and do nothing on a nil Cache.
type Cache struct {
	mu      sync.Mutex
	dir     string
	limit   int
	files   map[string]cachedFile
	queue   []pendingDownload
	pending map[string]struct{}
	running bool
	closed  bool
	cancel  context.CancelFunc
	worker  sync.WaitGroup
	fetch   func(ctx context.Context, streamURL, dir string) (string, error)
}

// pendingDownload is a track waiting to be cached, and the direct audio URL
// the app already resolved for it.
type pendingDownload struct {
	videoID   string
	streamURL string
}

// openDir prepares a private directory and removes any files over the
// limit left by an earlier run.
func openDir(dir string, limit int) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	c := &Cache{
		dir:     dir,
		limit:   ValidLimit(limit),
		files:   make(map[string]cachedFile),
		pending: make(map[string]struct{}),
		fetch: func(ctx context.Context, streamURL, cacheDir string) (string, error) {
			return downloadURL(ctx, http.DefaultClient, streamURL, cacheDir)
		},
	}
	c.mu.Lock()
	c.scanLocked()
	evicted := c.trimLocked("")
	c.mu.Unlock()
	c.removeFiles(evicted)
	return c, nil
}

// Dir is the directory the cache keeps its files in.
func (c *Cache) Dir() string { return c.dir }

// ValidLimit rejects negative cache sizes. The user may choose any
// non-negative count; zero disables the cache.
func ValidLimit(limit int) int {
	if limit < 0 {
		return DefaultLimit
	}
	return limit
}

// SetLimit applies a new cache size and evicts old files immediately.
func (c *Cache) SetLimit(limit int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.limit = ValidLimit(limit)
	if c.limit == 0 && c.cancel != nil {
		c.cancel()
	}
	evicted := c.trimLocked("")
	c.mu.Unlock()
	c.removeFiles(evicted)
}

// Warmable reports whether the cache keeps enough tracks for one downloaded
// ahead of time to survive until it plays. At a limit of one, the warmed
// track evicts the track that is playing, so nothing is gained.
func (c *Cache) Warmable() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.limit > 1
}

// Get returns a cached file and moves it to the most-recently-used end.
func (c *Cache) Get(videoID string) (string, bool) {
	if c == nil || videoID == "" {
		return "", false
	}
	c.mu.Lock()
	if c.limit == 0 {
		c.mu.Unlock()
		return "", false
	}
	file, ok := c.findLocked(videoID)
	if !ok {
		c.mu.Unlock()
		return "", false
	}
	now := time.Now()
	file.used = now
	c.files[file.key] = file
	c.mu.Unlock()
	// Touching the file keeps its least-recently-used order across restarts.
	_ = os.Chtimes(file.path, now, now)
	return file.path, true
}

// Add caches a track from the direct audio URL the app resolved for it, in
// the background, unless the track is already cached or on its way. The URL
// is downloaded as it is, so caching never resolves a track a second time.
func (c *Cache) Add(videoID, streamURL string) {
	if c == nil || videoID == "" || streamURL == "" {
		return
	}
	next := pendingDownload{videoID: videoID, streamURL: streamURL}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.limit == 0 {
		return
	}
	if _, cached := c.findLocked(videoID); cached {
		return
	}
	if _, exists := c.pending[videoID]; exists {
		return
	}
	c.pending[videoID] = struct{}{}
	c.queue = append(c.queue, next)
	if c.running {
		return
	}
	c.running = true
	c.worker.Add(1)
	go func() {
		defer c.worker.Done()
		c.drain()
	}()
}

// Close cancels the active download and waits for the worker to exit before
// the app process shuts down.
func (c *Cache) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.closed = true
	c.queue = nil
	c.pending = make(map[string]struct{})
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
	c.worker.Wait()
}

func (c *Cache) drain() {
	for {
		c.mu.Lock()
		if len(c.queue) == 0 {
			c.running = false
			c.mu.Unlock()
			return
		}
		download := c.queue[0]
		c.queue[0] = pendingDownload{}
		c.queue = c.queue[1:]
		limit := c.limit
		var ctx context.Context
		var cancel context.CancelFunc
		if limit > 0 {
			ctx, cancel = context.WithTimeout(context.Background(), 30*time.Minute)
			c.cancel = cancel
		}
		c.mu.Unlock()

		if limit > 0 {
			temp, err := c.fetch(ctx, download.streamURL, c.dir)
			cancelled := ctx.Err() != nil
			cancel()
			c.mu.Lock()
			c.cancel = nil
			var evicted []string
			if err == nil && c.limit > 0 {
				var installErr error
				evicted, installErr = c.installLocked(download.videoID, temp)
				if installErr != nil {
					log.Printf("caching audio video_id=%s: %v", download.videoID, installErr)
				}
			} else if err != nil && !cancelled {
				log.Printf("caching audio video_id=%s: %v", download.videoID, err)
			}
			c.mu.Unlock()
			c.removeFiles(evicted)
			if temp != "" {
				_ = os.RemoveAll(filepath.Dir(temp))
			}
		}

		c.mu.Lock()
		delete(c.pending, download.videoID)
		c.mu.Unlock()
	}
}

// installLocked moves a completed temporary download into the cache and drops
// the entries over the limit from the index. It returns the paths to delete;
// the caller releases c.mu before deleting them. The caller holds c.mu.
func (c *Cache) installLocked(videoID, temp string) ([]string, error) {
	if temp == "" {
		return nil, errors.New("the download left no audio file")
	}
	key := audioCacheKey(videoID)
	if _, cached := c.files[key]; cached {
		return nil, nil
	}
	ext := strings.ToLower(filepath.Ext(temp))
	if !audioCacheExtension(ext) {
		return nil, fmt.Errorf("the download left an unsupported audio file %q", ext)
	}
	if err := os.Chmod(temp, 0o600); err != nil {
		return nil, fmt.Errorf("secure audio file: %w", err)
	}
	now := time.Now()
	if err := os.Chtimes(temp, now, now); err != nil {
		return nil, fmt.Errorf("mark audio as recently used: %w", err)
	}
	final := filepath.Join(c.dir, key+ext)
	if err := os.Rename(temp, final); err != nil {
		return nil, fmt.Errorf("store audio file: %w", err)
	}
	c.files[key] = cachedFile{key: key, path: final, used: now}
	return c.trimLocked(key), nil
}

// findLocked returns the cache entry for videoID from the in-memory index. The
// caller holds c.mu.
func (c *Cache) findLocked(videoID string) (cachedFile, bool) {
	file, cached := c.files[audioCacheKey(videoID)]
	return file, cached
}

// trimLocked drops the least-recently-used entries from the index until the
// cache is within its limit, except for keep, and returns their paths. It
// updates only the index, so the caller can delete the paths after releasing
// c.mu. The caller holds c.mu.
func (c *Cache) trimLocked(keep string) []string {
	excess := len(c.files) - c.limit
	if excess <= 0 {
		return nil
	}
	candidates := make([]cachedFile, 0, len(c.files))
	for _, file := range c.files {
		if file.key != keep {
			candidates = append(candidates, file)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	slices.SortFunc(candidates, func(a, b cachedFile) int {
		return a.used.Compare(b.used)
	})
	evicted := make([]string, 0, min(excess, len(candidates)))
	for _, file := range candidates[:min(excess, len(candidates))] {
		delete(c.files, file.key)
		evicted = append(evicted, file.path)
	}
	return evicted
}

// removeFiles deletes evicted files. It runs without c.mu held, so a large
// eviction never blocks lookups or the UI's warmable check, and one file that
// cannot be removed does not stop the rest.
func (c *Cache) removeFiles(paths []string) {
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("evicting cached audio %s: %v", path, err)
		}
	}
}

// Purge deletes the files this cache owns, and then its directory when
// nothing else is in it: downloaded audio named by key, and abandoned
// temporary download folders. Anything else in the directory is left alone.
// It is for a cache that has been replaced by one in another location, and
// must run after close so no download is still writing.
func (c *Cache) Purge() {
	if c == nil {
		return
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(c.dir, entry.Name())
		switch {
		case entry.IsDir() && strings.HasPrefix(entry.Name(), tempPrefix):
			fsutil.RemoveAll(path)
		case !entry.IsDir() && isAudioCacheFile(entry.Name()):
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("removing old cached audio %s: %v", path, err)
			}
		}
	}
	// Remove fails on a directory that still holds the user's files.
	_ = os.Remove(c.dir)
}

// isAudioCacheFile reports whether name is a file the cache itself writes: a
// SHA-256 key followed by an audio extension.
func isAudioCacheFile(name string) bool {
	key, _, ok := strings.Cut(name, ".")
	if !ok || len(key) != sha256.Size*2 || !audioCacheExtension(filepath.Ext(name)) {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}

type cachedFile struct {
	key  string
	path string
	used time.Time
}

// scanLocked builds the in-memory index from the cache directory. It runs once
// when the cache is created, so later lookups and evictions need no directory
// scan. The caller holds c.mu.
func (c *Cache) scanLocked() {
	proc.SweepStale(c.dir, tempPrefix)
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !isAudioCacheFile(entry.Name()) {
			continue
		}
		key, _, _ := strings.Cut(entry.Name(), ".")
		info, err := entry.Info()
		if err != nil {
			continue
		}
		c.files[key] = cachedFile{key: key, path: filepath.Join(c.dir, entry.Name()), used: info.ModTime()}
	}
}

func audioCacheKey(videoID string) string {
	sum := sha256.Sum256([]byte(videoID))
	return hex.EncodeToString(sum[:])
}

// audioCacheExtension accepts the containers yt-dlp saves an audio-only
// format in: WebM audio arrives as .webm or .weba, MP4 audio as .m4a or .mp4.
func audioCacheExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".aac", ".flac", ".m4a", ".mkv", ".mp3", ".mp4", ".ogg", ".opus", ".wav", ".weba", ".webm":
		return true
	default:
		return false
	}
}

// userAgent is what the cache sends when it fetches an audio
// stream. YouTube's media servers answer some requests without a browser
// User-Agent with an error, the same reason the player tells ffmpeg to send
// one. The URL itself is already signed, so no cookie is needed.
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// downloadURL saves an audio stream the app already resolved for
// playback into a temporary directory inside the cache. The direct URL
// carries its own authentication and expiry, so no second yt-dlp run is
// needed. A download that ends early, or an expired URL that no longer
// answers, is discarded: only a complete file is returned, so an interrupted
// stream never becomes a cache hit. The caller atomically installs the file.
func downloadURL(ctx context.Context, client *http.Client, streamURL, cacheDir string) (string, error) {
	if strings.TrimSpace(streamURL) == "" {
		return "", errors.New("no resolved audio URL to cache")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return "", fmt.Errorf("prepare audio download: %w", err)
	}
	request.Header.Set("User-Agent", userAgent)
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download audio: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		// A signed URL that has expired answers with an error; the track is
		// already playing, so the cache just skips it.
		return "", fmt.Errorf("download audio: unexpected status %s", response.Status)
	}
	tempDir, err := os.MkdirTemp(cacheDir, tempPrefix+"*")
	if err != nil {
		return "", fmt.Errorf("prepare audio cache: %w", err)
	}
	path := filepath.Join(tempDir, "audio"+audioURLExtension(response.Header.Get("Content-Type")))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return "", fmt.Errorf("prepare audio cache: %w", err)
	}
	written, copyErr := io.Copy(file, response.Body)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.RemoveAll(tempDir)
		return "", fmt.Errorf("download audio: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.RemoveAll(tempDir)
		return "", fmt.Errorf("download audio: %w", closeErr)
	}
	// A stream cut short can end without an error, so the declared length is
	// the only proof that every byte arrived.
	if response.ContentLength >= 0 && written != response.ContentLength {
		_ = os.RemoveAll(tempDir)
		return "", fmt.Errorf("download audio: got %d of %d bytes", written, response.ContentLength)
	}
	if written == 0 {
		_ = os.RemoveAll(tempDir)
		return "", errors.New("download audio: the response was empty")
	}
	return path, nil
}

// audioURLExtension picks the file extension for a downloaded audio stream
// from the media type YouTube reports for it. ffmpeg reads a file by its
// contents, so the extension only has to name a container the cache keeps.
func audioURLExtension(contentType string) string {
	mediaType, _, _ := strings.Cut(contentType, ";")
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "audio/webm", "video/webm":
		return ".webm"
	case "audio/mp4", "video/mp4":
		return ".m4a"
	case "audio/ogg", "application/ogg":
		return ".ogg"
	case "audio/opus":
		return ".opus"
	case "audio/aac", "audio/aacp":
		return ".aac"
	case "audio/flac", "audio/x-flac":
		return ".flac"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav"
	case "audio/mpeg":
		return ".mp3"
	default:
		// YouTube Music serves most audio as Opus in WebM, which is the safe
		// fallback when a media server reports no useful type.
		return ".webm"
	}
}
