package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/mcpcontrol"
	"github.com/elianiva/meiro/youtube"
)

func TestMCPQueueAddReusesSearchMetadata(t *testing.T) {
	a := newTestApp(t)
	if got := a.mcpTrackByID("9cHbvRUALrc"); got.Title != "9cHbvRUALrc" {
		t.Fatalf("unknown ID: got title %q", got.Title)
	}
	a.cacheMCPSearchResults([]youtube.MusicItem{{VideoID: "9cHbvRUALrc", Title: "Pink + White", Artists: "Frank Ocean", Duration: "3:04"}})
	got := a.mcpTrackByID("9cHbvRUALrc")
	if got.Title != "Pink + White" || got.Artists != "Frank Ocean" || got.Duration != "3:04" {
		t.Fatalf("search metadata lost: %+v", got)
	}
}

func TestMCPGateAndLimits(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if _, e := a.mcpCall(ctx, "now_playing", json.RawMessage(`{}`)); e == nil {
		t.Fatal("disabled access")
	}
	a.settings.MCPControl = true
	for _, name := range []string{"now_playing", "queue_list"} {
		if _, e := a.mcpCall(ctx, name, json.RawMessage(`{}`)); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := a.mcpCall(ctx, "queue_add", json.RawMessage(`{"video_ids":["bad"],"position":"end"}`)); e == nil {
		t.Fatal("invalid ID accepted")
	}
	if _, e := a.mcpCall(ctx, "playback", json.RawMessage(`{"action":"pause"}`)); e != nil {
		t.Fatal(e)
	}
	if _, e := a.mcpCall(ctx, "playback", json.RawMessage(`{"action":"pause"}`)); e != nil {
		t.Fatal("local playback throttled")
	}
}

func TestMCPRequiredArguments(t *testing.T) {
	a := newTestApp(t)
	a.settings.MCPControl = true
	for _, name := range []string{"queue_move", "queue_remove", "play_now", "seek", "set_volume", "set_shuffle", "set_repeat"} {
		if _, err := a.mcpCall(context.Background(), name, json.RawMessage(`{}`)); err == nil {
			t.Fatalf("%s accepted missing arguments", name)
		}
	}
}

func TestMCPLocalAndNetworkBudgets(t *testing.T) {
	a := newTestApp(t)
	now := time.Now()
	if a.mcpCommandLimit(now, true) != nil || a.mcpCommandLimit(now, true) == nil {
		t.Fatal("network gap missing")
	}
	for range 120 {
		if a.mcpCommandLimit(now, false) != nil {
			t.Fatal("local gap applied")
		}
	}
	if a.mcpCommandLimit(now, false) == nil {
		t.Fatal("local cap missing")
	}
}

func TestMCPPlayNowSelectsDislikedTrackExplicitly(t *testing.T) {
	a, resolver := playbackApp(t)
	a.settings.MCPControl = true
	a.settings.AutoSkipDisliked = true
	items := tracks("current", "next")
	a.play(items[0], items)
	const requested = "9cHbvRUALrc"
	a.cacheMCPSearchResults([]youtube.MusicItem{{VideoID: requested, Like: youtube.Disliked}})

	if _, err := a.mcpCall(context.Background(), "play_now", json.RawMessage(`{"video_id":"9cHbvRUALrc"}`)); err != nil {
		t.Fatal(err)
	}
	if a.current.VideoID != requested || a.queue.index != 1 || !resolver.asked(requested) {
		t.Fatalf("wanted explicit playback of %q at index 1, got %q at %d", requested, a.current.VideoID, a.queue.index)
	}
}

func TestMCPArgumentValidation(t *testing.T) {
	a := newTestApp(t)
	a.settings.MCPControl = true
	for _, test := range []struct{ name, args string }{
		{"now_playing", `{"query":"track"}`},
		{"queue_list", `{"limit":0}`},
		{"queue_list", `{"limit":51}`},
		{"queue_list", `{"offset":null}`},
		{"search", `{"query":"test","kind":""}`},
		{"set_volume", `{"Volume":10}`},
		{"set_shuffle", `{"enabled":null}`},
		{"queue_clear", `{"confirm":false}`},
		{"queue_clear", `{}`},
	} {
		if _, err := a.mcpCall(t.Context(), test.name, json.RawMessage(test.args)); err == nil {
			t.Fatalf("%s accepted %s", test.name, test.args)
		}
	}
}

func TestMCPQueueToolsPreserveCurrentDuplicate(t *testing.T) {
	a := newTestApp(t)
	a.settings.MCPControl = true
	a.settings.AutoPlay = false
	items := tracks("duplicate", "current copy", "tail", "upcoming")
	items[1].VideoID = items[0].VideoID
	a.queue.items, a.queue.index, a.current = items, 1, items[1]
	call := func(tool, args string) mcpState {
		t.Helper()
		value, err := a.mcpCall(t.Context(), tool, json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		return value.(mcpState)
	}
	if got := call("queue_move", `{"index":2,"to":0}`); got.Index != 2 || a.queue.current().Title != items[1].Title {
		t.Fatalf("queue move detached the current entry: %+v", got)
	}
	if got := call("queue_remove", `{"index":0}`); got.Index != 1 || got.Total != 3 {
		t.Fatalf("queue remove: %+v", got)
	}
	if _, err := a.mcpCall(t.Context(), "queue_clear", nil); err == nil || a.queue.len() != 3 {
		t.Fatal("queue changed without confirmation")
	}
	if got := call("queue_clear", `{"confirm":true}`); got.Index != 1 || got.Total != 2 {
		t.Fatalf("queue clear: %+v", got)
	}
}

func TestMCPControlStateAndSearch(t *testing.T) {
	a := newTestApp(t)
	a.settings.MCPControl = true
	call := func(tool, args string) any {
		t.Helper()
		value, err := a.mcpCall(t.Context(), tool, json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := call("search", `{"query":"song"}`).(mcpItems); len(got.Items) == 0 || got.Items[0].Title != "Filtered Song" {
		t.Fatalf("search results: %+v", got)
	}
	if got := call("set_volume", `{"volume":37.5}`).(mcpState); got.Volume != 37.5 || a.settings.Volume != 37.5 {
		t.Fatalf("volume: %+v", got)
	}
	if got := call("set_repeat", `{"mode":2}`).(mcpState); got.Repeat != repeatTrack {
		t.Fatalf("repeat: %+v", got)
	}
	if got := call("set_shuffle", `{"enabled":true}`).(mcpState); !got.Shuffle {
		t.Fatalf("shuffle: %+v", got)
	}
	if _, err := a.mcpCall(t.Context(), "seek", json.RawMessage(`{"seconds":0}`)); err == nil || !strings.Contains(err.Error(), "start playback") {
		t.Fatalf("stopped seek: %v", err)
	}
	if got := mcpTrackFromItem(youtube.MusicItem{Artist: "Fallback"}, false); got.Artists != "Fallback" {
		t.Fatalf("artist fallback: %+v", got)
	}
}

func TestMCPSetupPreservesLaunchArguments(t *testing.T) {
	const executable = `C:\Music Apps\Meiro's + music\Meiro.exe`
	launch := mcpLaunchFor(executable, "windows", "", "")
	var config struct{ MCPServers map[string]mcpLaunch }
	if err := json.Unmarshal([]byte(launch.config()), &config); err != nil ||
		config.MCPServers["meiro"].Command != executable || !slices.Equal(config.MCPServers["meiro"].Args, []string{"--mcp"}) {
		t.Fatalf("config: %s", launch.config())
	}
	prompt := launch.setupPrompt()
	if !strings.Contains(prompt, launch.config()) {
		t.Fatal("setup prompt is missing the launch instructions")
	}
	flatpak := mcpLaunchFor("/app/bin/meiro", "linux", "com.elianiva.meiro", "")
	if flatpak.Command != "flatpak" || !slices.Equal(flatpak.Args, []string{"run", "--command=meiro", "com.elianiva.meiro", "--mcp"}) {
		t.Fatalf("Flatpak launch: %+v", flatpak)
	}
	bundle := "/Applications/Meiro.app/Contents/MacOS/Meiro"
	if launch := mcpLaunchFor(bundle, "darwin", "", ""); launch.Command != bundle {
		t.Fatalf("macOS launch: %+v", launch)
	}
	appImage := t.TempDir()
	if launch := mcpLaunchFor("/usr/bin/meiro", "linux", "", appImage); launch.Command != appImage {
		t.Fatalf("AppImage: %+v", launch)
	}
	if launch := mcpLaunchFor("/usr/bin/meiro", "linux", "", "relative.AppImage"); launch.Command != "/usr/bin/meiro" {
		t.Fatalf("relative AppImage: %+v", launch)
	}
}

func TestMCPSettingsShowSetupAndListenerFailure(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/settings")
	a.settings.MCPControl = true
	a.mcpError = "test socket conflict"
	tester := ui.NewTester(a.view, 1200, 1200)
	if err := tester.Click("Search settings"); err != nil {
		t.Fatal("settings search unavailable")
	}
	tester.Type("mcp")
	visible := strings.Join(tester.Texts(), "\n")
	if !strings.Contains(visible, "test socket conflict") || strings.Contains(visible, "Copy JSON config") {
		t.Fatalf("unexpected MCP settings: %s", visible)
	}
	if err := tester.Click("Copy setup prompt"); err != nil || !strings.Contains(tester.Clipboard(), "Connect my running Meiro") {
		t.Fatal("setup prompt unavailable")
	}
	if err := tester.Click("Advanced"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("Copy JSON config"); err != nil || !strings.Contains(tester.Clipboard(), "mcpServers") {
		t.Fatal("manual config unavailable")
	}
	if err := tester.Click("Allow AI control"); err != nil || a.settings.MCPControl || a.mcpAdvanced {
		t.Fatalf("switch did not disable AI control and close Advanced: %v", err)
	}
	if strings.Contains(strings.Join(tester.Texts(), "\n"), "Copy setup prompt") {
		t.Fatal("setup is visible when AI control is off")
	}
}

func TestMCPQueueBoundaryAndPaging(t *testing.T) {
	a := newTestApp(t)
	a.settings.MCPControl = true
	call := func(name, args string) any {
		t.Helper()
		value, err := a.mcpCall(t.Context(), name, json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if page := call("queue_list", `{}`).(mcpPage); page.Index != -1 || page.RecommendationsStart != -1 || len(page.Items) != 0 {
		t.Fatalf("empty queue: %+v", page)
	}
	base := tracks("current", "manual", "recommended", "also recommended")
	a.queue.items, a.current = base, base[0]
	a.queue.recStart = 2
	for _, item := range base[2:] {
		a.queue.generated[item.VideoID] = struct{}{}
	}
	call("queue_add", `{"video_ids":["aaaaaaaaaaa","bbbbbbbbbbb"],"position":"next"}`)
	call("queue_add", `{"video_ids":["ccccccccccc"],"position":"end"}`)
	page := call("queue_list", `{"offset":1,"limit":5}`).(mcpPage)
	if page.Total != 7 || page.Offset != 1 || page.RecommendationsStart != 5 ||
		page.Items[0].VideoID != "aaaaaaaaaaa" || page.Items[1].VideoID != "bbbbbbbbbbb" ||
		page.Items[3].VideoID != "ccccccccccc" || page.Items[4].VideoID != base[2].VideoID {
		t.Fatalf("queue page: %+v", page)
	}
	call("set_shuffle", `{"enabled":true}`)
	page = call("queue_list", `{"offset":5}`).(mcpPage)
	if page.RecommendationsStart != 5 || len(page.Items) != 2 || page.Items[0].VideoID != base[2].VideoID {
		t.Fatalf("shuffled tail: %+v", page)
	}
	if page := call("queue_list", `{"offset":10}`).(mcpPage); page.Total != 7 || len(page.Items) != 0 {
		t.Fatalf("past queue end: %+v", page)
	}
}

func TestMCPCollectionPlayback(t *testing.T) {
	for _, test := range []struct {
		id      string
		shuffle bool
	}{
		{"MPREb_test", false}, {"PL_paginated", true}, {fakeArtist, false},
	} {
		t.Run(test.id, func(t *testing.T) {
			a, _ := playbackApp(t)
			a.settings.MCPControl = true
			a.shuffle = !test.shuffle
			a.queue.items = tracks("previous")
			a.current = a.queue.items[0]
			raw := fmt.Appendf(nil, `{"collection_id":%q,"shuffle":%t}`, test.id, test.shuffle)
			result, err := a.mcpCall(t.Context(), "play_now", raw)
			if err != nil {
				t.Fatal(err)
			}
			state := result.(mcpState)
			if state.Shuffle != test.shuffle || state.Track.VideoID == "" || state.Track.Title == "previous" || state.Total < 1 {
				t.Fatalf("collection state: %+v", state)
			}
			if _, err := a.mcpCall(t.Context(), "play_now", raw); err == nil || !strings.Contains(err.Error(), "rate limit") {
				t.Fatalf("collection rate limit: %v", err)
			}
		})
	}
}

func TestMCPLikeAndRadio(t *testing.T) {
	a, _ := playbackApp(t)
	a.settings.MCPControl = true
	if _, err := a.mcpCall(t.Context(), "like", json.RawMessage(`{"rating":"like"}`)); err == nil || !strings.Contains(err.Error(), "sign in to Meiro") {
		t.Fatalf("signed-out rating: %v", err)
	}
	a.current = youtube.MusicItem{VideoID: "9cHbvRUALrc", Title: "Seed"}
	a.queue.items = append([]youtube.MusicItem{a.current}, tracks("old upcoming")...)
	result, err := a.mcpCall(t.Context(), "start_radio", nil)
	if err != nil {
		t.Fatal(err)
	}
	if state := result.(mcpState); state.Track.VideoID != a.current.VideoID || slices.ContainsFunc(a.queue.items, func(item youtube.MusicItem) bool { return item.Title == "old upcoming" }) {
		t.Fatalf("radio: %+v", state)
	}

	resetFakes()
	signed := signedInApp(t)
	signed.settings.MCPControl = true
	signed.current = youtube.MusicItem{VideoID: "9cHbvRUALrc", Title: "Seed"}
	signed.queue.items = []youtube.MusicItem{signed.current}
	checkRating := func(want string) {
		t.Helper()
		state, err := signed.mcpCall(t.Context(), "now_playing", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := state.(mcpState).Track.Rating; got != want {
			t.Fatalf("now_playing rating = %q, want %q", got, want)
		}
		page, err := signed.mcpCall(t.Context(), "queue_list", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := page.(mcpPage).Items[0].Rating; got != want {
			t.Fatalf("queue_list rating = %q, want %q", got, want)
		}
	}
	checkRating("unknown")
	signed.learnLikes([]youtube.MusicItem{{VideoID: signed.current.VideoID, Like: youtube.Liked}})
	checkRating("like")
	for _, rating := range []string{"like", "like", "dislike", "none"} {
		result, err := signed.mcpCall(t.Context(), "like", fmt.Appendf(nil, `{"rating":%q}`, rating))
		if err != nil || result != (mcpRating{"9cHbvRUALrc", rating}) {
			t.Fatalf("rating %s: %v %v", rating, result, err)
		}
		checkRating(rating)
	}
	if likeWrites.Load() != 4 || signed.current.Like != youtube.Indifferent {
		t.Fatalf("rating writes: %d, status %v", likeWrites.Load(), signed.current.Like)
	}
}

func TestMCPThreadDoesNotRunAfterCancelOrClose(t *testing.T) {
	a := newTestApp(t)
	execute := func() bool { t.Error("ran a tool after cancellation"); return true }
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := onMCPThread(ctx, a, execute); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	a.stopTicking()
	if _, err := onMCPThread(t.Context(), a, execute); !errors.Is(err, errMCPClosing) {
		t.Fatal(err)
	}
}

func TestMCPSuccessResultsMatchSchemas(t *testing.T) {
	catalog := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var output bytes.Buffer
	if err := mcpcontrol.Bridge(catalog, &output, "test"); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var listed struct {
		Result struct {
			Tools []struct {
				Name         string
				OutputSchema mcpTestSchema
			}
		}
	}
	if err := decoder.Decode(new(json.RawMessage)); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&listed); err != nil {
		t.Fatal(err)
	}
	resetFakes()
	tests := map[string]string{
		"now_playing": `{}`, "queue_list": `{"offset":1,"limit":2}`, "search": `{"query":"song","kind":"all"}`,
		"playback": `{"action":"toggle"}`, "play_now": `{"video_id":"9cHbvRUALrc"}`, "queue_add": `{"position":"end","video_ids":["9cHbvRUALrc"]}`,
		"queue_move": `{"index":0,"to":1}`, "queue_remove": `{"index":0}`, "queue_clear": `{"confirm":true}`,
		"seek": `{"seconds":1}`, "set_repeat": `{"mode":1}`, "set_shuffle": `{"enabled":true}`, "set_volume": `{"volume":0}`,
		"like": `{"rating":"like"}`, "start_radio": `{}`,
	}
	if len(tests) != len(listed.Result.Tools) {
		t.Fatal("tool catalog and result cases differ")
	}
	for _, tool := range listed.Result.Tools {
		t.Run(tool.Name, func(t *testing.T) {
			a, _ := playbackApp(t)
			signed := signedInApp(t)
			a.account.session = signed.account.session
			a.settings.MCPControl = true
			queue := tracks("aaaaaaaaaaa", "bbbbbbbbbbb", "ccccccccccc")
			if tool.Name == "queue_add" {
				if result, err := a.mcpCall(t.Context(), tool.Name, json.RawMessage(tests[tool.Name])); err != nil ||
					result.(mcpState).Track.VideoID != "9cHbvRUALrc" {
					t.Fatalf("empty-queue add: %v %v", result, err)
				}
			} else {
				a.playFrom(queue[1], queue, 1, youtube.UpNextOptions{}, "test")
			}
			value, err := a.mcpCall(t.Context(), tool.Name, json.RawMessage(tests[tool.Name]))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var actual any
			if err := json.Unmarshal(raw, &actual); err != nil {
				t.Fatal(err)
			}
			tool.OutputSchema.check(t, tool.Name, actual)
		})
	}
}

type mcpTestSchema struct {
	Type       string
	Required   []string
	Properties map[string]mcpTestSchema
	Items      *mcpTestSchema
}

func (schema mcpTestSchema) check(t *testing.T, path string, value any) {
	t.Helper()
	switch data := value.(type) {
	case map[string]any:
		if schema.Type == "object" {
			for _, key := range schema.Required {
				if _, ok := data[key]; !ok {
					t.Fatalf("%s missing %s", path, key)
				}
			}
			for key, field := range data {
				child, ok := schema.Properties[key]
				if !ok {
					t.Fatalf("%s unexpected field %s", path, key)
				}
				child.check(t, path+"."+key, field)
			}
			return
		}
	case []any:
		if schema.Type == "array" && schema.Items != nil {
			for _, field := range data {
				schema.Items.check(t, path+"[]", field)
			}
			return
		}
	case string:
		if schema.Type == "string" {
			return
		}
	case bool:
		if schema.Type == "boolean" {
			return
		}
	case float64:
		if schema.Type == "number" || schema.Type == "integer" && data == math.Trunc(data) {
			return
		}
	}
	t.Fatalf("%s: expected %s, got %#v", path, schema.Type, value)
}

type mcpTestTransport func(*http.Request) (*http.Response, error)

func (transport mcpTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestMCPSearchFiltersAndAccess(t *testing.T) {
	for _, test := range []struct {
		kind, params, id, renderer string
	}{
		{"videos", youtube.SearchVideosParams, "", ""},
		{"all", "", "", ""},
		{"albums", youtube.SearchAlbumsParams, "MPREb_example", "ALBUM"},
		{"playlists", youtube.SearchPlaylistsParams, "VLPL_example", "PLAYLIST"},
		{"artists", youtube.SearchArtistsParams, "UCexample", "ARTIST"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			a := newTestApp(t)
			a.settings.MCPControl = true
			a.account.public = youtube.NewClient(youtube.Options{APIKey: "test", HTTPClient: &http.Client{Transport: mcpTestTransport(func(r *http.Request) (*http.Response, error) {
				var payload struct{ Params string }
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Params != test.params {
					t.Fatalf("search params: %+v %v", payload, err)
				}
				body := searchFilteredResponse
				if test.id != "" {
					body = fmt.Sprintf(`{"contents":{"musicShelfRenderer":{"contents":[{"musicResponsiveListItemRenderer":{"navigationEndpoint":{"browseEndpoint":{"browseId":%q,"browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":%q}}}},"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Result"}]}}},{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Description"}]}}}]}}]}}}`, test.id, "MUSIC_PAGE_TYPE_"+test.renderer)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}})
			result, err := a.mcpCall(t.Context(), "search", fmt.Appendf(nil, `{"kind":%q,"query":"test"}`, test.kind))
			if err != nil {
				t.Fatal(err)
			}
			items := result.(mcpItems).Items
			if len(items) == 0 || items[0].CollectionID != test.id {
				t.Fatalf("search results: %+v", items)
			}
			if test.id != "" {
				if items[0].VideoID != "" || items[0].Subtitle == "" {
					t.Fatalf("collection result: %+v", items[0])
				}
				if _, ok := a.mcpSearchCache.Get(test.id); !ok {
					t.Fatal("collection missing from search cache")
				}
			}
		})
	}
	for _, off := range []bool{false, true} {
		a := newTestApp(t)
		a.settings.MCPControl = true
		a.account.public = youtube.NewClient(youtube.Options{APIKey: "test", HTTPClient: &http.Client{Transport: mcpTestTransport(func(*http.Request) (*http.Response, error) {
			if off {
				a.settings.MCPControl = false
			}
			return nil, errors.New("test network failure")
		})}})
		_, err := a.mcpCall(t.Context(), "search", json.RawMessage(`{"query":"test"}`))
		if err == nil || (off && !errors.Is(err, errMCPDisabled)) || (!off && !strings.Contains(err.Error(), "search failed")) {
			t.Fatal(err)
		}
	}
}

func TestMCPPlaybackActions(t *testing.T) {
	for _, test := range []struct {
		action   string
		position time.Duration
		index    int
	}{
		{"play", 0, 1},
		{"toggle", 0, 1},
		{"next", 0, 2},
		{"previous", 2 * time.Second, 0},
		{"previous", 4 * time.Second, 1},
	} {
		t.Run(fmt.Sprintf("%s/%s", test.action, test.position), func(t *testing.T) {
			a, _ := playbackApp(t)
			a.settings.MCPControl = true
			queue := tracks("earlier", "current", "next")
			a.playFrom(queue[1], queue, 1, youtube.UpNextOptions{}, "test")
			a.player.Pause()
			a.player.Seek(test.position)
			value, err := a.mcpCall(t.Context(), "playback", fmt.Appendf(nil, `{"action":%q}`, test.action))
			if err != nil {
				t.Fatal(err)
			}
			state := value.(mcpState)
			if state.Index != test.index {
				t.Fatalf("queue index: %+v", state)
			}
			if test.action == "previous" && test.index == 1 && state.Position > 1 {
				t.Fatalf("did not restart: %+v", state)
			}
		})
	}
}

func TestMCPExcludedQueueAddKeepsBudget(t *testing.T) {
	a, _ := playbackApp(t)
	a.settings.MCPControl = true
	a.settings.ExcludeAIGenerated = true
	a.cacheMCPSearchResults([]youtube.MusicItem{{VideoID: "9cHbvRUALrc", AIDisclosure: youtube.AIAlteredOrGenerated}})
	raw := json.RawMessage(`{"position":"end","video_ids":["9cHbvRUALrc"]}`)
	if _, err := a.mcpCall(t.Context(), "queue_add", raw); err == nil || !strings.Contains(err.Error(), "excluded") {
		t.Fatal(err)
	}
	if wait := a.mcpAdd.AllowAfter(time.Now(), 30, 0, 30); wait != 0 {
		t.Fatalf("rejected track charged the budget: %v", wait)
	}
}

func TestMCPSnapshotSettlesOrReportsLoading(t *testing.T) {
	a := newTestApp(t)
	snapshot, err := a.waitForMCPSnapshot(t.Context())
	if err != nil || snapshot.Status != "stopped" {
		t.Fatalf("settled snapshot: %+v %v", snapshot, err)
	}
	_, cancel := context.WithCancel(t.Context())
	defer cancel()
	a.stream.cancel = cancel
	snapshot, err = a.waitForMCPSnapshot(t.Context())
	if err != nil || snapshot.Status != "loading" {
		t.Fatalf("waiting snapshot: %+v %v", snapshot, err)
	}
}
