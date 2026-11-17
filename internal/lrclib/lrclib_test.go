package lrclib

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/elianiva/meiro/internal/lyrics"
)

func TestParseLRC(t *testing.T) {
	got := ParseLRC("[ar: Someone]\n[00:12.50] Hello\n[01:02.5][00:05]Chorus \nno stamp\n[00:20.00]\n[xx:yy] bad")
	want := []lyrics.Line{
		{At: 5 * time.Second, Text: "Chorus"},
		{At: 12500 * time.Millisecond, Text: "Hello"},
		{At: 20 * time.Second},
		{At: 62500 * time.Millisecond, Text: "Chorus"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestGetFallsBackToSearch(t *testing.T) {
	var gets, searches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/get":
			gets++
			if r.URL.Query().Get("duration") != "222" || r.Header.Get("User-Agent") == "" {
				t.Errorf("bad request %s %v", r.URL, r.Header)
			}
			http.NotFound(w, r)
		case "/api/search":
			searches++
			_, _ = w.Write([]byte(`[{"plainLyrics":"only plain"},{"plainLyrics":"x","syncedLyrics":"[00:01.00] one"}]`))
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	got, err := c.Get(context.Background(), Query{Title: "T", Artist: "A", Duration: 222 * time.Second})
	if err != nil || got == nil || len(got.Synced) != 1 || got.Plain != "x" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if gets != 1 || searches != 1 {
		t.Fatalf("gets %d searches %d", gets, searches)
	}
}

func TestGetNothingFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/search" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	got, err := (&Client{BaseURL: srv.URL}).Get(context.Background(), Query{Title: "T"})
	if err != nil || got != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestParseLRCKeepsTheOriginalOfARomanisedCopy(t *testing.T) {
	got := ParseLRC("[00:01.00] 無敵の笑顔\n[00:04.00] 知りたい\n[03:29.00] 最後\n[00:01.00] Muteki no egao\n[00:04.00] Shiritai\n[03:29.00] Saigo")
	want := []lyrics.Line{{At: time.Second, Text: "無敵の笑顔"}, {At: 4 * time.Second, Text: "知りたい"}, {At: 209 * time.Second, Text: "最後"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	got = ParseLRC("[00:01.00] one\n[00:40.00] two\n[00:01.00] uno\n[00:40.00] dos")
	if len(got) != 2 || got[0].Text != "one" {
		t.Fatalf("got %v", got)
	}
}

func TestGetRetriesAServerErrorAndSearchesAfterAFailure(t *testing.T) {
	var gets, searches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/get":
			gets++
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/api/search":
			searches++
			if searches == 1 {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			if r.URL.Query().Get("track_name") != "ラッキー☆オーブ" {
				t.Errorf("search by %q", r.URL.Query().Get("track_name"))
			}
			_, _ = w.Write([]byte(`[{"plainLyrics":"p","syncedLyrics":"[00:01.00] a"}]`))
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, RetryDelay: time.Millisecond}
	got, err := c.Get(context.Background(), Query{Title: "ラッキー☆オーブ - Lucky Orb (feat. Hatsune Miku)", Artist: "emon(Tes.)"})
	if err != nil || got == nil || len(got.Synced) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if gets != 3 {
		t.Fatalf("a 503 was asked %d times, want 3", gets)
	}
}

func TestGetReportsAFailureWhenNothingWasFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	got, err := (&Client{BaseURL: srv.URL, RetryDelay: time.Millisecond}).Get(context.Background(), Query{Title: "T"})
	if err == nil || got != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
}
