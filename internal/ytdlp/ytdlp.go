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

const (
	skipManifestsArg       = "youtube:skip=hls,dash"
	visionosClientArg      = "player_client=visionos"
	premiumDetectedMessage = "Detected YouTube Premium subscription"
	premiumProbeVideoID    = "jNQXAC9IVRw"
)

var ErrMissing = errors.New("playing needs ffmpeg and yt-dlp on PATH")

type Run struct {
	Args      []string
	Elapsed   time.Duration
	JSRuntime string
}

func Resolve(ctx context.Context, videoID, cookie string) (string, time.Duration, Run, error) {
	if cookie != "" {
		return resolve(ctx, videoID, false, cookie)
	}
	const withoutSolver = true
	streamURL, total, run, err := resolve(ctx, videoID, withoutSolver, "")
	if err == nil {
		return streamURL, total, run, nil
	}
	if ctx.Err() != nil {
		return "", 0, run, err
	}
	log.Printf("playback: js-less resolve failed video_id=%s, retrying with the solver: %v", videoID, err)
	return resolve(ctx, videoID, false, "")
}

func resolve(ctx context.Context, videoID string, jsless bool, cookie string) (string, time.Duration, Run, error) {
	path, err := proc.Tool("yt-dlp")
	if err != nil {
		return "", 0, Run{}, ErrMissing
	}
	jsRuntime := "none"
	var runtimeArgs []string
	extractorArgs := skipManifestsArg
	if jsless {
		extractorArgs += ";" + visionosClientArg
	} else {
		jsRuntime, runtimeArgs = jsRuntimeArgs()
	}
	args := []string{"-f", "bestaudio", "-g", "--no-playlist", "--extractor-args", extractorArgs}
	args = append(args, runtimeArgs...)
	run := Run{JSRuntime: jsRuntime}
	if cookie != "" {
		file, remove, err := cookieFile(cookie)
		if err != nil {
			return "", 0, run, err
		}
		defer remove()
		run.Args = append(slices.Clone(args), "--cookies", "<cookies.txt>", youtube.WatchURL(videoID))
		args = append(args, "--cookies", file)
	} else {
		run.Args = append(slices.Clone(args), youtube.WatchURL(videoID))
	}
	args = append(args, youtube.WatchURL(videoID))
	command := proc.Command(ctx, path, args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	started := time.Now()
	log.Printf("playback: running yt-dlp video_id=%s js_runtime=%s", videoID, jsRuntime)
	output, err := command.Output()
	run.Elapsed = time.Since(started)
	if err != nil {
		log.Printf("playback: yt-dlp failed video_id=%s elapsed=%s: %s", videoID, time.Since(started).Round(time.Millisecond), strings.TrimSpace(stderr.String()))
		if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
			if reason := LastError(stderr.String()); reason != "" {
				return "", 0, run, fmt.Errorf("yt-dlp could not resolve the audio: %s", reason)
			}
		}
		return "", 0, run, fmt.Errorf("yt-dlp could not resolve the audio: %w", err)
	}
	streamURL, _, _ := strings.Cut(string(output), "\n")
	streamURL = strings.TrimSpace(streamURL)
	if streamURL == "" {
		return "", 0, run, errors.New("could not resolve the audio")
	}
	log.Printf("playback: yt-dlp resolved video_id=%s elapsed=%s", videoID, time.Since(started).Round(time.Millisecond))
	return streamURL, durationFromURL(streamURL), run, nil
}

func cookieFile(cookie string) (file string, remove func(), err error) {
	directory, err := proc.MkdirTemp("meiro-cookies-")
	if err != nil {
		return "", nil, err
	}
	file = filepath.Join(directory, "cookies.txt")
	if err := os.WriteFile(file, []byte(netscape(cookie)), 0o600); err != nil {
		fsutil.RemoveAll(directory)
		return "", nil, err
	}
	return file, func() { fsutil.RemoveAll(directory) }, nil
}

func HasPremium(ctx context.Context, cookie string) (bool, error) {
	path, err := proc.Tool("yt-dlp")
	if err != nil {
		return false, ErrMissing
	}
	file, remove, err := cookieFile(cookie)
	if err != nil {
		return false, err
	}
	defer remove()
	output, runErr := proc.Command(ctx, path, "-v", "--simulate", "--no-playlist", "--no-warnings",
		"--cookies", file, "--extractor-args", skipManifestsArg+";"+visionosClientArg,
		youtube.WatchURL(premiumProbeVideoID)).CombinedOutput()
	if strings.Contains(string(output), premiumDetectedMessage) {
		return true, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if runErr != nil {
		if reason := LastError(string(output)); reason != "" {
			return false, errors.New(reason)
		}
		return false, runErr
	}
	return false, nil
}

func netscape(header string) string {
	expires := time.Now().AddDate(1, 0, 0).Unix()
	var out strings.Builder
	out.WriteString("# Netscape HTTP Cookie File\n")
	for part := range strings.SplitSeq(header, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || name == "" {
			continue
		}
		fmt.Fprintf(&out, ".youtube.com\tTRUE\t/\tTRUE\t%d\t%s\t%s\n", expires, name, value)
	}
	return out.String()
}

func BrowserCookie(ctx context.Context, spec string, parse func(netscape string) (string, error)) (string, error) {
	path, err := proc.Tool("yt-dlp")
	if err != nil {
		return "", errors.New("importing needs yt-dlp on PATH")
	}
	directory, err := proc.MkdirTemp("meiro-cookies-")
	if err != nil {
		return "", err
	}
	defer fsutil.RemoveAll(directory)
	file := filepath.Join(directory, "cookies.txt")
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

func LastError(output string) string {
	reason := ""
	for line := range strings.SplitSeq(output, "\n") {
		if message, ok := strings.CutPrefix(strings.TrimSpace(line), "ERROR:"); ok {
			reason = strings.TrimSpace(message)
		}
	}
	return reason
}

func jsRuntimeArgs() (string, []string) {
	jsRuntime.mu.Lock()
	defer jsRuntime.mu.Unlock()
	if jsRuntime.found {
		return jsRuntime.name, slices.Clone(jsRuntime.args)
	}
	name, args := lookupJSRuntime()
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
