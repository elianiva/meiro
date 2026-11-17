package main

import (
	"slices"
	"strings"
)

var playErrorRules = []struct {
	phrases []string
	message string
}{
	{
		[]string{"not available in your country", "blocked it in your country", "blocked in your country", "geo restrict", "geo-restrict"},
		"This track is not available in your region.",
	},
	{
		[]string{"confirm your age", "age-restricted", "age restricted", "inappropriate for some users"},
		"This track is age-restricted. Sign in to play it.",
	},
	{
		[]string{"not a bot", "sign in to confirm", "login required", "use --cookies"},
		"YouTube wants you to sign in before it plays this track.",
	},
	{
		[]string{"private video", "this video is private"},
		"This track is private.",
	},
	{
		[]string{"premium", "members-only", "members only", "join this channel"},
		"This track is only for Premium or channel members.",
	},
	{
		[]string{"has been removed", "video unavailable", "this video is unavailable", "no longer available", "does not exist", "account associated with this video has been terminated", "copyright"},
		"This track is no longer available.",
	},
	{
		formatUnavailablePhrases,
		"YouTube did not offer audio for this track. Try again later, or update Meiro.",
	},
	{
		[]string{"playing needs ffmpeg"},
		"Playing needs ffmpeg and yt-dlp, and one of them was not found.",
	},
	{
		[]string{
			"name resolution", "no such host", "network is unreachable", "no route to host", "timed out", "timeout",
			"deadline exceeded", "connection reset", "connection refused", "connection closed", "unable to download",
			"failed to resolve", "temporary failure", "end of file", "i/o error", "server returned 5", "transporterror",
		},
		"Could not reach YouTube. Check your connection.",
	},
	{
		[]string{"server returned 403", "server returned 404", "server returned 410", "403 forbidden", "404 not found", "410 gone", "http error 403", "http error 404", "http error 410"},
		"YouTube stopped serving this track's audio.",
	},
	{
		[]string{"invalid data found", "unsupported", "no such file", "audio device"},
		"This track's audio could not be played.",
	},
}

var formatUnavailablePhrases = []string{"requested format is not available", "no video formats found"}

func isFormatUnavailable(raw string) bool {
	lower := strings.ToLower(raw)
	return slices.ContainsFunc(formatUnavailablePhrases, func(p string) bool { return strings.Contains(lower, p) })
}

func playErrorMessage(raw string) string {
	lower := strings.ToLower(raw)
	for _, rule := range playErrorRules {
		for _, phrase := range rule.phrases {
			if strings.Contains(lower, phrase) {
				return rule.message
			}
		}
	}
	if line := strings.TrimSpace(firstLine(raw)); line != "" {
		return "This track could not be played: " + line
	}
	return "This track could not be played."
}
