package danklyrics

import (
	"context"
	"errors"
	"testing"
	"time"

	dux "github.com/duxlabs-dev/dux-go/lyrics"

	"github.com/elianiva/meiro/internal/lyrics"
)

func wordResult(source dux.Provider, name string) *dux.Result {
	return &dux.Result{
		Found:       true,
		Source:      source,
		Attribution: dux.Attribution{Name: name},
		Lyrics: dux.Lyrics{Synced: []dux.Line{
			{Start: 1.5, Text: "Hi there", Words: []dux.Word{
				{Start: 1.5, Text: "Hi "},
				{Start: 2, Text: "there"},
			}},
		}},
	}
}

func lineResult() *dux.Result {
	return &dux.Result{
		Found:       true,
		Source:      dux.LRCLIB,
		Attribution: dux.Attribution{Name: "LRCLIB"},
		Lyrics:      dux.Lyrics{Synced: []dux.Line{{Start: 0, Text: "line"}}},
	}
}

func plainResult() *dux.Result {
	return &dux.Result{
		Found:       true,
		Source:      dux.LRCLIB,
		Attribution: dux.Attribution{Name: "LRCLIB"},
		Lyrics:      dux.Lyrics{Plain: "plain words"},
	}
}

func isSlow(request dux.Request) bool {
	return len(request.Providers) == 1 && request.Providers[0] == dux.KuGou
}

func TestLookupConvertsLinesWordsAndSource(t *testing.T) {
	calls := 0
	fetch := func(_ context.Context, request dux.Request) (*dux.Result, error) {
		calls++
		if !request.CacheOnly || request.Duration != 0 || isSlow(request) {
			t.Errorf("first attempt was %+v", request)
		}
		return wordResult(dux.BetterLyrics, "Better Lyrics"), nil
	}
	got, err := Lookup(context.Background(), fetch, lyrics.Query{Title: "T", Artist: "A", Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("looked up %d times", calls)
	}
	if got.Source != "Better Lyrics" || len(got.Synced) != 1 || got.Synced[0].Text != "Hi there" {
		t.Fatalf("got %#v", got)
	}
	if got.Synced[0].At != 1500*time.Millisecond {
		t.Fatalf("line starts at %v", got.Synced[0].At)
	}
	if len(got.Synced[0].Words) != 2 || got.Synced[0].Words[1].At != 2*time.Second {
		t.Fatalf("words are %#v", got.Synced[0].Words)
	}
}

func TestLookupAsksWithTheLengthWhenTheBroadAttemptHasOnlyLines(t *testing.T) {
	cacheOnly := 0
	var network []time.Duration
	fetch := func(_ context.Context, request dux.Request) (*dux.Result, error) {
		if request.CacheOnly {
			cacheOnly++
			return &dux.Result{}, nil
		}
		if isSlow(request) {
			t.Errorf("asked KuGou before trying the fast providers with the length")
		}
		network = append(network, request.Duration)
		if request.Duration == 0 {
			return lineResult(), nil
		}
		return wordResult(dux.BetterLyrics, "Better Lyrics"), nil
	}
	got, err := Lookup(context.Background(), fetch, lyrics.Query{Title: "T", Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if cacheOnly != 4 {
		t.Fatalf("read the cache %d times", cacheOnly)
	}
	if len(network) != 2 || network[0] != 0 || network[1] != time.Minute {
		t.Fatalf("asked the network with %v", network)
	}
	if got.Source != "Better Lyrics" || len(got.Synced) != 1 || len(got.Synced[0].Words) == 0 {
		t.Fatalf("got %#v", got)
	}
}

func TestLookupFallsBackToKuGouWhenTheFastProvidersMiss(t *testing.T) {
	calls := 0
	fetch := func(_ context.Context, request dux.Request) (*dux.Result, error) {
		calls++
		if request.CacheOnly {
			return &dux.Result{}, nil
		}
		if isSlow(request) && request.Duration == 0 {
			return wordResult(dux.KuGou, "KuGou"), nil
		}
		return &dux.Result{}, nil
	}
	got, err := Lookup(context.Background(), fetch, lyrics.Query{Title: "T", Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 8 {
		t.Fatalf("looked up %d times", calls)
	}
	if got.Source != "KuGou" || len(got.Synced) != 1 {
		t.Fatalf("got %#v", got)
	}
}

func TestLookupKeepsPlainWhenTheSlowAttemptsFail(t *testing.T) {
	calls := 0
	fetch := func(_ context.Context, request dux.Request) (*dux.Result, error) {
		calls++
		switch {
		case request.CacheOnly:
			return &dux.Result{}, nil
		case isSlow(request):
			return nil, errors.New("offline")
		case request.Duration == 0:
			return plainResult(), nil
		default:
			return &dux.Result{}, nil
		}
	}
	got, err := Lookup(context.Background(), fetch, lyrics.Query{Title: "T", Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 8 {
		t.Fatalf("looked up %d times", calls)
	}
	if got.Plain != "plain words" || got.Source != "LRCLIB" {
		t.Fatalf("got %#v", got)
	}
}

func TestLookupReportsAnErrorWhenEveryAttemptFails(t *testing.T) {
	calls := 0
	fetch := func(context.Context, dux.Request) (*dux.Result, error) {
		calls++
		return nil, errors.New("offline")
	}
	if _, err := Lookup(context.Background(), fetch, lyrics.Query{Title: "T", Duration: time.Minute}); err == nil {
		t.Fatal("a failed lookup was reported as no lyrics")
	}
	if calls != 8 {
		t.Fatalf("looked up %d times", calls)
	}
}

func TestLookupCapsASlowAttempt(t *testing.T) {
	p := defaultPolicy
	p.fastTimeout = 10 * time.Millisecond
	p.slowTimeout = 10 * time.Millisecond
	calls := 0
	fetch := func(ctx context.Context, request dux.Request) (*dux.Result, error) {
		calls++
		if request.CacheOnly {
			return &dux.Result{}, nil
		}
		if !isSlow(request) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Second):
				return nil, errors.New("the attempt was not capped")
			}
		}
		return wordResult(dux.KuGou, "KuGou"), nil
	}
	began := time.Now()
	got, err := lookup(context.Background(), fetch, lyrics.Query{Title: "T", Duration: time.Minute}, p)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took > time.Second {
		t.Fatalf("a slow provider held the lookup for %v", took)
	}
	if got.Source != "KuGou" || len(got.Synced) != 1 {
		t.Fatalf("got %#v", got)
	}
	if calls != 7 {
		t.Fatalf("looked up %d times", calls)
	}
}

func TestLookupDoesNotRepeatTheLengthWhenItIsUnknown(t *testing.T) {
	calls := 0
	fetch := func(_ context.Context, request dux.Request) (*dux.Result, error) {
		calls++
		if request.Duration != 0 {
			t.Errorf("asked with duration %v", request.Duration)
		}
		return &dux.Result{}, nil
	}
	if _, err := Lookup(context.Background(), fetch, lyrics.Query{Title: "T"}); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatalf("looked up %d times", calls)
	}
}
