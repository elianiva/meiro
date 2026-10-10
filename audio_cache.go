package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// defaultAudioCacheLimit keeps a short run of recent songs for quick replay.
const defaultAudioCacheLimit = 10

// audioCachePath keeps cache files in an app-owned child directory, so
// choosing a parent folder does not change that folder's permissions.
func audioCachePath(root string) string { return filepath.Join(root, "Meiro Audio Cache") }

func newAudioCacheAtRoot(root string, limit int) (*audioCache, string, error) {
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
	cache, err := newAudioCache(audioCachePath(absolute), limit)
	if err != nil {
		return nil, "", err
	}
	return cache, absolute, nil
}

// audioCache keeps completed audio files for recently played tracks. It is a
// private app cache, not a user-facing download library.
type audioCache struct {
	mu       sync.Mutex
	dir      string
	limit    int
	files    map[string]audioCacheFile
	queue    []audioCacheDownload
	pending  map[string]struct{}
	running  bool
	closed   bool
	cancel   context.CancelFunc
	worker   sync.WaitGroup
	download func(context.Context, string, string, string) (string, error)
	fetch    func(context.Context, string, string) (string, error)
}

type audioCacheDownload struct {
	videoID string
	cookie  string
	// streamURL is a direct audio URL the app already resolved for playback.
	// When it is set, the cache downloads it instead of resolving the track
	// again through yt-dlp. Warm-next tracks have none and take the yt-dlp path.
	streamURL string
}

// newAudioCache prepares a private directory and removes any files over the
// configured limit left by an earlier run.
func newAudioCache(dir string, limit int) (*audioCache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	c := &audioCache{
		dir:      dir,
		limit:    validAudioCacheLimit(limit),
		files:    make(map[string]audioCacheFile),
		pending:  make(map[string]struct{}),
		download: downloadAudio,
		fetch: func(ctx context.Context, streamURL, cacheDir string) (string, error) {
			return downloadAudioURL(ctx, http.DefaultClient, streamURL, cacheDir)
		},
	}
	c.mu.Lock()
	c.scanLocked()
	evicted := c.trimLocked("")
	c.mu.Unlock()
	c.removeFiles(evicted)
	return c, nil
}

// validAudioCacheLimit rejects negative cache sizes. The user may choose any
// non-negative count; zero disables the cache.
func validAudioCacheLimit(limit int) int {
	if limit < 0 {
		return defaultAudioCacheLimit
	}
	return limit
}

// setLimit applies a new cache size and evicts old files immediately.
func (c *audioCache) setLimit(limit int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.limit = validAudioCacheLimit(limit)
	if c.limit == 0 && c.cancel != nil {
		c.cancel()
	}
	evicted := c.trimLocked("")
	c.mu.Unlock()
	c.removeFiles(evicted)
}

// warmable reports whether the cache keeps enough tracks for one downloaded
// ahead of time to survive until it plays. At a limit of one, the warmed
// track evicts the track that is playing, so nothing is gained.
func (c *audioCache) warmable() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.limit > 1
}

// get returns a cached file and moves it to the most-recently-used end.
func (c *audioCache) get(videoID string) (string, bool) {
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

// enqueue downloads videoID in the background with yt-dlp unless it is
// already cached or queued. One worker keeps disk use within the configured
// item limit.
func (c *audioCache) enqueue(videoID, cookie string) {
	c.enqueueDownload(audioCacheDownload{videoID: videoID, cookie: cookie})
}

// enqueueStream caches a track whose direct audio URL the app resolved while
// starting playback. The URL is downloaded as it is, so starting a track does
// not resolve the same track a second time through yt-dlp.
func (c *audioCache) enqueueStream(videoID, streamURL string) {
	c.enqueueDownload(audioCacheDownload{videoID: videoID, streamURL: streamURL})
}

// enqueueDownload adds one download to the queue unless the track is already
// cached or queued, and starts the worker if it is idle.
func (c *audioCache) enqueueDownload(download audioCacheDownload) {
	if c == nil || download.videoID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.limit == 0 {
		return
	}
	if _, cached := c.findLocked(download.videoID); cached {
		return
	}
	if _, exists := c.pending[download.videoID]; exists {
		return
	}
	c.pending[download.videoID] = struct{}{}
	c.queue = append(c.queue, download)
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

// close cancels the active download and waits for the worker to exit before
// the app process shuts down.
func (c *audioCache) close() {
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

func (c *audioCache) drain() {
	for {
		c.mu.Lock()
		if len(c.queue) == 0 {
			c.running = false
			c.mu.Unlock()
			return
		}
		download := c.queue[0]
		c.queue[0] = audioCacheDownload{}
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
			temp, err := c.downloadQueued(ctx, download)
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

// downloadQueued fetches the audio for one queued download. A track the app
// already resolved streams from its direct URL; a warm-next track has none and
// is resolved with yt-dlp.
func (c *audioCache) downloadQueued(ctx context.Context, download audioCacheDownload) (string, error) {
	if download.streamURL != "" {
		return c.fetch(ctx, download.streamURL, c.dir)
	}
	return c.download(ctx, download.videoID, c.dir, download.cookie)
}

// installLocked moves a completed temporary download into the cache and drops
// the entries over the limit from the index. It returns the paths to delete;
// the caller releases c.mu before deleting them. The caller holds c.mu.
func (c *audioCache) installLocked(videoID, temp string) ([]string, error) {
	if temp == "" {
		return nil, errors.New("yt-dlp returned no audio file")
	}
	key := audioCacheKey(videoID)
	if _, cached := c.files[key]; cached {
		return nil, nil
	}
	ext := strings.ToLower(filepath.Ext(temp))
	if !audioCacheExtension(ext) {
		return nil, fmt.Errorf("yt-dlp returned an unsupported audio file %q", ext)
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
	c.files[key] = audioCacheFile{key: key, path: final, used: now}
	return c.trimLocked(key), nil
}

// findLocked returns the cache entry for videoID from the in-memory index. The
// caller holds c.mu.
func (c *audioCache) findLocked(videoID string) (audioCacheFile, bool) {
	file, cached := c.files[audioCacheKey(videoID)]
	return file, cached
}

// trimLocked drops the least-recently-used entries from the index until the
// cache is within its limit, except for keep, and returns their paths. It
// updates only the index, so the caller can delete the paths after releasing
// c.mu. The caller holds c.mu.
func (c *audioCache) trimLocked(keep string) []string {
	excess := len(c.files) - c.limit
	if excess <= 0 {
		return nil
	}
	candidates := make([]audioCacheFile, 0, len(c.files))
	for _, file := range c.files {
		if file.key != keep {
			candidates = append(candidates, file)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	slices.SortFunc(candidates, func(a, b audioCacheFile) int {
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
func (c *audioCache) removeFiles(paths []string) {
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("evicting cached audio %s: %v", path, err)
		}
	}
}

type audioCacheFile struct {
	key  string
	path string
	used time.Time
}

// scanLocked builds the in-memory index from the cache directory. It runs once
// when the cache is created, so later lookups and evictions need no directory
// scan. The caller holds c.mu.
func (c *audioCache) scanLocked() {
	sweepStaleTemp(c.dir, staleTempAge, ".audio-")
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !audioCacheExtension(filepath.Ext(entry.Name())) {
			continue
		}
		key, _, ok := strings.Cut(entry.Name(), ".")
		if !ok || len(key) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(key); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		c.files[key] = audioCacheFile{key: key, path: filepath.Join(c.dir, entry.Name()), used: info.ModTime()}
	}
}

// filesLocked returns the indexed cache files. The caller holds c.mu.
func (c *audioCache) filesLocked() []audioCacheFile {
	files := make([]audioCacheFile, 0, len(c.files))
	for _, file := range c.files {
		files = append(files, file)
	}
	return files
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

// downloadAudio asks yt-dlp to save a song into a temporary directory inside
// the cache. The caller atomically installs it after the download succeeds.
func downloadAudio(ctx context.Context, videoID, cacheDir, cookie string) (string, error) {
	path, err := toolPath("yt-dlp")
	if err != nil {
		return "", errors.New("yt-dlp is not installed or not on PATH")
	}
	tempDir, err := os.MkdirTemp(cacheDir, ".audio-*")
	if err != nil {
		return "", fmt.Errorf("prepare audio cache: %w", err)
	}
	_, runtimeArgs := ytDlpJSRuntimeArgs()
	options := runtimeArgs
	if cookie != "" {
		cookieFile, err := ytDlpCookieFile(cookie)
		if err != nil {
			_ = os.RemoveAll(tempDir)
			return "", fmt.Errorf("prepare yt-dlp authentication: %w", err)
		}
		defer func() { _ = os.Remove(cookieFile) }()
		options = append(options, "--cookies", cookieFile)
	}
	args := audioCacheDownloadArgs(videoID, tempDir, options...)
	command := command(ctx, path, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(tempDir)
		if reason := lastError(string(output)); reason != "" {
			return "", fmt.Errorf("yt-dlp could not cache the audio: %s", reason)
		}
		return "", fmt.Errorf("yt-dlp could not cache the audio: %w", err)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return "", fmt.Errorf("read downloaded audio: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".part") || !strings.HasPrefix(entry.Name(), "audio.") {
			continue
		}
		return filepath.Join(tempDir, entry.Name()), nil
	}
	_ = os.RemoveAll(tempDir)
	return "", errors.New("yt-dlp finished without an audio file")
}

// audioStreamUserAgent is what the cache sends when it fetches an audio
// stream. YouTube's media servers answer some requests without a browser
// User-Agent with an error, the same reason the player tells ffmpeg to send
// one. The URL itself is already signed, so no cookie is needed.
const audioStreamUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// downloadAudioURL saves an audio stream the app already resolved for
// playback into a temporary directory inside the cache. The direct URL
// carries its own authentication and expiry, so no second yt-dlp run is
// needed. A download that ends early, or an expired URL that no longer
// answers, is discarded: only a complete file is returned, so an interrupted
// stream never becomes a cache hit. The caller atomically installs the file.
func downloadAudioURL(ctx context.Context, client *http.Client, streamURL, cacheDir string) (string, error) {
	if strings.TrimSpace(streamURL) == "" {
		return "", errors.New("no resolved audio URL to cache")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return "", fmt.Errorf("prepare audio download: %w", err)
	}
	request.Header.Set("User-Agent", audioStreamUserAgent)
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
	tempDir, err := os.MkdirTemp(cacheDir, ".audio-*")
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

// audioCacheDownloadArgs asks yt-dlp for the track's own audio stream. It
// keeps that stream as it is: YouTube Music already serves Opus in WebM or
// AAC in MP4, which the player reads, and re-encoding it through ffmpeg
// would spend CPU and lose a generation of quality for nothing.
func audioCacheDownloadArgs(videoID, tempDir string, options ...string) []string {
	args := []string{
		"-f", "bestaudio",
		"--no-playlist", "--no-warnings", "--no-progress",
		"-P", tempDir, "-o", "audio.%(ext)s",
	}
	args = append(args, options...)
	return append(args, "https://music.youtube.com/watch?v="+url.QueryEscape(videoID))
}
