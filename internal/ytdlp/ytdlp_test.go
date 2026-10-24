package ytdlp

import (
	"testing"
	"time"
)

func TestParseStreamsTellsTheAudioFromThePicture(t *testing.T) {
	audioURL := "https://rr1.googlevideo.com/videoplayback?mime=audio%2Fmp4&dur=201.5&itag=140"
	videoURL := "https://rr1.googlevideo.com/videoplayback?mime=video%2Fmp4&dur=201.4&itag=136"
	tests := []struct {
		name       string
		output     string
		url, video string
		total      time.Duration
	}{
		{"audio then picture", audioURL + "\n" + videoURL + "\n", audioURL, videoURL, 201500 * time.Millisecond},
		{"picture first", videoURL + "\n" + audioURL + "\n", audioURL, videoURL, 201500 * time.Millisecond},
		{"audio alone", audioURL + "\r\n", audioURL, "", 201500 * time.Millisecond},
		{"picture alone", videoURL + "\n", "", videoURL, 0},
		{"no mime", "https://a.test/one\nhttps://a.test/two\n", "https://a.test/one", "https://a.test/two", 0},
		{"nothing", "\n", "", "", 0},
	}
	for _, tc := range tests {
		got := parseStreams(tc.output)
		if got.URL != tc.url || got.Video != tc.video || got.Total != tc.total {
			t.Errorf("%s: got %+v, want url=%q video=%q total=%s", tc.name, got, tc.url, tc.video, tc.total)
		}
	}
}
