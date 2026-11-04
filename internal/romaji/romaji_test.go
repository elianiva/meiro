package romaji

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ikawaha/kagome-dict/ipa"
)

func TestToRomaji(t *testing.T) {
	for _, c := range []struct{ kana, want string }{
		{"きょう", "kyou"},
		{"コーヒー", "kōhī"},
		{"ずっと", "zutto"},
		{"こっち", "kotchi"},
		{"しんぶん", "shimbun"},
		{"しんや", "shin'ya"},
		{"ヴァイオリン", "vaiorin"},
		{"サッカー", "sakkā"},
		{"パーティー", "pātī"},
		{"ありがとう", "arigatou"},
		{"を", "o"},
		{"ん", "n"},
	} {
		if got := toRomaji(c.kana); got != c.want {
			t.Errorf("toRomaji(%q) = %q, want %q", c.kana, got, c.want)
		}
	}
}

func TestCanonicalize(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"sō , sore .", "sō, sore."},
		{`" arigatō "`, `"arigatō"`},
		{"( a )", "(a)"},
		{"mol - 74", "mol-74"},
		{"  one   two  ", "one two"},
		{"sore wa , sore", "sore wa, sore"},
	} {
		if got := canonicalize(c.in); got != c.want {
			t.Errorf("canonicalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRomanize(t *testing.T) {
	r := testRomanizer(t)
	for _, c := range []struct{ line, want string }{
		{"夜に駆ける", "yoru ni kakeru"},
		{"今日はいい天気ですね", "kyō wa ii tenki desu ne"},
		{"君の名前を呼ぶよ", "kimi no namae o yobu yo"},
		{"ありがとう", "arigatō"},
		{"東京", "tōkyō"},
		{"コーヒーを飲む", "kōhī o nomu"},
		{"すもももももももものうち", "sumomo mo momo mo momo no uchi"},
		{"Hello 世界", "Hello sekai"},
		{"「そうか」と彼は言った。", `"sō ka" to kare wa itta.`},
		{"行った", "itta"},
		{"ずっと", "zutto"},
		{"しんぶん", "shimbun"},
		{"日本語の歌詞をローマ字にする", "nihongo no kashi o rōmaji ni suru"},
		{"こんにちは", "konnichiwa"},
	} {
		if got := r.Romanize(c.line); got != c.want {
			t.Errorf("Romanize(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}

func TestLinesOnlyRomanizesJapaneseLyrics(t *testing.T) {
	r := testRomanizer(t)
	chinese := r.Lines([]string{"你好世界", "我们走吧", "Hello"})
	for line, want := range map[string]string{"你好世界": "你好世界", "我们走吧": "我们走吧", "Hello": "Hello"} {
		if got := chinese[line]; got != want {
			t.Errorf("Chinese line %q = %q, want %q", line, got, want)
		}
	}
	japanese := r.Lines([]string{"夜に駆ける", "Hello", "東京"})
	for line, want := range map[string]string{"夜に駆ける": "yoru ni kakeru", "Hello": "Hello", "東京": "tōkyō"} {
		if got := japanese[line]; got != want {
			t.Errorf("Japanese line %q = %q, want %q", line, got, want)
		}
	}
}

func TestDictionaryDownloadsThenLoadsFromDisk(t *testing.T) {
	data := dictionaryBytes(t)
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write(data)
	}))
	path := filepath.Join(t.TempDir(), "ipa.dict")
	d := &Dictionary{path: path, urls: []string{server.URL}, sha256: checksum(data), client: server.Client()}
	if d.Cached() {
		t.Fatal("a dictionary that was never downloaded says it is cached")
	}
	if got := mustLines(t, d, "夜に駆ける"); got != "yoru ni kakeru" {
		t.Fatalf("downloaded dictionary romanized as %q", got)
	}
	if !d.Cached() || requests != 1 {
		t.Fatalf("cached=%v downloads=%d, want one download", d.Cached(), requests)
	}
	// The loaded dictionary is kept, so the server can go away.
	server.Close()
	if got := mustLines(t, d, "夜に駆ける"); got != "yoru ni kakeru" {
		t.Fatalf("cached dictionary romanized as %q", got)
	}
	// A new process reads the file it downloaded instead of the network.
	again := &Dictionary{path: path, urls: []string{"http://127.0.0.1:1/unreachable"}, sha256: checksum(data)}
	if got := mustLines(t, again, "夜に駆ける"); got != "yoru ni kakeru" {
		t.Fatalf("dictionary on disk romanized as %q", got)
	}
}

func TestDictionaryRejectsWhatItCannotTrust(t *testing.T) {
	data := dictionaryBytes(t)
	for _, c := range []struct{ name, sha string }{
		{"wrong checksum", checksum([]byte("something else"))},
		{"no checksum", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(data)
			}))
			t.Cleanup(server.Close)
			dir := t.TempDir()
			d := &Dictionary{path: filepath.Join(dir, "ipa.dict"), urls: []string{server.URL}, sha256: c.sha, client: server.Client()}
			if _, err := d.Lines(context.Background(), []string{"東京"}); err == nil {
				t.Fatal("a dictionary with a bad checksum was accepted")
			}
			if d.Cached() {
				t.Fatal("a rejected download was left on disk")
			}
			if left := tempFiles(t, dir); len(left) != 0 {
				t.Fatalf("rejected download left %v", left)
			}
		})
	}
}

func TestDictionaryRedownloadsADamagedFile(t *testing.T) {
	data := dictionaryBytes(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "ipa.dict")
	if err := os.WriteFile(path, []byte("not a dictionary"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Dictionary{path: path, urls: []string{server.URL}, sha256: checksum(data), client: server.Client()}
	if got := mustLines(t, d, "夜に駆ける"); got != "yoru ni kakeru" {
		t.Fatalf("a damaged file was not replaced: %q", got)
	}
}

func TestUnloadDropsTheLoadedDictionary(t *testing.T) {
	d := &Dictionary{romanizer: testRomanizer(t)}
	d.Unload()
	if d.romanizer != nil {
		t.Fatal("unloaded dictionary was still in memory")
	}
}

func TestDictionaryReportsAFailedDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no dictionary today", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "ipa.dict")
	d := &Dictionary{path: path, urls: []string{server.URL}, sha256: checksum([]byte("x")), client: server.Client()}
	if _, err := d.Lines(context.Background(), []string{"東京"}); err == nil {
		t.Fatal("a failed download was reported as success")
	}
	if d.Cached() {
		t.Fatal("a failed download left a dictionary on disk")
	}
}

func mustLines(t *testing.T, d *Dictionary, line string) string {
	t.Helper()
	got, err := d.Lines(context.Background(), []string{line})
	if err != nil {
		t.Fatal(err)
	}
	return got[line]
}

func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			left = append(left, entry.Name())
		}
	}
	return left
}

var (
	dictOnce sync.Once
	dictZip  []byte
	dictErr  error
)

// dictionaryBytes is the IPADIC dictionary in the zip format Meiro downloads,
// built once for every test from the embedded dictionary of the test binary.
func dictionaryBytes(t *testing.T) []byte {
	t.Helper()
	dictOnce.Do(func() {
		var buffer bytes.Buffer
		w := zip.NewWriter(&buffer)
		if dictErr = ipa.Dict().Save(w); dictErr != nil {
			return
		}
		dictErr = w.Close()
		dictZip = buffer.Bytes()
	})
	if dictErr != nil {
		t.Fatal(dictErr)
	}
	return dictZip
}

var (
	romanizerOnce sync.Once
	romanizer     *Romanizer
	romanizerErr  error
)

// testRomanizer is the embedded IPA dictionary, built once for the tests that
// only read it. The tests that download and read a file build their own.
func testRomanizer(t *testing.T) *Romanizer {
	t.Helper()
	romanizerOnce.Do(func() { romanizer, romanizerErr = newRomanizer(ipa.Dict()) })
	if romanizerErr != nil {
		t.Fatal(romanizerErr)
	}
	return romanizer
}
