package audio

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/elianiva/meiro/internal/proc"
)

// Probe reads how long an audio file plays. The cached tracks carry their
// length in their header, and ffmpeg is already at hand to decode them, so it
// is asked to open the file: it prints the length it finds, and fails for the
// output it was not given. It returns 0 when the length cannot be read.
func Probe(ctx context.Context, path string) time.Duration {
	ffmpeg, err := proc.Tool("ffmpeg")
	if err != nil {
		return 0
	}
	output, _ := proc.Command(ctx, ffmpeg, "-hide_banner", "-i", path).CombinedOutput()
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
