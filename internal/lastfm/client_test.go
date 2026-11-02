package lastfm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestSignKnownAnswers(t *testing.T) {
	// Expected values come from md5sum over the documented recipe: parameters
	// in name order, name then value, then the secret.
	got := Sign(url.Values{"token": {"yyy"}, "method": {"auth.getSession"}, "api_key": {"xxx"}}, "secret")
	if want := "16f6531c029f18b4bef6b36971bbdf4c"; got != want {
		t.Errorf("Sign = %s, want %s", got, want)
	}
	// format and callback are not signed, and the value is signed as UTF-8.
	params := url.Values{
		"api_key": {"k1"}, "method": {"track.scrobble"}, "sk": {"SK"},
		"artist[0]": {"Beyoncé"}, "track[0]": {"Halo"}, "timestamp[0]": {"100"},
		"format": {"json"}, "callback": {"x"},
	}
	if got, want := Sign(params, "s3"), "c0545b5403a2df57f77d39c0eddfc5f2"; got != want {
		t.Errorf("Sign = %s, want %s", got, want)
	}
}

// fakeLastFM answers the API and records what it was sent.
type fakeLastFM struct {
	form  url.Values
	reply string
	code  int
}

func (f *fakeLastFM) client(t *testing.T) *Client {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		f.form, _ = url.ParseQuery(string(body))
		if f.code != 0 {
			w.WriteHeader(f.code)
		}
		_, _ = w.Write([]byte(f.reply))
	}))
	t.Cleanup(server.Close)
	return &Client{Key: "k1", Secret: "s3", Root: server.URL}
}

func TestScrobbleSendsSignedBatch(t *testing.T) {
	f := &fakeLastFM{reply: `{"scrobbles":{"@attr":{"accepted":2,"ignored":0}}}`}
	c := f.client(t)
	started := time.Unix(1700000000, 0)
	err := c.Scrobble(context.Background(), "SK", []Scrobble{
		{Track: Track{Artist: "A", Title: "One", Album: "Al", Duration: 215 * time.Second}, Started: started, Chosen: true},
		{Track: Track{Artist: "B", Title: "Two"}, Started: started.Add(time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"method": "track.scrobble", "api_key": "k1", "sk": "SK", "format": "json",
		"artist[0]": "A", "track[0]": "One", "album[0]": "Al", "duration[0]": "215",
		"timestamp[0]": "1700000000", "chosenByUser[0]": "1",
		"artist[1]": "B", "track[1]": "Two", "timestamp[1]": "1700000060", "chosenByUser[1]": "0",
	}
	for name, value := range want {
		if got := f.form.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	if f.form.Has("album[1]") || f.form.Has("duration[1]") {
		t.Errorf("empty album or duration was sent: %v", f.form)
	}
	if got, signed := f.form.Get("api_sig"), Sign(f.form, "s3"); got != signed || got == "" {
		t.Errorf("api_sig = %q, want %q", got, signed)
	}
	for name, values := range f.form {
		if values[0] == "s3" {
			t.Errorf("the secret was sent as %s", name)
		}
	}
}

func TestErrorClasses(t *testing.T) {
	for _, tc := range []struct {
		name               string
		reply              string
		code               int
		transient, invalid bool
	}{
		{"invalid session", `{"error":9,"message":"Invalid session key"}`, 403, false, true},
		{"service offline", `{"error":11,"message":"offline"}`, 503, true, false},
		{"rate limit", `{"error":29,"message":"limit"}`, 429, true, false},
		{"bad parameters", `{"error":6,"message":"Invalid parameters"}`, 400, false, false},
		{"bad signature", `{"error":13,"message":"Invalid method signature"}`, 403, false, false},
		{"server error without body", `oops`, 502, true, false},
		{"unexpected 4xx", `nope`, 404, false, false},
	} {
		f := &fakeLastFM{reply: tc.reply, code: tc.code}
		err := f.client(t).Scrobble(context.Background(), "SK", []Scrobble{{Track: Track{Artist: "A", Title: "T"}}})
		if err == nil {
			t.Fatalf("%s: no error", tc.name)
		}
		if Transient(err) != tc.transient || InvalidSession(err) != tc.invalid {
			t.Errorf("%s: transient=%t invalid=%t, want %t %t", tc.name, Transient(err), InvalidSession(err), tc.transient, tc.invalid)
		}
	}
	// No answer at all is the network failing.
	c := &Client{Key: "k", Secret: "s", Root: "http://127.0.0.1:1/"}
	if err := c.Scrobble(context.Background(), "SK", []Scrobble{{}}); !Transient(err) {
		t.Errorf("connection refused: %v, want transient", err)
	}
	if Transient(context.Canceled) || Transient(nil) {
		t.Error("a cancelled call is not worth retrying")
	}
	if InvalidSession(errors.New("x")) {
		t.Error("plain error is not an invalid session")
	}
}
