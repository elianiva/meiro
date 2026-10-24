package ytdlp

import (
	"strings"
	"testing"
	"time"
)

func TestParseStreamsTellsTheAudioFromThePicture(t *testing.T) {
	audioURL := "https://rr1.googlevideo.com/videoplayback?mime=audio%2Fmp4&dur=201.5&itag=140"
	videoURL := "https://rr1.googlevideo.com/videoplayback?mime=video%2Fmp4&dur=201.4&itag=136"
	fullURL := "https://rr1.googlevideo.com/videoplayback?mime=video%2Fmp4&dur=201.4&itag=137"
	tests := []struct {
		name             string
		output           string
		url, video, full string
		total            time.Duration
	}{
		{"audio then picture", audioURL + "\n" + videoURL + "\n", audioURL, videoURL, "", 201500 * time.Millisecond},
		{"picture first", videoURL + "\n" + audioURL + "\n", audioURL, videoURL, "", 201500 * time.Millisecond},
		{"audio, picture and full screen", audioURL + "\n" + videoURL + "\n" + fullURL + "\n", audioURL, videoURL, fullURL, 201500 * time.Millisecond},
		{"nothing sharper for full screen", audioURL + "\n" + videoURL + "\n" + videoURL + "\n", audioURL, videoURL, "", 201500 * time.Millisecond},
		{"audio alone", audioURL + "\r\n", audioURL, "", "", 201500 * time.Millisecond},
		{"picture alone", videoURL + "\n", "", videoURL, "", 0},
		{"no mime", "https://a.test/one\nhttps://a.test/two\n", "https://a.test/one", "https://a.test/two", "", 0},
		{"nothing", "\n", "", "", "", 0},
	}
	for _, tc := range tests {
		got := parseStreams(tc.output)
		if got.URL != tc.url || got.Video != tc.video || got.FullScreenVideo != tc.full || got.Total != tc.total {
			t.Errorf("%s: got %+v, want url=%q video=%q full=%q total=%s", tc.name, got, tc.url, tc.video, tc.full, tc.total)
		}
	}

	// As resolve asks for them: each line the stream's frame rate, then its
	// URL. Each picture keeps its own rate.
	got := parseStreams(strings.Join([]string{"NA " + audioURL, "60 " + videoURL, "30 " + fullURL, ""}, "\n"))
	if got.URL != audioURL || got.Video != videoURL || got.VideoFPS != 60 || got.FullScreenVideo != fullURL || got.FullScreenVideoFPS != 30 {
		t.Errorf("with frame rates: got %+v", got)
	}
}
