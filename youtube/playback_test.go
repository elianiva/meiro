package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseLoudnessPrefersPerceptualWhenIntegratedLoudnessIsZero(t *testing.T) {
	raw := json.RawMessage(`{"playerConfig":{"audioConfig":{"loudnessDb":0,"perceptualLoudnessDb":-12.5}}}`)
	got, err := parseLoudness(raw)
	if err != nil || got.LoudnessDB != -12.5 {
		t.Fatalf("parseLoudness() = %#v, %v; want -12.5, nil", got, err)
	}
}

func TestParseLoudnessRejectsUnavailableOrMalformedResponses(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"playabilityStatus":{"status":"UNPLAYABLE"}}`),
		json.RawMessage(`{"playerConfig":{"audioConfig":{}}}`),
		json.RawMessage(`{"streamingData":{"formats":[{"audioTrack":{"loudnessDb":9}}]}}`),
		json.RawMessage(`not json`),
	} {
		if got, err := parseLoudness(raw); err == nil || got != (Loudness{}) {
			t.Errorf("parseLoudness(%s) = %#v, %v; want empty and an error", raw, got, err)
		}
	}
}

func TestParseAudioStreamPicksTheBestDirectAudio(t *testing.T) {
	raw := json.RawMessage(`{"playabilityStatus":{"status":"OK"},"streamingData":{"adaptiveFormats":[
		{"mimeType":"video/mp4; codecs=\"avc1\"","bitrate":900000,"url":"https://v/video","approxDurationMs":"1000"},
		{"mimeType":"audio/mp4; codecs=\"mp4a.40.2\"","bitrate":130000,"url":"https://v/140","approxDurationMs":"231746"},
		{"mimeType":"audio/webm; codecs=\"opus\"","bitrate":200000,"signatureCipher":"s=x","approxDurationMs":"231761"},
		{"mimeType":"audio/webm; codecs=\"opus\"","bitrate":149000,"url":"https://v/251-drc","approxDurationMs":"231761","isDrc":true},
		{"mimeType":"audio/webm; codecs=\"opus\"","bitrate":160000,"url":"https://v/251-dub","approxDurationMs":"231761","audioTrack":{"audioIsDefault":false}},
		{"mimeType":"audio/webm; codecs=\"opus\"","bitrate":148000,"url":"https://v/251","approxDurationMs":"231761","audioTrack":{"audioIsDefault":true}},
		{"mimeType":"audio/webm; codecs=\"opus\"","bitrate":58000,"url":"https://v/250","approxDurationMs":"231761"}]}}`)
	url, length, err := parseAudioStream(raw)
	if err != nil || url != "https://v/251" || length != 231761*time.Millisecond {
		t.Fatalf("parseAudioStream() = %q, %v, %v; want the original-language 148 kb/s opus without DRC, 3m51.761s, nil", url, length, err)
	}
}

func TestParseAudioStreamRejectsUnplayableOrCipheredResponses(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"playabilityStatus":{"status":"LOGIN_REQUIRED","reason":"Sign in to confirm you're not a bot"}}`),
		json.RawMessage(`{"playabilityStatus":{"status":"OK"},"streamingData":{"adaptiveFormats":[{"mimeType":"audio/webm","bitrate":1,"signatureCipher":"s=x"}]}}`),
		json.RawMessage(`not json`),
	} {
		if url, _, err := parseAudioStream(raw); err == nil || url != "" {
			t.Errorf("parseAudioStream(%s) = %q, %v; want no URL and an error", raw, url, err)
		}
	}
}

// The player answers a request without a visitor with a bot check, so a
// client that has not browsed yet asks for a visitor first and sends it. The
// URL is only handed on once the media server serves it.
func TestAudioStreamAsksAsAVisitorOfTheVisionOSClient(t *testing.T) {
	var media string
	var refuse atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/youtubei/v1/visitor_id":
			_, _ = w.Write([]byte(`{"responseContext":{"visitorData":"V1"}}`))
		case "/youtubei/v1/player":
			var asked struct {
				Context struct {
					Client struct{ ClientName, VisitorData string }
				}
				VideoID string
			}
			_ = json.NewDecoder(r.Body).Decode(&asked)
			if asked.Context.Client.ClientName != "VISIONOS" || asked.Context.Client.VisitorData != "V1" ||
				r.Header.Get("X-Goog-Visitor-Id") != "V1" || asked.VideoID != "abc" {
				_, _ = w.Write([]byte(`{"playabilityStatus":{"status":"LOGIN_REQUIRED"}}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"playabilityStatus":{"status":"OK"},"streamingData":{"adaptiveFormats":[
				{"mimeType":"audio/webm; codecs=\"opus\"","bitrate":148000,"url":%q,"approxDurationMs":"2000"}]}}`, media)
		case "/media":
			if r.Header.Get("Range") != "bytes=0-0" || refuse.Load() {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusPartialContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	media = server.URL + "/media"
	client := NewClient(Options{BaseURL: server.URL})
	url, length, err := client.AudioStream(context.Background(), "abc")
	if err != nil || url != media || length != 2*time.Second {
		t.Fatalf("AudioStream() = %q, %v, %v; want %s, 2s, nil", url, length, err, media)
	}
	// A URL the media server refuses is no answer: the caller falls back.
	refuse.Store(true)
	if url, _, err := client.AudioStream(context.Background(), "abc"); err == nil {
		t.Fatalf("AudioStream() = %q, nil with the media server refusing; want an error", url)
	}
}
