// Package ytdlp uses yt-dlp to resolve audio URLs and import browser cookies.
package ytdlp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/internal/fsutil"
	"github.com/elianiva/meiro/internal/proc"
	"github.com/elianiva/meiro/youtube"
)

// ErrMissing reports that yt-dlp is neither inside the app nor on PATH.
var ErrMissing = errors.New("playing needs ffmpeg and yt-dlp on PATH")

// streamFormat is what one run asks for: the best audio, and beside it the
// picture, no taller than the player shows it and H.264 where YouTube has
// it, which every machine decodes with little work. Both come from the one
// player response, so a track with a picture costs no more requests than
// one without; a track with no picture gives the audio alone.
const streamFormat = "bestaudio,bestvideo[height<=720][vcodec^=avc1]/bestvideo[height<=720]/bestvideo"

// Resolve asks yt-dlp for the direct URLs of a track's audio and picture, and
// its length. Stream resolution is anonymous; account cookies stay with the
// YouTube API client for account features and playback history.
func Resolve(ctx context.Context, videoID string) (audio.Stream, error) {
	// The visionos client returns anonymous stream URLs without a challenge.
	// yt-dlp's default also asks the web client, which makes it run the solver
	// under QuickJS for about seven seconds of CPU, so try visionos alone first
	// and fall back to the full set if YouTube stops serving it.
	stream, err := resolve(ctx, videoID, true)
	if err == nil {
		return stream, nil
	}
	if ctx.Err() != nil {
		return audio.Stream{}, err
	}
	log.Printf("playback: js-less resolve failed video_id=%s, retrying with the solver: %v", videoID, err)
	return resolve(ctx, videoID, false)
}

func resolve(ctx context.Context, videoID string, jsless bool) (audio.Stream, error) {
	path, err := proc.Tool("yt-dlp")
	if err != nil {
		return audio.Stream{}, ErrMissing
	}
	jsRuntime := "none"
	var runtimeArgs []string
	// The player API already lists the audio formats; the HLS and DASH
	// manifests only cost two more requests.
	extractorArgs := "youtube:skip=hls,dash"
	if jsless {
		extractorArgs += ";player_client=visionos"
	} else {
		jsRuntime, runtimeArgs = jsRuntimeArgs()
	}
	args := []string{"-f", streamFormat, "-g", "--no-playlist", "--extractor-args", extractorArgs}
	args = append(args, runtimeArgs...)
	args = append(args, youtube.WatchURL(videoID))
	command := proc.Command(ctx, path, args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	started := time.Now()
	log.Printf("playback: running yt-dlp video_id=%s js_runtime=%s", videoID, jsRuntime)
	output, err := command.Output()
	if err != nil {
		log.Printf("playback: yt-dlp failed video_id=%s elapsed=%s: %s", videoID, time.Since(started).Round(time.Millisecond), strings.TrimSpace(stderr.String()))
		if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
			if reason := LastError(stderr.String()); reason != "" {
				return audio.Stream{}, fmt.Errorf("yt-dlp could not resolve the stream: %s", reason)
			}
		}
		return audio.Stream{}, fmt.Errorf("yt-dlp could not resolve the stream: %w", err)
	}
	stream := parseStreams(string(output))
	if stream.URL == "" {
		return audio.Stream{}, errors.New("could not resolve the audio")
	}
	log.Printf("playback: yt-dlp resolved video_id=%s video=%t elapsed=%s", videoID, stream.Video != "", time.Since(started).Round(time.Millisecond))
	return stream, nil
}

// parseStreams reads the URLs yt-dlp printed, one per line in the order of
// streamFormat: the audio, then the picture when the track has one. A
// YouTube media URL names its kind in its mime parameter, which tells the
// two apart even when the audio is missing; a URL without one is taken in
// order.
func parseStreams(output string) audio.Stream {
	var stream audio.Stream
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		mime := ""
		if parsed, err := url.Parse(line); err == nil {
			mime = parsed.Query().Get("mime")
		}
		switch {
		case strings.HasPrefix(mime, "video/") || (mime == "" && stream.URL != ""):
			if stream.Video == "" {
				stream.Video = line
			}
		case stream.URL == "":
			stream.URL = line
		}
	}
	stream.Total = durationFromURL(stream.URL)
	return stream
}

// BrowserCookie has yt-dlp read the cookies of one browser, or profile, as
// yt-dlp's --cookies-from-browser names it, and returns the Cookie header for
// YouTube they make. It returns "" when the browser holds none.
func BrowserCookie(ctx context.Context, spec string, parse func(netscape string) (string, error)) (string, error) {
	path, err := proc.Tool("yt-dlp")
	if err != nil {
		return "", errors.New("importing needs yt-dlp on PATH")
	}
	directory, err := proc.MkdirTemp("meiro-cookies-")
	if err != nil {
		return "", err
	}
	// The cookies are a credential: the directory goes as soon as they have
	// been read.
	defer fsutil.RemoveAll(directory)
	file := filepath.Join(directory, "cookies.txt")
	// yt-dlp needs a URL to get as far as loading the cookies, and saves them
	// when it ends, whether or not it found anything to download.
	output, runErr := proc.Command(ctx, path,
		"--cookies-from-browser", spec, "--cookies", file,
		"--skip-download", "--no-warnings", "--no-playlist", "--playlist-items", "0",
		"https://music.youtube.com/").CombinedOutput()
	if data, err := os.ReadFile(file); err == nil {
		cookie, parseErr := parse(string(data))
		if parseErr != nil {
			return "", parseErr
		}
		if cookie != "" {
			return cookie, nil
		}
	}
	if runErr != nil {
		if reason := LastError(string(output)); reason != "" {
			return "", errors.New(reason)
		}
		return "", fmt.Errorf("yt-dlp failed: %w", runErr)
	}
	return "", nil
}

// LastError returns the message of the last error yt-dlp printed.
func LastError(output string) string {
	reason := ""
	for line := range strings.SplitSeq(output, "\n") {
		if message, ok := strings.CutPrefix(strings.TrimSpace(line), "ERROR:"); ok {
			reason = strings.TrimSpace(message)
		}
	}
	return reason
}

// jsRuntimeArgs names the JavaScript runtime yt-dlp should use to solve
// YouTube's challenges, and the arguments that enable it. yt-dlp ships the
// EJS solver scripts but needs a runtime to run them. A JIT runtime already
// on PATH is several times cheaper in CPU than QuickJS, which only
// interprets the solver. The app still ships QuickJS as the fallback so
// playing works on a machine with neither.
func jsRuntimeArgs() (string, []string) {
	jsRuntime.mu.Lock()
	defer jsRuntime.mu.Unlock()
	if jsRuntime.found {
		return jsRuntime.name, slices.Clone(jsRuntime.args)
	}
	name, args := lookupJSRuntime()
	// Only a runtime that exists is remembered, so installing one later is
	// noticed and a missing tool is still reported on every call.
	if args != nil {
		jsRuntime.found, jsRuntime.name, jsRuntime.args = true, name, args
	}
	return name, slices.Clone(args)
}

var jsRuntime struct {
	mu    sync.Mutex
	found bool
	name  string
	args  []string
}

func lookupJSRuntime() (string, []string) {
	for _, name := range []string{"node", "bun"} {
		if path, err := exec.LookPath(name); err == nil {
			return name, []string{"--js-runtimes", name + ":" + path}
		}
	}
	if path, err := proc.Tool("qjs"); err == nil {
		return "quickjs", []string{"--js-runtimes", "quickjs:" + path}
	}
	return "none", nil
}

// durationFromURL reads the track length YouTube puts in its media URLs.
func durationFromURL(streamURL string) time.Duration {
	parsed, err := url.Parse(streamURL)
	if err != nil {
		return 0
	}
	seconds, err := strconv.ParseFloat(parsed.Query().Get("dur"), 64)
	if err != nil {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}
