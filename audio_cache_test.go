package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAudioCacheDownloadsTheOriginalAudio(t *testing.T) {
	args := audioCacheDownloadArgs("video-id", t.TempDir(), "--cookies", "cookies.txt")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-f bestaudio") {
		t.Errorf("yt-dlp options do not request the best audio: %s", joined)
	}
	// The cache keeps what YouTube serves. Transcoding or remuxing it spends
	// CPU and loses quality for metadata the player never reads.
	for _, unwanted := range []string{"--extract-audio", "--audio-format", "--audio-quality", "--embed-metadata"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("yt-dlp still post-processes the cached audio (%s): %s", unwanted, joined)
		}
	}
	if got := args[len(args)-1]; got != "https://music.youtube.com/watch?v=video-id" {
		t.Errorf("yt-dlp URL = %q, want it after all options", got)
	}
}

func TestAudioCacheReturnsHitsAndEvictsLeastRecentlyUsed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audio")
	cache, err := newAudioCache(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	cache.setLimit(5)
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		installTestAudio(t, cache, id)
		path, ok := cache.get(id)
		if !ok {
			t.Fatalf("cached track %q was not found", id)
		}
		used := time.Unix(int64(i+1), 0)
		if err := os.Chtimes(path, used, used); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := cache.get("a"); !ok {
		t.Fatal("the first cached track was not found")
	}
	installTestAudio(t, cache, "f")
	if _, ok := cache.get("b"); ok {
		t.Error("the least recently used track was not evicted")
	}
	if _, ok := cache.get("a"); !ok {
		t.Error("a recently replayed track was evicted")
	}
	if got := cachedFileCount(cache); got != 5 {
		t.Errorf("cache has %d files, want 5", got)
	}
}

func TestAudioCacheAtRootUsesPrivateAudioSubdirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	userAudio := filepath.Join(root, "audio")
	if err := os.Mkdir(userAudio, 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(userAudio, "favorite.mp3")
	if err := os.WriteFile(userFile, []byte("keep this"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache, gotRoot, err := newAudioCacheAtRoot(root, 3)
	if err != nil {
		t.Fatal(err)
	}
	if gotRoot != root {
		t.Errorf("cache root = %q, want %q", gotRoot, root)
	}
	if cache.dir != audioCachePath(root) {
		t.Errorf("cache directory = %q, want %q", cache.dir, audioCachePath(root))
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if rootInfo.Mode().Perm() != 0o755 {
		t.Errorf("selected folder permissions changed to %04o", rootInfo.Mode().Perm())
	}
	cacheInfo, err := os.Stat(cache.dir)
	if err != nil {
		t.Fatal(err)
	}
	if cacheInfo.Mode().Perm() != 0o700 {
		t.Errorf("app-owned cache folder permissions = %04o, want 0700", cacheInfo.Mode().Perm())
	}
	if contents, err := os.ReadFile(userFile); err != nil || string(contents) != "keep this" {
		t.Errorf("cache setup changed unrelated audio file: contents %q, error %v", contents, err)
	}
}

func TestAudioCacheDisablesAndClearsAtZero(t *testing.T) {
	cache, err := newAudioCache(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	installTestAudio(t, cache, "cached")
	cache.setLimit(0)
	if got := cachedFileCount(cache); got != 0 {
		t.Fatalf("turning the cache off left %d files", got)
	}
	if _, ok := cache.get("cached"); ok {
		t.Fatal("the disabled cache returned a file")
	}
}

func TestAudioCacheTreatsCompletedDownloadAsMostRecentlyUsed(t *testing.T) {
	cache, err := newAudioCache(t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	installTestAudio(t, cache, "older")
	installTestAudio(t, cache, "newer")
	// Give the two tracks a definite recency order — "older" was used before
	// "newer" — by setting the index and the files directly. Touching them with
	// get would order them by two time.Now() calls, and the map iteration that
	// used to drive this loop shuffled that order from run to run.
	for _, entry := range []struct {
		id   string
		used time.Time
	}{
		{"older", time.Unix(1, 0)},
		{"newer", time.Unix(2, 0)},
	} {
		if _, ok := cache.get(entry.id); !ok {
			t.Fatalf("cached track %q was not found", entry.id)
		}
		cache.mu.Lock()
		file := cache.files[audioCacheKey(entry.id)]
		file.used = entry.used
		cache.files[file.key] = file
		cache.mu.Unlock()
		if err := os.Chtimes(file.path, entry.used, entry.used); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	temp := filepath.Join(dir, "audio.webm")
	if err := os.WriteFile(temp, []byte("just downloaded"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(0, 0)
	if err := os.Chtimes(temp, old, old); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	evicted, err := cache.installLocked("downloaded", temp)
	cache.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	cache.removeFiles(evicted)
	if _, ok := cache.get("downloaded"); !ok {
		t.Fatal("the newly completed download was treated as least recently used")
	}
	if _, ok := cache.get("older"); ok {
		t.Error("the track last used first was not evicted")
	}
}

func TestAudioCacheEnqueueDownloadsOnceInBackground(t *testing.T) {
	cache, err := newAudioCache(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	cookieSeen := make(chan string, 1)
	cache.download = func(_ context.Context, id, dir, cookie string) (string, error) {
		cookieSeen <- cookie
		close(started)
		<-release
		tempDir, err := os.MkdirTemp(dir, ".test-audio-")
		if err != nil {
			return "", err
		}
		path := filepath.Join(tempDir, "audio.webm")
		if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
			return "", err
		}
		return path, nil
	}
	cookie := "SID=private-session"
	cache.enqueue("track", cookie)
	<-started
	cache.enqueue("track", "")
	close(release)
	if got := <-cookieSeen; got != cookie {
		t.Errorf("download cookie = %q, want the signed-in session", got)
	}
	waitFor(t, func() bool {
		cache.mu.Lock()
		pending := len(cache.pending)
		cache.mu.Unlock()
		if pending != 0 {
			return false
		}
		_, ok := cache.get("track")
		return ok
	})
	if got := cachedFileCount(cache); got != 1 {
		t.Errorf("one track was queued more than once: %d files", got)
	}
	for _, entry := range mustReadDir(t, cache.dir) {
		if entry.IsDir() {
			t.Errorf("temporary download directory was left behind: %s", entry.Name())
		}
	}
}

func TestTurningAudioCacheOffCancelsTheActiveDownload(t *testing.T) {
	cache, err := newAudioCache(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	cache.download = func(ctx context.Context, _, _, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	cache.enqueue("track", "")
	<-started
	cache.setLimit(0)
	waitFor(t, func() bool {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		return len(cache.pending) == 0
	})
	if got := cachedFileCount(cache); got != 0 {
		t.Errorf("turning the cache off stored %d files", got)
	}
}

func TestClosingAudioCacheStopsDownloadsAndKeepsCompletedFiles(t *testing.T) {
	cache, err := newAudioCache(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	installTestAudio(t, cache, "completed")
	started := make(chan struct{})
	cache.download = func(ctx context.Context, _, _, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	cache.enqueue("in-progress", "")
	<-started
	cache.close()
	if _, ok := cache.get("completed"); !ok {
		t.Error("closing the cache deleted completed audio")
	}
	if _, ok := cache.get("in-progress"); ok {
		t.Error("closing the cache stored an incomplete download")
	}
	cache.enqueue("after-close", "")
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.pending) != 0 {
		t.Errorf("closing the cache left pending downloads: %v", cache.pending)
	}
}

func TestAudioCacheRejectsInvalidLimitsAndSecuresFiles(t *testing.T) {
	if got := validAudioCacheLimit(37); got != 37 {
		t.Errorf("custom cache limit = %d, want 37", got)
	}
	if got := validAudioCacheLimit(-1); got != defaultAudioCacheLimit {
		t.Errorf("negative cache limit = %d, want default %d", got, defaultAudioCacheLimit)
	}
	dir := t.TempDir()
	cache, err := newAudioCache(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	installTestAudio(t, cache, "private")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("cache directory permissions = %04o, want 0700", info.Mode().Perm())
	}
	path, ok := cache.get("private")
	if !ok {
		t.Fatal("installed audio was not found")
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("cached audio permissions = %04o, want 0600", info.Mode().Perm())
	}
}

func installTestAudio(t *testing.T, cache *audioCache, id string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "audio.webm")
	if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	evicted, err := cache.installLocked(id, path)
	cache.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	cache.removeFiles(evicted)
}

// cachedFileCount reports the number of indexed files under the cache lock.
func cachedFileCount(cache *audioCache) int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return len(cache.filesLocked())
}

func mustReadDir(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// fakeTransport answers HTTP requests without a network, so the tests below
// are deterministic.
type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func fakeAudioResponse(status int, contentType string, body []byte) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Header:        http.Header{"Content-Type": {contentType}},
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(bytes.NewReader(body)),
	}
}

func TestDownloadAudioURLSavesACompleteStream(t *testing.T) {
	body := []byte("resolved audio bytes")
	var sent *http.Request
	client := &http.Client{Transport: fakeTransport(func(request *http.Request) (*http.Response, error) {
		sent = request
		return fakeAudioResponse(http.StatusOK, "audio/webm; codecs=\"opus\"", body), nil
	})}
	dir := t.TempDir()
	path, err := downloadAudioURL(context.Background(), client, "https://media.test/audio", dir)
	if err != nil {
		t.Fatal(err)
	}
	// The direct URL is signed, so the cache sends no cookie of its own, only
	// a browser User-Agent, which YouTube's media servers expect.
	if cookie := sent.Header.Get("Cookie"); cookie != "" {
		t.Errorf("download sent a cookie %q, want none for a signed URL", cookie)
	}
	if ua := sent.Header.Get("User-Agent"); ua == "" || ua == "Go-http-client/1.1" {
		t.Errorf("download User-Agent = %q, want a browser User-Agent", ua)
	}
	if filepath.Dir(path) == dir || !strings.HasPrefix(filepath.Dir(path), dir+string(filepath.Separator)) {
		t.Errorf("download %q is not inside a temporary directory under %q", path, dir)
	}
	if ext := filepath.Ext(path); ext != ".webm" {
		t.Errorf("downloaded audio extension = %q, want .webm from the media type", ext)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("downloaded audio = %q, want %q", got, body)
	}
}

// A stopped stream or an expired URL must not leave a usable file, and must
// not leave a temporary directory behind.
func TestDownloadAudioURLRejectsIncompleteStreams(t *testing.T) {
	readErr := errors.New("connection reset")
	cases := []struct {
		name    string
		ctx     context.Context
		client  *http.Client
		wantErr bool
	}{
		{
			name: "expired URL",
			ctx:  context.Background(),
			client: &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) {
				return fakeAudioResponse(http.StatusForbidden, "text/plain", []byte("expired")), nil
			})},
			wantErr: true,
		},
		{
			name: "fewer bytes than declared",
			ctx:  context.Background(),
			client: &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) {
				short := fakeAudioResponse(http.StatusOK, "audio/webm", []byte("partial"))
				short.ContentLength = 1000
				return short, nil
			})},
			wantErr: true,
		},
		{
			name: "stream cut off",
			ctx:  context.Background(),
			client: &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) {
				response := fakeAudioResponse(http.StatusOK, "audio/webm", nil)
				response.ContentLength = -1
				response.Body = io.NopCloser(&failingBody{err: readErr})
				return response, nil
			})},
			wantErr: true,
		},
		{
			name: "cancelled",
			// The caller cancels the download when playback moves on.
			ctx: cancelledContext(),
			client: &http.Client{Transport: fakeTransport(func(request *http.Request) (*http.Response, error) {
				<-request.Context().Done()
				return nil, request.Context().Err()
			})},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path, err := downloadAudioURL(tc.ctx, tc.client, "https://media.test/audio", dir)
			if tc.wantErr && err == nil {
				t.Fatalf("incomplete download returned %q with no error", path)
			}
			if path != "" {
				t.Errorf("incomplete download returned a path %q", path)
			}
			if entries := mustReadDir(t, dir); len(entries) != 0 {
				t.Errorf("incomplete download left %d entries behind: %v", len(entries), entries)
			}
		})
	}
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// failingBody serves some bytes and then fails, like a connection that drops
// partway through a stream.
type failingBody struct {
	served bool
	err    error
}

func (b *failingBody) Read(p []byte) (int, error) {
	if !b.served {
		b.served = true
		return copy(p, "partial"), nil
	}
	return 0, b.err
}

// Starting a track already resolved its direct URL, so the cache must use
// that URL instead of resolving the same track through yt-dlp a second time.
func TestAudioCacheCachesTheResolvedStreamWithoutResolvingAgain(t *testing.T) {
	cache, err := newAudioCache(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	cache.download = func(context.Context, string, string, string) (string, error) {
		t.Error("caching the current track resolved it through yt-dlp again")
		return "", errors.New("unexpected yt-dlp run")
	}
	body := []byte("resolved audio bytes")
	cache.fetch = func(ctx context.Context, streamURL, cacheDir string) (string, error) {
		client := &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) {
			return fakeAudioResponse(http.StatusOK, "audio/mp4", body), nil
		})}
		return downloadAudioURL(ctx, client, streamURL, cacheDir)
	}
	cache.enqueueStream("track", "https://media.test/audio")
	waitFor(t, func() bool {
		_, ok := cache.get("track")
		return ok
	})
	path, ok := cache.get("track")
	if !ok {
		t.Fatal("the resolved stream was not cached")
	}
	if ext := filepath.Ext(path); ext != ".m4a" {
		t.Errorf("cached audio extension = %q, want .m4a from the media type", ext)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("cached audio = %q, want %q", got, body)
	}
	for _, entry := range mustReadDir(t, cache.dir) {
		if entry.IsDir() {
			t.Errorf("temporary download directory was left behind: %s", entry.Name())
		}
	}
}

// A cache write is best effort: when it fails, playback keeps going and the
// cache stays usable for the next track.
func TestAudioCacheDiscardsAFailedStreamDownload(t *testing.T) {
	cache, err := newAudioCache(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	cache.fetch = func(context.Context, string, string) (string, error) {
		return "", errors.New("signed URL expired")
	}
	cache.enqueueStream("expired", "https://media.test/expired")
	waitFor(t, func() bool {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		return len(cache.pending) == 0
	})
	if _, ok := cache.get("expired"); ok {
		t.Error("a failed download became a cache hit")
	}
	for _, entry := range mustReadDir(t, cache.dir) {
		if entry.IsDir() {
			t.Errorf("temporary download directory was left behind: %s", entry.Name())
		}
	}
}

// seedAudioCache writes count complete cache files with distinct modification
// times, so lookups and eviction run against a realistic directory without
// going through the download path.
func seedAudioCache(tb testing.TB, dir string, count int) []string {
	tb.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		tb.Fatal(err)
	}
	ids := make([]string, count)
	for i := range count {
		id := fmt.Sprintf("video-%d", i)
		ids[i] = id
		path := filepath.Join(dir, audioCacheKey(id)+".webm")
		if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
			tb.Fatal(err)
		}
		used := time.Unix(int64(i+1), 0)
		if err := os.Chtimes(path, used, used); err != nil {
			tb.Fatal(err)
		}
	}
	return ids
}

// BenchmarkAudioCacheLookup measures a hit and a miss against caches of 100 and
// 500 files, the sizes the audit used.
func BenchmarkAudioCacheLookup(b *testing.B) {
	for _, size := range []int{100, 500} {
		b.Run(fmt.Sprintf("files=%d/hit", size), func(b *testing.B) {
			dir := filepath.Join(b.TempDir(), "audio")
			ids := seedAudioCache(b, dir, size)
			cache, err := newAudioCache(dir, size)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, ok := cache.get(ids[size/2]); !ok {
					b.Fatal("cache miss")
				}
			}
		})
		b.Run(fmt.Sprintf("files=%d/miss", size), func(b *testing.B) {
			dir := filepath.Join(b.TempDir(), "audio")
			seedAudioCache(b, dir, size)
			cache, err := newAudioCache(dir, size)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, ok := cache.get("absent"); ok {
					b.Fatal("unexpected cache hit")
				}
			}
		})
	}
}

// BenchmarkAudioCacheTrimToTen measures applying a smaller limit, which evicts
// every file over the new limit. Each iteration indexes a fresh directory.
func BenchmarkAudioCacheTrimToTen(b *testing.B) {
	for _, size := range []int{100, 500} {
		b.Run(fmt.Sprintf("files=%d", size), func(b *testing.B) {
			root := b.TempDir()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				dir, err := os.MkdirTemp(root, "audio-")
				if err != nil {
					b.Fatal(err)
				}
				seedAudioCache(b, dir, size)
				cache, err := newAudioCache(dir, size)
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				cache.setLimit(10)
				b.StopTimer()
				cache.close()
				if err := os.RemoveAll(dir); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Concurrent lookups, enqueues, and limit changes must stay race-free while the
// worker installs and evicts files. Run with -race.
func TestAudioCacheConcurrentAccessIsRaceFree(t *testing.T) {
	cache, err := newAudioCache(t.TempDir(), 20)
	if err != nil {
		t.Fatal(err)
	}
	cache.download = func(_ context.Context, id, dir, _ string) (string, error) {
		tempDir, err := os.MkdirTemp(dir, ".test-audio-")
		if err != nil {
			return "", err
		}
		path := filepath.Join(tempDir, "audio.webm")
		if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
			return "", err
		}
		return path, nil
	}
	const workers = 8
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 40 {
				id := fmt.Sprintf("video-%d-%d", i, j)
				cache.get(id)
				cache.enqueue(id, "")
				if j%10 == 0 {
					cache.setLimit(10 + j%10)
				}
			}
		}()
	}
	wg.Wait()
	cache.close()
	if files := mustReadDir(t, cache.dir); len(files) > 22 {
		t.Errorf("cache kept %d files after concurrent use", len(files))
	}
}

// A cache scaled to hundreds of files must still find and evict tracks in one
// pass, not one directory scan per file.
func TestAudioCacheScalesToHundredsOfFiles(t *testing.T) {
	for _, size := range []int{100, 500} {
		t.Run(fmt.Sprintf("files=%d", size), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "audio")
			ids := seedAudioCache(t, dir, size)
			cache, err := newAudioCache(dir, size)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := cache.get(ids[size-1]); !ok {
				t.Fatal("a track in a large cache was not found")
			}
			cache.setLimit(10)
			if got := cachedFileCount(cache); got != 10 {
				t.Fatalf("cache holds %d indexed files after trimming to 10", got)
			}
			if files := mustReadDir(t, dir); len(files) != 10 {
				t.Fatalf("cache directory holds %d files after trimming to 10", len(files))
			}
		})
	}
}

// A single entry that cannot be removed must not stop the rest of an eviction,
// so the cache cannot stay over its limit because of one bad file.
func TestAudioCacheEvictionContinuesPastARemovalFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audio")
	seedAudioCache(t, dir, 5)
	cache, err := newAudioCache(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	// os.Remove cannot delete a non-empty directory. Give it the oldest
	// timestamp so it is chosen first, then check the files after it go too.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.MkdirAll(filepath.Join(blocker, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	cache.files["blocker"] = audioCacheFile{key: "blocker", path: blocker, used: time.Unix(0, 0)}
	cache.mu.Unlock()
	cache.setLimit(3)
	if got := cachedFileCount(cache); got != 3 {
		t.Errorf("cache holds %d indexed files after a failed removal", got)
	}
	if files := mustReadDir(t, dir); len(files) != 3 {
		t.Errorf("cache directory holds %d files after a failed removal", len(files))
	}
}
