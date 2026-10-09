package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/elianiva/meiro/youtube"
)

// play starts item, with the items around it as the queue.
func (a *app) play(item youtube.MusicItem, queue []youtube.MusicItem, index int) {
	a.playWithOptions(item, queue, index, youtube.UpNextOptions{}, "")
}

// playWithOptions starts an item and remembers the context used to ask
// YouTube Music for the generated queue.
func (a *app) playWithOptions(item youtube.MusicItem, queue []youtube.MusicItem, index int, options youtube.UpNextOptions, source string) {
	if item.VideoID == "" {
		return
	}
	// The page rebuilds its own list on every frame, so the queue must not
	// share its backing array.
	a.queue = slices.Clone(queue)
	if len(a.queue) == 0 {
		a.queue = []youtube.MusicItem{item}
	}
	if index < 0 || index >= len(a.queue) || a.queue[index].VideoID != item.VideoID {
		index = 0
		for i := range a.queue {
			if a.queue[i].VideoID == item.VideoID {
				index = i
				break
			}
		}
	}
	a.index = index
	options.VideoID = item.VideoID
	a.resetUpNext(options, source)
	if a.resolving && a.current.VideoID == item.VideoID {
		a.ensureUpNext()
		return // this track is already on its way
	}
	a.start(item)
}

// resetUpNext drops recommendations from the queue being replaced and
// invalidates any request that belongs to the previous selection.
func (a *app) resetUpNext(options youtube.UpNextOptions, source string) {
	a.upNextGeneration++
	a.playNextID = ""
	a.upNextOptions = options
	a.upNextLoading = false
	a.upNextFetched = false
	a.upNextErr = ""
	a.upNextToken = ""
	a.upNextSeen = make(map[string]struct{})
	a.recommendationStart = -1
	a.queueSource = source
	a.waitingForAuto = false
}

// playAll plays everything on the page shown.
func (a *app) playAll() {
	if len(a.playable) == 0 {
		return
	}
	options, source := a.playbackQueueOptions(0)
	a.playWithOptions(a.playable[0], a.playable, 0, options, source)
}

// start makes item the current track and resolves its audio.
func (a *app) start(item youtube.MusicItem) {
	a.current = item
	a.total = parseDuration(item.Duration)
	a.scrub, a.playErr = 0, ""
	a.player.Stop()
	a.stream(item)
	a.ensureUpNext()
}

// ensureUpNext fetches the initial queue at once, then asks for another page
// only when the generated part is almost exhausted.
func (a *app) ensureUpNext() {
	if !a.settings.AutoPlay || a.current.VideoID == "" || a.upNextLoading || len(a.queue) == 0 {
		return
	}
	client := a.client()
	if client == nil {
		return
	}
	continuation := ""
	if a.upNextFetched {
		if a.upNextToken == "" || len(a.queue)-a.index > 2 {
			return
		}
		continuation = a.upNextToken
		if a.upNextSeen == nil {
			a.upNextSeen = make(map[string]struct{})
		}
		if _, seen := a.upNextSeen[continuation]; seen {
			a.upNextToken = ""
			return
		}
		a.upNextSeen[continuation] = struct{}{}
	} else {
		a.upNextFetched = true
	}
	a.upNextLoading = true
	generation := a.upNextGeneration
	options := a.upNextOptions
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		var result *youtube.BrowseResult
		var err error
		if continuation == "" {
			result, err = client.GetUpNextWithOptions(ctx, options)
		} else {
			result, err = client.ContinueUpNext(ctx, options, continuation)
		}
		defer reclaimMemory()
		a.update(func() {
			if generation != a.upNextGeneration {
				return
			}
			a.upNextLoading = false
			if err != nil {
				a.upNextErr = err.Error()
				a.upNextToken = ""
				if a.waitingForAuto {
					a.waitingForAuto = false
					a.stopAtQueueEnd()
				}
				return
			}
			a.upNextErr = ""
			if result.QueuePlaylistID != "" {
				if a.upNextOptions.PlaylistID != "" && a.upNextOptions.PlaylistID != result.QueuePlaylistID {
					a.upNextOptions.PlaylistIndex = nil
				}
				a.upNextOptions.PlaylistID = result.QueuePlaylistID
			}
			if result.ContinuationToken == continuation {
				a.upNextToken = ""
			} else {
				a.upNextToken = result.ContinuationToken
			}
			a.appendRecommendations(result.Items)
			if a.waitingForAuto {
				if a.index+1 < len(a.queue) {
					a.waitingForAuto = false
					a.advance()
				} else {
					a.ensureUpNext()
				}
			}
		})
	})
}

// appendRecommendations keeps the selected queue intact and appends only
// tracks YouTube Music has not already returned in it.
func (a *app) appendRecommendations(items []youtube.MusicItem) {
	seen := make(map[string]struct{}, len(a.queue)+len(items))
	for _, queued := range a.queue {
		if queued.VideoID != "" {
			seen[queued.VideoID] = struct{}{}
		}
	}
	for _, item := range items {
		if item.VideoID == "" {
			continue
		}
		if _, exists := seen[item.VideoID]; exists {
			continue
		}
		seen[item.VideoID] = struct{}{}
		if a.recommendationStart < 0 {
			a.recommendationStart = len(a.queue)
		}
		a.queue = append(a.queue, item)
	}
	// A recommended track can be the one that plays next, so warm it too.
	a.warmNext()
}

// enqueue adds a track to the selected queue without changing the current
// track. User-selected tracks stay ahead of autoplay recommendations.
func (a *app) enqueue(item youtube.MusicItem, playNext bool) {
	if item.VideoID == "" {
		return
	}
	if a.current.VideoID == "" || len(a.queue) == 0 {
		a.playWithOptions(item, []youtube.MusicItem{item}, 0, youtube.UpNextOptions{}, "Queue")
		a.notice = "Playing " + item.Title
		return
	}

	position := len(a.queue)
	if a.recommendationStart >= 0 {
		position = a.recommendationStart
	}
	if playNext {
		position = a.index + 1
	}
	position = min(max(position, 0), len(a.queue))
	a.queue = slices.Insert(a.queue, position, item)
	if position <= a.index {
		a.index++
	}
	if a.recommendationStart >= 0 && position <= a.recommendationStart {
		a.recommendationStart++
	}
	if playNext {
		a.playNextID = item.VideoID
		if !a.settings.AutoPlay && a.recommendationStart >= 0 && position >= a.recommendationStart {
			a.recommendationStart = position + 1
		}
		a.notice = item.Title + " will play next"
	} else {
		a.notice = "Added " + item.Title + " to the queue"
	}
}

// setAutoPlay changes whether generated tracks can follow the selected queue.
func (a *app) setAutoPlay(enabled bool) {
	if a.settings.AutoPlay == enabled {
		return
	}
	a.settings.AutoPlay = enabled
	a.saveSettings()
	a.upNextGeneration++
	a.upNextLoading = false
	a.upNextErr = ""
	a.waitingForAuto = false
	if enabled {
		a.upNextFetched = false
		a.upNextToken = ""
		a.upNextSeen = make(map[string]struct{})
		a.upNextOptions.VideoID = a.current.VideoID
		a.ensureUpNext()
	}
}

// loading reports whether the track chosen is not making sound yet: its audio
// is being found, or ffmpeg is still opening it.
func (a *app) loading() bool {
	return a.resolving || a.player.Buffering()
}

// stream resolves a track's audio off the main thread and plays it.
func (a *app) stream(item youtube.MusicItem) {
	a.streamGen++
	gen := a.streamGen
	a.resolving = true
	client := a.client()
	cookie := a.ytDlpCookie
	log.Printf("playback: resolving video_id=%s signed_in=%t", item.VideoID, cookie != "")
	a.run(func() {
		streamURL, total := "", parseDuration(item.Duration)
		var err error
		cached, fromCache := a.currentAudioCache().get(item.VideoID)
		if fromCache {
			log.Printf("playback: audio cache hit video_id=%s", item.VideoID)
			streamURL = cached
			if total <= 0 {
				// A track downloaded ahead of time was never resolved, so its
				// length is written nowhere but the file itself.
				probe, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				total = audioFileDuration(probe, cached)
				cancel()
			}
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			streamURL, total, err = resolveStream(ctx, client, item, cookie)
			cancel()
		}
		defer reclaimMemory()
		a.update(func() {
			if gen != a.streamGen {
				return // another track was chosen while this one resolved
			}
			a.resolving = false
			if err != nil {
				log.Printf("playback: audio resolution failed video_id=%s: %v", item.VideoID, err)
				a.playErr = err.Error()
				return
			}
			if total > 0 {
				a.total = total
			}
			if playerErr := a.player.Play(streamURL); playerErr != nil {
				log.Printf("playback: player could not open video_id=%s: %v", item.VideoID, playerErr)
				a.playErr = playerErr.Error()
				return
			}
			source := "stream"
			if fromCache {
				source = "cache"
			}
			log.Printf("playback: audio started video_id=%s source=%s duration=%s", item.VideoID, source, total)
			if !fromCache {
				cache, videoID, cookie := a.currentAudioCache(), item.VideoID, cookie
				a.run(func() { cache.enqueue(videoID, cookie) })
			}
			a.warmNext()
		})
	})
}

// warmNext starts the next queue track downloading while the current one
// plays, so a play that follows opens a local file instead of waiting on the
// network. It does nothing when the queue ends at the current track, or when
// the cache is too small for a warmed track to survive until it plays.
func (a *app) warmNext() {
	if a.index+1 >= len(a.queue) {
		return
	}
	next := a.queue[a.index+1]
	if next.VideoID == "" || next.VideoID == a.current.VideoID {
		return
	}
	cache, cookie := a.currentAudioCache(), a.ytDlpCookie
	if !cache.warmable() {
		return
	}
	a.run(func() { cache.enqueue(next.VideoID, cookie) })
}

// setAudioCacheLimit changes the number of recently played audio files kept
// locally, evicting old files at once when the limit goes down.
func (a *app) setAudioCacheLimit(limit int) {
	a.settings.CacheSongs = validAudioCacheLimit(limit)
	a.cacheSongsText = strconv.Itoa(a.settings.CacheSongs)
	cache, count := a.currentAudioCache(), a.settings.CacheSongs
	a.run(func() { cache.setLimit(count) })
	a.saveSettings()
}

// resolveStream finds a URL ffmpeg can read. It asks YouTube through the
// package first, and falls back to yt-dlp, which keeps working when
// YouTube's player script has moved past what the package can decipher.
func resolveStream(ctx context.Context, client *youtube.Client, item youtube.MusicItem, cookie string) (string, time.Duration, error) {
	var direct error
	if client != nil {
		info, err := client.GetTrackInfo(ctx, item.VideoID)
		if err == nil {
			if err := playabilityError(info.Playability); err != nil {
				log.Printf("playback: YouTube rejected video_id=%s: %v", item.VideoID, err)
				return "", 0, err
			}
			if format, ok := info.BestAudioFormat(); ok {
				log.Printf("playback: YouTube resolved video_id=%s itag=%d mime=%s", item.VideoID, format.Itag, format.MimeType)
				return format.PlayableURL(), parseDuration(info.VideoDetails.Length), nil
			}
			err = errors.New("no audio format could be read")
		}
		direct = fmt.Errorf("asking YouTube: %w", err)
		log.Printf("playback: YouTube audio unavailable video_id=%s: %v; trying yt-dlp", item.VideoID, direct)
	}
	streamURL, total, err := ytDlpStream(ctx, item.VideoID, cookie)
	if err != nil {
		// What yt-dlp said comes first, as the message shows only a line of it;
		// what YouTube said is what to look at when yt-dlp is not there.
		return "", 0, errors.Join(err, direct)
	}
	return streamURL, total, nil
}

func playabilityError(status youtube.Playability) error {
	if status.Status == "" || status.Status == "OK" {
		return nil
	}
	reason := strings.TrimSpace(status.Reason)
	if reason == "" {
		reason = strings.Join(status.Messages, " ")
	}
	if reason == "" {
		reason = status.Status
	}
	return fmt.Errorf("YouTube cannot play this track (%s): %s", status.Status, reason)
}

// ytDlpStream asks yt-dlp for a direct audio URL, using the signed-in session
// when YouTube requires one.
func ytDlpStream(ctx context.Context, videoID, cookie string) (string, time.Duration, error) {
	path, err := toolPath("yt-dlp")
	if err != nil {
		return "", 0, errors.New("playing needs ffmpeg and yt-dlp on PATH")
	}
	jsRuntime, runtimeArgs := ytDlpJSRuntimeArgs()
	args := []string{"-f", "bestaudio", "-g", "--no-playlist", "--verbose"}
	args = append(args, runtimeArgs...)
	if cookie != "" {
		cookieFile, err := ytDlpCookieFile(cookie)
		if err != nil {
			return "", 0, fmt.Errorf("prepare yt-dlp authentication: %w", err)
		}
		defer os.Remove(cookieFile)
		args = append(args, "--cookies", cookieFile)
	}
	args = append(args,
		"https://music.youtube.com/watch?v="+url.QueryEscape(videoID))
	command := exec.CommandContext(ctx, path, args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	started := time.Now()
	log.Printf("playback: running yt-dlp video_id=%s authenticated=%t js_runtime=%s", videoID, cookie != "", jsRuntime)
	output, err := command.Output()
	if err != nil {
		log.Printf("playback: yt-dlp failed video_id=%s elapsed=%s: %s", videoID, time.Since(started).Round(time.Millisecond), strings.TrimSpace(stderr.String()))
		if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
			if reason := lastError(stderr.String()); reason != "" {
				return "", 0, fmt.Errorf("yt-dlp could not resolve the audio: %s", reason)
			}
		}
		return "", 0, fmt.Errorf("yt-dlp could not resolve the audio: %w", err)
	}
	streamURL, _, _ := strings.Cut(string(output), "\n")
	streamURL = strings.TrimSpace(streamURL)
	if streamURL == "" {
		return "", 0, errors.New("could not resolve the audio")
	}
	log.Printf("playback: yt-dlp resolved video_id=%s elapsed=%s", videoID, time.Since(started).Round(time.Millisecond))
	return streamURL, durationFromURL(streamURL), nil
}

func ytDlpJSRuntimeArgs() (string, []string) {
	for _, runtime := range []struct {
		name   string
		binary string
	}{
		{name: "deno", binary: "deno"},
		{name: "node", binary: "node"},
		{name: "bun", binary: "bun"},
		{name: "quickjs", binary: "qjs"},
	} {
		path, err := exec.LookPath(runtime.binary)
		if err == nil {
			return runtime.name, []string{"--js-runtimes", runtime.name + ":" + path}
		}
	}
	return "none", nil
}

// ytDlpCookieFile translates the app's YouTube Cookie header into the
// temporary Netscape file yt-dlp accepts. CreateTemp keeps credentials private.
func ytDlpCookieFile(cookie string) (string, error) {
	file, err := os.CreateTemp("", "meiro-yt-dlp-cookies-*.txt")
	if err != nil {
		return "", err
	}
	name := file.Name()
	remove := func() { _ = os.Remove(name) }
	if _, err := file.WriteString("# Netscape HTTP Cookie File\n"); err != nil {
		_ = file.Close()
		remove()
		return "", err
	}
	count := 0
	for part := range strings.SplitSeq(cookie, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || key == "" || strings.ContainsAny(key+value, "\t\r\n") {
			continue
		}
		if _, err := fmt.Fprintf(file, ".youtube.com\tTRUE\t/\tTRUE\t0\t%s\t%s\n", key, strings.TrimSpace(value)); err != nil {
			_ = file.Close()
			remove()
			return "", err
		}
		count++
	}
	if err := file.Close(); err != nil {
		remove()
		return "", err
	}
	if count == 0 {
		remove()
		return "", errors.New("cookie header has no valid cookies")
	}
	return name, nil
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

// parseDuration reads a "3:42" or "1:02:03" track length.
func parseDuration(text string) time.Duration {
	parts := strings.Split(strings.TrimSpace(text), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	total := time.Duration(0)
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return 0
		}
		total = total*60 + time.Duration(value)*time.Second
	}
	return total
}

// audioFileDuration reads how long an audio file plays. The cached tracks
// carry their length in their header, and ffmpeg is already at hand to decode
// them, so it is asked to open the file: it prints the length it finds, and
// fails for the output it was not given.
func audioFileDuration(ctx context.Context, path string) time.Duration {
	ffmpeg, err := toolPath("ffmpeg")
	if err != nil {
		return 0
	}
	output, _ := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-i", path).CombinedOutput()
	return parseFFmpegDuration(string(output))
}

// parseFFmpegDuration reads the length out of what ffmpeg prints about a
// file, in its "Duration: 00:03:00.62" line.
func parseFFmpegDuration(output string) time.Duration {
	for line := range strings.SplitSeq(output, "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "Duration:")
		if !ok {
			continue
		}
		value, _, _ = strings.Cut(strings.TrimSpace(value), ",")
		parts := strings.Split(strings.TrimSpace(value), ":")
		if len(parts) != 3 {
			return 0
		}
		hours, hourErr := strconv.ParseFloat(parts[0], 64)
		minutes, minuteErr := strconv.ParseFloat(parts[1], 64)
		seconds, secondErr := strconv.ParseFloat(parts[2], 64)
		if hourErr != nil || minuteErr != nil || secondErr != nil {
			return 0
		}
		return time.Duration((hours*3600 + minutes*60 + seconds) * float64(time.Second))
	}
	return 0
}

// advance plays the next track of the queue, waiting for recommendations at
// its end when autoplay is on.
func (a *app) advance() {
	if next, ok := a.nextIndex(); ok {
		if a.queue[next].VideoID == a.playNextID {
			a.playNextID = ""
		}
		a.index = next
		a.start(a.queue[a.index])
		return
	}
	if a.settings.AutoPlay && (a.upNextLoading || !a.upNextFetched || a.upNextToken != "") {
		a.waitingForAuto = true
		a.player.Stop()
		a.ensureUpNext()
		return
	}
	a.stopAtQueueEnd()
}

// stopAtQueueEnd stops playback and invalidates a stream lookup that may still
// be outstanding.
func (a *app) stopAtQueueEnd() {
	a.streamGen++ // drop a track still being found
	a.resolving = false
	a.player.Stop()
}

// previous restarts the track, or plays the one before it when the track
// has only just started.
func (a *app) previous() {
	if a.index == 0 || a.player.Position() > 3*time.Second {
		a.player.Seek(0)
		return
	}
	a.index--
	a.start(a.queue[a.index])
}

// togglePlay pauses a playing track, resumes a paused one, and starts the
// current track again after it failed or ended.
func (a *app) togglePlay() {
	if a.resolving {
		return // the track is already on its way
	}
	if a.playErr != "" || !a.player.Active() {
		if a.current.VideoID != "" {
			a.start(a.current)
		}
		return
	}
	a.player.Toggle()
}

// clock formats a position as "3:42" or "1:02:03".
func clock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	hours, minutes, seconds := total/3600, (total%3600)/60, total%60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}

// shufflePage turns shuffle on and plays the page from a song picked at
// random.
func (a *app) shufflePage() {
	if len(a.playable) == 0 {
		return
	}
	a.shuffle = true
	i := rand.IntN(len(a.playable))
	options, source := a.playbackQueueOptions(i)
	a.playWithOptions(a.playable[i], a.playable, i, options, source)
}

// setVolume sets the volume, from 0 to 100, and keeps it for the next run.
func (a *app) setVolume(v float64) {
	a.volume = min(max(v, 0), 100)
	a.player.SetVolume(a.volume / 100)
	a.settings.Volume = a.volume
}

// toggleMute silences the player, or brings the volume back.
func (a *app) toggleMute() {
	if a.volume > 0 {
		a.muted = a.volume
		a.setVolume(0)
		return
	}
	a.setVolume(max(a.muted, 40))
}

// cycleRepeat goes from not repeating to repeating the queue to repeating the
// track and back.
func (a *app) cycleRepeat() { a.repeat = (a.repeat + 1) % (repeatTrack + 1) }

// lyricsState is the lyrics of the track playing, as far as they are loaded.
type lyricsState struct {
	videoID string
	loading bool
	text    string
	footer  string
	err     string
}

// loadLyrics fetches the lyrics of the current track, once.
func (a *app) loadLyrics() {
	id := a.current.VideoID
	client := a.client()
	if id == "" || a.lyrics.videoID == id || client == nil {
		return
	}
	a.lyrics = lyricsState{videoID: id, loading: true}
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		lyrics, err := client.GetLyrics(ctx, id)
		a.update(func() {
			if a.lyrics.videoID != id {
				return
			}
			a.lyrics.loading = false
			if err != nil {
				a.lyrics.err = "No lyrics for this song."
				return
			}
			a.lyrics.text, a.lyrics.footer = strings.TrimSpace(lyrics.Description), lyrics.Footer
			if a.lyrics.text == "" {
				a.lyrics.err = "No lyrics for this song."
			}
		})
	})
}

// loadRelated fetches related songs for the track playing, once per track.
func (a *app) loadRelated() {
	id := a.current.VideoID
	client := a.client()
	if id == "" || a.related.videoID == id || client == nil {
		return
	}
	a.related = relatedState{videoID: id, loading: true}
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		result, err := client.GetRelated(ctx, id)
		a.update(func() {
			if a.related.videoID != id {
				return
			}
			a.related.loading = false
			if err != nil {
				a.related.err = "Related songs aren't available."
				return
			}
			a.related.items = result.Items
			if len(result.Items) == 0 {
				a.related.err = "No related songs for this track."
			}
		})
	})
}

// followArtwork takes the theme's seed from the artwork of the track playing,
// when the user asked for it.
func (a *app) followArtwork() {
	if !a.settings.Dynamic || a.current.Thumbnail == "" {
		return
	}
	if colour, ok := a.thumbs.colour(a.current.Thumbnail, playerArt); ok {
		a.dynamic, a.dynamicOK = colour, true
	}
}
