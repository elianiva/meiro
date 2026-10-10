package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elianiva/meiro/youtube"
)

func TestAutoplayQueueIsBoundedWithoutReplacingCurrentTrack(t *testing.T) {
	a := newTestApp()
	a.queue = make([]youtube.MusicItem, autoplayQueueLimit+1)
	a.queue[0] = youtube.MusicItem{VideoID: "selected"}
	for i := 1; i < len(a.queue); i++ {
		a.queue[i] = youtube.MusicItem{VideoID: "recommendation-" + strconv.Itoa(i)}
	}
	a.recommendationStart = 1
	a.index = 5
	a.current = a.queue[a.index]
	current := a.current.VideoID
	a.appendRecommendations([]youtube.MusicItem{
		{VideoID: "new-a"}, {VideoID: "new-b"}, {VideoID: "new-c"}, {VideoID: "new-d"}, {VideoID: "new-e"},
	})

	if a.current.VideoID != current || a.queue[a.index].VideoID != current {
		t.Fatalf("adding recommendations changed the current track to %q at index %d", a.queue[a.index].VideoID, a.index)
	}
	if len(a.recommendationIDs) > autoplayQueueLimit {
		t.Fatalf("retained %d autoplay tracks, limit is %d", len(a.recommendationIDs), autoplayQueueLimit)
	}
	if a.index != 0 {
		t.Errorf("played queue prefix was not evicted, current index = %d", a.index)
	}
	if len(a.queue) != autoplayQueueLimit || a.queue[len(a.queue)-1].VideoID != "new-d" {
		t.Fatalf("queue length = %d and last item = %q, want a full bounded queue ending in new-d", len(a.queue), a.queue[len(a.queue)-1].VideoID)
	}

	more := make([]youtube.MusicItem, autoplayQueueLimit)
	for i := range more {
		more[i] = youtube.MusicItem{VideoID: "later-" + strconv.Itoa(i)}
	}
	a.appendRecommendations(more)
	if len(a.recommendationIDs) > autoplayQueueLimit || len(a.queue) != autoplayQueueLimit {
		t.Fatalf("a full autoplay queue grew to %d items (%d recommendations)", len(a.queue), len(a.recommendationIDs))
	}
}

// Resolving the next track's URL costs a few kilobytes, so it always happens.
// Downloading it is only worth its bandwidth when the cache keeps enough
// tracks for it to still be there when it plays. At a limit of one it would
// evict the track that is playing.
func TestWarmNextResolvesAlwaysAndDownloadsOnlyWithRoom(t *testing.T) {
	for _, limit := range []int{0, 1, 2} {
		a := newTestApp()
		cache, err := newAudioCache(filepath.Join(t.TempDir(), "audio"), limit)
		if err != nil {
			t.Fatal(err)
		}
		fetched := make(chan string, 1)
		cache.fetch = func(_ context.Context, streamURL, _ string) (string, error) {
			fetched <- streamURL
			return "", errors.New("stub")
		}
		a.replaceAudioCache(cache)
		resolved := 0
		a.streams = newStreamCache(func(_ context.Context, id, _ string) (string, time.Duration, error) {
			resolved++
			return "https://media.invalid/" + id, 0, nil
		})
		a.current = youtube.MusicItem{VideoID: "playing"}
		a.queue = []youtube.MusicItem{{VideoID: "playing"}, {VideoID: "next"}}
		a.index = 0
		a.warmNext()
		if resolved != 1 {
			t.Errorf("limit %d: resolved %d times, want 1", limit, resolved)
		}
		select {
		case got := <-fetched:
			if limit <= 1 {
				t.Errorf("limit %d downloaded %q, want no download", limit, got)
			} else if got != "https://media.invalid/next" {
				t.Errorf("downloaded %q, want the resolved URL", got)
			}
		case <-time.After(300 * time.Millisecond):
			if limit > 1 {
				t.Errorf("limit %d did not download the next track", limit)
			}
		}
		cache.close()
	}
}

func TestYtDlpCookieFileUsesPrivateNetscapeFormat(t *testing.T) {
	path, err := ytDlpCookieFile("SID=session; SAPISID=secret=value")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(path) }()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("cookie file permissions = %04o, want 0600", got)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Netscape HTTP Cookie File",
		".youtube.com\tTRUE\t/\tTRUE\t0\tSID\tsession",
		".youtube.com\tTRUE\t/\tTRUE\t0\tSAPISID\tsecret=value",
	} {
		if !strings.Contains(string(contents), want) {
			t.Errorf("cookie file does not contain %q: %s", want, contents)
		}
	}
}

func TestYtDlpCookieFileRejectsEmptyCookieHeader(t *testing.T) {
	if path, err := ytDlpCookieFile("; = ;"); err == nil {
		_ = os.Remove(path)
		t.Fatal("cookie file was created without any valid cookies")
	}
}

func TestParseFFmpegDuration(t *testing.T) {
	output := strings.Join([]string{
		"Input #0, matroska,webm, from '/tmp/audio.webm':",
		"  Metadata:",
		"    encoder         : google/video-file",
		"  Duration: 00:03:00.62, start: 0.000000, bitrate: 135 kb/s",
	}, "\n")
	if got, want := parseFFmpegDuration(output), 3*time.Minute+620*time.Millisecond; got != want {
		t.Errorf("parseFFmpegDuration = %v, want %v", got, want)
	}
	for _, output := range []string{
		"",
		"Input #0, matroska,webm\n  Duration: n/a, bitrate: 135 kb/s", // a live stream
		"Duration: 00:03, start: 0",                                   // not three fields
	} {
		if got := parseFFmpegDuration(output); got != 0 {
			t.Errorf("parseFFmpegDuration(%q) = %v, want 0", output, got)
		}
	}
}

// A cached track is downloaded ahead of time, so nothing resolved its length:
// the app reads it back out of the file, which needs ffmpeg.
func TestAudioFileDurationReadsACachedTrack(t *testing.T) {
	if _, err := toolPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	path := filepath.Join(t.TempDir(), "track.wav")
	writeSilentWAV(t, path, 2*time.Second)

	got := audioFileDuration(context.Background(), path)
	if got < 1900*time.Millisecond || got > 2100*time.Millisecond {
		t.Errorf("audioFileDuration = %v, want about 2s", got)
	}
	if got := audioFileDuration(context.Background(), filepath.Join(t.TempDir(), "missing.webm")); got != 0 {
		t.Errorf("a file that is not there gave %v, want 0", got)
	}
}

// writeSilentWAV writes a PCM file of the given length, which ffmpeg reads
// with no codec of its own.
func writeSilentWAV(t *testing.T, path string, length time.Duration) {
	const (
		rate     = 8000
		channels = 1
		bits     = 16
	)
	data := make([]byte, int(length.Seconds()*rate)*channels*bits/8)
	var file bytes.Buffer
	put := func(value any) { _ = binary.Write(&file, binary.LittleEndian, value) }
	file.WriteString("RIFF")
	put(uint32(36 + len(data)))
	file.WriteString("WAVEfmt ")
	put(uint32(16))
	put(uint16(1)) // PCM
	put(uint16(channels))
	put(uint32(rate))
	put(uint32(rate * channels * bits / 8))
	put(uint16(channels * bits / 8))
	put(uint16(bits))
	file.WriteString("data")
	put(uint32(len(data)))
	file.Write(data)
	if err := os.WriteFile(path, file.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
