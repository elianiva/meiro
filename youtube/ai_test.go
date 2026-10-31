package youtube

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAIDisclosureReadsThePrimaryVideoBadge(t *testing.T) {
	var request struct {
		VideoID string `json:"videoId"`
		Context struct {
			Client struct {
				ClientName string `json:"clientName"`
			} `json:"client"`
		} `json:"context"`
		AudioOnly bool `json:"isAudioOnly"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/next" {
			t.Errorf("path = %q, want /youtubei/v1/next", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{
			"contents":{"twoColumnWatchNextResults":{"results":{"results":{"contents":[
				{"videoPrimaryInfoRenderer":{"title":{"runs":[{"text":"Song"}]},"badges":[
					{"metadataBadgeRenderer":{"icon":{"iconType":"INFO"},"label":"AI",
						"accessibilityData":{"label":"AI: Content was made with AI"}}}
				]}}
			]}}}},
			"related":{"videoRenderer":{"title":{"runs":[{"text":"AI in a title"}]}}}
		}`))
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL})
	status, err := client.AIDisclosure(context.Background(), "video-1")
	if err != nil {
		t.Fatal(err)
	}
	if status != AIAlteredOrGenerated {
		t.Fatalf("status = %d, want AIAlteredOrGenerated", status)
	}
	if request.VideoID != "video-1" || request.Context.Client.ClientName != "WEB" {
		t.Errorf("request = %+v, want video-1 asked as WEB", request)
	}
	if request.AudioOnly {
		t.Error("watch-page lookup asked as audio-only")
	}
}

func TestAIDisclosureOnlyTrustsThePrimaryBadge(t *testing.T) {
	const badge = `{"metadataBadgeRenderer":{"icon":{"iconType":"INFO"},"label":"AI","accessibilityData":{"label":"AI: Content was made with AI"}}}`
	for _, test := range []struct {
		name string
		body string
		want AIDisclosure
	}{
		{"marked", `{"videoPrimaryInfoRenderer":{"badges":[` + badge + `]}}`, AIAlteredOrGenerated},
		{"not marked", `{"videoPrimaryInfoRenderer":{"title":{"runs":[{"text":"AI Generated Flow"}]},"badges":[]}}`, AINone},
		{"related only", `{"videoPrimaryInfoRenderer":{"badges":[]},"videoRenderer":{"badges":[` + badge + `]}}`, AINone},
		{"no primary", `{"videoRenderer":{"badges":[` + badge + `]}}`, AIUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := aiDisclosure(decodeResponse(json.RawMessage(test.body))); got != test.want {
				t.Errorf("aiDisclosure() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestAIDisclosureRequiresAVideo(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	if _, err := NewClient(Options{BaseURL: server.URL}).AIDisclosure(context.Background(), "  "); err == nil {
		t.Fatal("AIDisclosure accepted an empty video ID")
	}
	if called {
		t.Error("an empty video ID was sent to YouTube")
	}
}
