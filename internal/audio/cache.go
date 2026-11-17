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

const tempPrefix = ".audio-"

const DefaultLimit = 10

func cacheDir(root string) string { return filepath.Join(root, "Meiro Audio Cache") }

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

type pendingDownload struct {
	videoID   string
	streamURL string
}

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

func (c *Cache) Dir() string { return c.dir }

func ValidLimit(limit int) int {
	if limit < 0 {
		return DefaultLimit
	}
	return limit
}

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

func (c *Cache) Warmable() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.limit > 1
}

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
	_ = os.Chtimes(file.path, now, now)
	return file.path, true
}

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

func (c *Cache) findLocked(videoID string) (cachedFile, bool) {
	file, cached := c.files[audioCacheKey(videoID)]
	return file, cached
}

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

func (c *Cache) removeFiles(paths []string) {
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("evicting cached audio %s: %v", path, err)
		}
	}
}

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
	_ = os.Remove(c.dir)
}

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

func (c *Cache) scanLocked() {
	proc.SweepStale(c.dir, nil, tempPrefix)
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

func audioCacheExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".aac", ".flac", ".m4a", ".mkv", ".mp3", ".mp4", ".ogg", ".opus", ".wav", ".weba", ".webm":
		return true
	default:
		return false
	}
}

const browserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

func Fetch(ctx context.Context, streamURL, dir string) (string, error) {
	return downloadURL(ctx, http.DefaultClient, streamURL, dir)
}

func downloadURL(ctx context.Context, client *http.Client, streamURL, cacheDir string) (string, error) {
	if strings.TrimSpace(streamURL) == "" {
		return "", errors.New("no resolved audio URL to cache")
	}
	tempDir, err := os.MkdirTemp(cacheDir, tempPrefix+"*")
	if err != nil {
		return "", fmt.Errorf("prepare audio cache: %w", err)
	}
	path, err := downloadRanges(ctx, client, streamURL, filepath.Join(tempDir, "audio"))
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return "", err
	}
	return path, nil
}

const downloadRange = 10 << 20

const defaultAudioExtension = ".webm"

func downloadRanges(ctx context.Context, client *http.Client, streamURL, base string) (string, error) {
	file, err := os.OpenFile(base, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("prepare audio cache: %w", err)
	}
	var written int64
	total := int64(-1)
	var contentType string
	for total < 0 || written < total {
		n, size, kind, err := appendRange(ctx, client, streamURL, written, file)
		if err != nil {
			_ = file.Close()
			return "", err
		}
		if written == 0 {
			contentType = kind
		}
		written, total = written+n, size
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("download audio: %w", err)
	}
	path := base + audioURLExtension(contentType)
	if err := os.Rename(base, path); err != nil {
		return "", fmt.Errorf("prepare audio cache: %w", err)
	}
	return path, nil
}

func appendRange(ctx context.Context, client *http.Client, streamURL string, offset int64, file io.Writer) (written, total int64, contentType string, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return 0, 0, "", fmt.Errorf("prepare audio download: %w", err)
	}
	request.Header.Set("User-Agent", browserUserAgent)
	request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+downloadRange-1))
	response, err := client.Do(request)
	if err != nil {
		return 0, 0, "", fmt.Errorf("download audio: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case response.StatusCode == http.StatusPartialContent:
		var first, last int64
		contentRange := response.Header.Get("Content-Range")
		if _, err := fmt.Sscanf(contentRange, "bytes %d-%d/%d", &first, &last, &total); err != nil || first != offset {
			return 0, 0, "", fmt.Errorf("download audio: unexpected range %q", contentRange)
		}
	case response.StatusCode == http.StatusOK && offset == 0:
	default:
		return 0, 0, "", fmt.Errorf("download audio: unexpected status %s", response.Status)
	}
	written, err = io.Copy(file, response.Body)
	if err != nil {
		return 0, 0, "", fmt.Errorf("download audio: %w", err)
	}
	if response.ContentLength >= 0 && written != response.ContentLength {
		return 0, 0, "", fmt.Errorf("download audio: got %d of %d bytes", written, response.ContentLength)
	}
	if written == 0 {
		return 0, 0, "", errors.New("download audio: the response was empty")
	}
	if response.StatusCode == http.StatusOK {
		total = written
	}
	return written, total, response.Header.Get("Content-Type"), nil
}

func audioURLExtension(contentType string) string {
	mediaType, _, _ := strings.Cut(contentType, ";")
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "audio/webm", "video/webm":
		return defaultAudioExtension
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
		return defaultAudioExtension
	}
}
