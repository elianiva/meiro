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

	"github.com/elianiva/meiro/internal/fsutil"
	"github.com/elianiva/meiro/internal/proc"
	"github.com/elianiva/meiro/youtube"
)

// ErrMissing reports that yt-dlp is neither inside the app nor on PATH.
var ErrMissing = errors.New("playing needs ffmpeg and yt-dlp on PATH")

// Resolve asks yt-dlp for a direct audio URL of a track and its length. Stream
// resolution is anonymous; account cookies stay with the YouTube API client
// for account features and playback history.
func Resolve(ctx context.Context, videoID string) (string, time.Duration, error) {
	streamURL, err := resolveFormat(ctx, videoID, "audio", "bestaudio")
	if err != nil {
		return "", 0, err
	}
	return streamURL, durationFromURL(streamURL), nil
}

// videoFormat is the picture ResolveVideo asks for: no taller than the player
// shows it, and H.264 where YouTube has it, which every machine decodes with
// little work.
const videoFormat = "bestvideo[height<=720][vcodec^=avc1]/bestvideo[height<=720]/bestvideo"

// ResolveVideo asks yt-dlp for a direct URL of a track's picture alone, to
// play beside the audio Resolve finds.
func ResolveVideo(ctx context.Context, videoID string) (string, error) {
	return resolveFormat(ctx, videoID, "video", videoFormat)
}

// resolveFormat resolves the stream of videoID that format selects. what
// names it in errors.
func resolveFormat(ctx context.Context, videoID, what, format string) (string, error) {
	// The visionos client returns anonymous stream URLs without a challenge.
	// yt-dlp's default also asks the web client, which makes it run the solver
	// under QuickJS for about seven seconds of CPU, so try visionos alone first
	// and fall back to the full set if YouTube stops serving it.
	streamURL, err := resolve(ctx, videoID, what, format, true)
	if err == nil {
		return streamURL, nil
	}
	if ctx.Err() != nil {
		return "", err
	}
	log.Printf("playback: js-less resolve failed video_id=%s, retrying with the solver: %v", videoID, err)
	return resolve(ctx, videoID, what, format, false)
}

func resolve(ctx context.Context, videoID, what, format string, jsless bool) (string, error) {
	path, err := proc.Tool("yt-dlp")
	if err != nil {
		return "", ErrMissing
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
	args := []string{"-f", format, "-g", "--no-playlist", "--extractor-args", extractorArgs}
	args = append(args, runtimeArgs...)
	args = append(args, youtube.WatchURL(videoID))
	command := proc.Command(ctx, path, args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	started := time.Now()
	log.Printf("playback: running yt-dlp video_id=%s stream=%s js_runtime=%s", videoID, what, jsRuntime)
	output, err := command.Output()
	if err != nil {
		log.Printf("playback: yt-dlp failed video_id=%s stream=%s elapsed=%s: %s", videoID, what, time.Since(started).Round(time.Millisecond), strings.TrimSpace(stderr.String()))
		if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
			if reason := LastError(stderr.String()); reason != "" {
				return "", fmt.Errorf("yt-dlp could not resolve the %s: %s", what, reason)
			}
		}
		return "", fmt.Errorf("yt-dlp could not resolve the %s: %w", what, err)
	}
	streamURL, _, _ := strings.Cut(string(output), "\n")
	streamURL = strings.TrimSpace(streamURL)
	if streamURL == "" {
		return "", fmt.Errorf("could not resolve the %s", what)
	}
	log.Printf("playback: yt-dlp resolved video_id=%s stream=%s elapsed=%s", videoID, what, time.Since(started).Round(time.Millisecond))
	return streamURL, nil
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
