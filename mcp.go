package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/elianiva/meiro/internal/lru"
	"github.com/elianiva/meiro/internal/mcpcontrol"
	"github.com/elianiva/meiro/youtube"
)

func (a *app) syncMCP() {
	a.mcpError = ""
	if !a.settings.MCPControl {
		a.mcpAdvanced = false
		a.closeMCP()
		return
	}
	if a.mcp != nil && a.mcp.Failure() == "" {
		return
	}
	a.closeMCP()
	server, err := mcpcontrol.Start(a.mcpCall, func() {
		if !a.mcpStopping() {
			a.refresh()
		}
	})
	if err != nil {
		log.Printf("start local MCP listener: %v", err)
		a.mcpError = firstLine(err.Error())
		return
	}
	a.mcp = server
}

func (a *app) closeMCP() {
	if a.mcp != nil {
		server := a.mcp
		a.mcp = nil
		server.Close()
		a.run(server.Wait)
	}
}

type mcpTrack struct {
	VideoID      string `json:"video_id"`
	CollectionID string `json:"collection_id"`
	Subtitle     string `json:"subtitle"`
	Title        string `json:"title"`
	Artists      string `json:"artists"`
	Album        string `json:"album"`
	Duration     string `json:"duration"`
	Kind         string `json:"kind"`
	Rating       string `json:"rating"`
}

type mcpState struct {
	Track         mcpTrack   `json:"track"`
	Status        string     `json:"status"`
	Position      float64    `json:"position_seconds"`
	Index         int        `json:"current_index"`
	Total         int        `json:"queue_total"`
	Shuffle       bool       `json:"shuffle"`
	Repeat        repeatMode `json:"repeat"`
	Volume        float64    `json:"volume"`
	Seeking       bool       `json:"seeking"`
	PlaybackError string     `json:"playback_error,omitempty"`
}

type mcpItems struct {
	Items []mcpTrack `json:"items"`
}

type mcpPage struct {
	mcpItems
	Total                int `json:"total"`
	Offset               int `json:"offset"`
	Index                int `json:"current_index"`
	RecommendationsStart int `json:"recommendations_start"`
}

func clipMCPText(text string) string {
	for index, offset := 0, 0; offset < len(text); index++ {
		if index == 200 {
			return text[:offset]
		}
		_, size := utf8.DecodeRuneInString(text[offset:])
		offset += size
	}
	return text
}

func mcpTrackFromItem(item youtube.MusicItem, signedIn bool) mcpTrack {
	rating := "unknown"
	if signedIn && item.VideoID != "" {
		switch item.Like {
		case youtube.Liked:
			rating = "like"
		case youtube.Disliked:
			rating = "dislike"
		case youtube.Indifferent:
			rating = "none"
		case youtube.LikeUnknown:
		}
	}
	row := mcpTrack{
		Rating:  rating,
		VideoID: clipMCPText(item.VideoID), Title: clipMCPText(item.Title), Artists: clipMCPText(shownArtist(item)),
		Subtitle: clipMCPText(item.Subtitle), Album: clipMCPText(item.Album), Duration: clipMCPText(item.Duration), Kind: clipMCPText(string(item.Kind)),
	}
	if id := mcpCollectionID(item); id != "" {
		row.VideoID, row.CollectionID = "", id
	}
	return row
}

func mcpCollectionID(item youtube.MusicItem) string {
	target := item.Target()
	switch target.Kind {
	case youtube.TargetAlbum, youtube.TargetPlaylist, youtube.TargetArtist:
		return target.ID
	case youtube.TargetNone, youtube.TargetTrack, youtube.TargetPodcast:
		return ""
	}
	return ""
}

func (a *app) cacheMCPSearchResults(items []youtube.MusicItem) {
	if a.mcpSearchCache == nil {
		a.mcpSearchCache = lru.New[string, youtube.MusicItem](200)
	}
	for _, item := range items {
		id := mcpCollectionID(item)
		if id == "" {
			id = item.VideoID
		}
		a.mcpSearchCache.Put(id, item)
	}
}

func (a *app) mcpTrackByID(id string) youtube.MusicItem {
	if a.mcpSearchCache != nil {
		if item, ok := a.mcpSearchCache.Get(id); ok {
			return item
		}
	}
	if a.current.VideoID == id && a.current.Title != id {
		return a.current
	}
	for index := range a.queue.len() {
		if item := a.queue.at(index); item.VideoID == id && item.Title != id {
			return item
		}
	}
	return youtube.MusicItem{VideoID: id, Title: id}
}

type mcpReply struct {
	value   any
	err     error
	search  *mcpSearchRequest
	wait    bool
	pending <-chan error
}

type mcpSearchRequest struct {
	client   *youtube.Client
	query    string
	params   string
	signedIn bool
}

var (
	mcpCollection  = regexp.MustCompile(`^(MPR|VL|PL|UC)[A-Za-z0-9_-]{1,197}$`)
	mcpID          = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	errMCPDisabled = errors.New("enable Settings > Desktop > AI control to use Meiro tools")
	errMCPClosing  = errors.New("open Meiro before retrying; the app is closing")
)

func (a *app) mcpStopping() bool {
	select {
	case <-a.stop:
		return true
	default:
		return false
	}
}

func onMCPThread[T any](ctx context.Context, a *app, execute func() T) (T, error) {
	var zero T
	if a.mcpStopping() {
		return zero, errMCPClosing
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	uiCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	reply := make(chan T, 1)
	a.update(func() {
		if !a.mcpStopping() && uiCtx.Err() == nil {
			reply <- execute()
		}
	})
	select {
	case value := <-reply:
		return value, nil
	case <-uiCtx.Done():
		return zero, uiCtx.Err()
	case <-a.stop:
		return zero, errMCPClosing
	}
}

func (a *app) mcpCall(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	response, err := onMCPThread(ctx, a, func() mcpReply {
		if !a.settings.MCPControl {
			return mcpReply{err: errMCPDisabled}
		}
		reply := a.executeMCP(func() bool { return ctx.Err() == nil && !a.mcpStopping() }, name, raw) //nolint:contextcheck // Playback and account writes follow the app lifecycle.
		if reply.search == nil && reply.pending == nil {
			a.recordMCPAction(name, reply.err)
		}
		return reply
	})
	if err != nil {
		return nil, err
	}
	if response.err != nil {
		return nil, response.err
	}
	if response.search != nil {
		return a.runMCPSearch(ctx, response.search)
	}
	if response.pending != nil {
		select {
		case err := <-response.pending:
			if err != nil {
				return nil, err
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-a.stop:
			return nil, errMCPClosing
		}
	}
	if response.wait {
		return a.waitForMCPSnapshot(ctx)
	}
	return response.value, nil
}

func (a *app) recordMCPAction(name string, err error) {
	if !mcpcontrol.KnownTool(name) {
		return
	}
	a.mcpLastTool, a.mcpLastAt, a.mcpLastFailed = name, time.Now(), err != nil
}

func (a *app) executeMCP(active func() bool, name string, raw json.RawMessage) mcpReply {
	decode := func(args any) error { return mcpcontrol.DecodeArgs(name, raw, args) }
	switch name {
	case "now_playing":
		if err := decode(&struct{}{}); err != nil {
			return mcpReply{err: err}
		}
		return mcpReply{value: a.mcpSnapshot()}
	case "queue_list":
		var args struct {
			Offset int `json:"offset"`
			Limit  int `json:"limit"`
		}
		args.Limit = 20
		if err := decode(&args); err != nil {
			return mcpReply{err: err}
		}
		if args.Offset < 0 || args.Limit < 1 || args.Limit > 50 {
			return mcpReply{err: errors.New("use an offset of at least 0 and a limit from 1 to 50")}
		}
		first := min(args.Offset, a.queue.len())
		rows := make([]mcpTrack, 0, min(args.Limit, a.queue.len()-first))
		for index := first; index < min(a.queue.len(), first+args.Limit); index++ {
			rows = append(rows, mcpTrackFromItem(a.queue.at(index), a.signedIn()))
		}
		return mcpReply{value: mcpPage{mcpItems{rows}, a.queue.len(), args.Offset, a.mcpQueueIndex(), a.queue.recStart}}
	case "search":
		var args struct {
			Query string `json:"query"`
			Kind  string `json:"kind"`
		}
		args.Kind = "songs"
		if err := decode(&args); err != nil {
			return mcpReply{err: err}
		}
		return a.prepareMCPSearch(args.Query, args.Kind)
	case "like":
		return a.mcpLike(decode)
	case "play_now":
		var args struct {
			VideoID      string `json:"video_id"`
			CollectionID string `json:"collection_id"`
			Shuffle      *bool  `json:"shuffle"`
		}
		if err := decode(&args); err != nil {
			return mcpReply{err: err}
		}
		if (args.VideoID == "") == (args.CollectionID == "") {
			return mcpReply{err: errors.New("provide exactly one of video_id or collection_id")}
		}
		if args.CollectionID != "" {
			return a.mcpPlayCollection(active, args.CollectionID, args.Shuffle != nil && *args.Shuffle)
		}
		if args.Shuffle != nil {
			return mcpReply{err: errors.New("use shuffle only with collection_id")}
		}
		return a.mcpPlaybackReply(name, func() error { return a.mcpPlayNow(args.VideoID) })
	}
	return a.mcpPlaybackReply(name, func() error { return a.mutateMCP(name, decode) })
}

func (a *app) mcpPlaybackReply(name string, apply func() error) mcpReply {
	stream, ai, seek := a.stream.gen, a.aiCheck.gen, a.seeking.gen
	if err := apply(); err != nil {
		return mcpReply{err: err}
	}
	state := a.mcpSnapshot()
	wait := (state.Status == "loading" && (stream != a.stream.gen || ai != a.aiCheck.gen)) ||
		(state.Seeking && (seek != a.seeking.gen || name == "seek"))
	return mcpReply{value: state, wait: wait}
}

func (a *app) prepareMCPSearch(query, kind string) mcpReply {
	var params string
	switch kind {
	case "songs":
		params = youtube.SearchSongsParams
	case "videos":
		params = youtube.SearchVideosParams
	case "albums":
		params = youtube.SearchAlbumsParams
	case "playlists":
		params = youtube.SearchPlaylistsParams
	case "artists":
		params = youtube.SearchArtistsParams
	case "all":
	default:
		return mcpReply{err: errors.New("use songs, videos, albums, playlists, artists or all for kind")}
	}
	if strings.TrimSpace(query) == "" || utf8.RuneCountInString(query) > 200 {
		return mcpReply{err: errors.New("use a search query of 1 to 200 characters")}
	}
	if wait := a.mcpSearch.AllowAfter(time.Now(), 10, 6*time.Second, 1); wait > 0 {
		return mcpReply{err: mcpWaitError("search", wait)}
	}
	return mcpReply{search: &mcpSearchRequest{a.client(), query, params, a.signedIn()}}
}

func (a *app) runMCPSearch(ctx context.Context, search *mcpSearchRequest) (any, error) {
	result, searchErr := search.client.Search(ctx, search.query, youtube.SearchOptions{Params: search.params})
	if searchErr != nil {
		searchErr = errors.New("search failed; check that YouTube Music works in Meiro before trying again")
	}
	rows := make([]mcpTrack, 0, 10)
	items := make([]youtube.MusicItem, 0, 10)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if result != nil {
		for _, item := range result.Items {
			if item.VideoID != "" || mcpCollectionID(item) != "" {
				rows = append(rows, mcpTrackFromItem(item, search.signedIn))
				items = append(items, item)
				if len(rows) == 10 {
					break
				}
			}
		}
	}
	access, err := onMCPThread(ctx, a, func() error {
		if !a.settings.MCPControl {
			return errMCPDisabled
		}
		a.recordMCPAction("search", searchErr)
		if searchErr == nil {
			a.cacheMCPSearchResults(items)
		}
		return nil
	})
	return mcpItems{rows}, errors.Join(err, access, searchErr)
}

func (a *app) mutateMCP(name string, decode func(any) error) error {
	switch name {
	case "playback":
		var args struct {
			Action string `json:"action"`
		}
		if err := decode(&args); err != nil {
			return err
		}
		return a.mcpPlaybackAction(args.Action)
	case "queue_add":
		var args struct {
			IDs      []string `json:"video_ids"`
			Position string   `json:"position"`
		}
		if err := decode(&args); err != nil {
			return err
		}
		return a.mcpQueueAdd(args.IDs, args.Position)
	case "start_radio":
		var args struct {
			VideoID string `json:"video_id"`
		}
		if err := decode(&args); err != nil {
			return err
		}
		item, err := a.mcpTargetTrack(args.VideoID)
		if err != nil {
			return err
		}
		if a.excludesItem(item) {
			return errors.New("track excluded by playback settings; choose another track")
		}
		if err := a.mcpCommandLimit(time.Now(), true); err != nil {
			return err
		}
		a.startRadio(item, "mcp-radio")
		return nil
	default:
		return a.mutateMCPLocal(name, decode)
	}
}

func (a *app) mcpPlaybackAction(action string) error {
	actions := map[string]func(){"play": a.resume, "pause": a.pause, "toggle": a.togglePlay, "next": a.advance, "previous": a.previous}
	apply := actions[action]
	if apply == nil {
		return errors.New("use play, pause, toggle, next or previous")
	}
	if err := a.mcpCommandLimit(time.Now(), action == "next" || action == "previous"); err != nil {
		return err
	}
	apply()
	return nil
}

func (a *app) mcpQueueAdd(ids []string, position string) error {
	if len(ids) < 1 || len(ids) > 10 || (position != "next" && position != "end") {
		return errors.New("use 1 to 10 video IDs and position next or end")
	}
	items := make([]youtube.MusicItem, 0, len(ids))
	for _, id := range ids {
		if !mcpID.MatchString(id) {
			return errors.New("use a video ID from search or queue_list")
		}
		item := a.mcpTrackByID(id)
		if a.excludesItem(item) {
			return errors.New("a track is excluded by playback settings; choose another track")
		}
		items = append(items, item)
	}
	if wait := a.mcpAdd.AllowAfter(time.Now(), 30, 0, len(items)); wait > 0 {
		return mcpWaitError("queue addition", wait)
	}
	if a.current.VideoID == "" || a.queue.len() == 0 {
		a.enqueue(items[0], position == "next")
		items = items[1:]
	}
	if position == "next" {
		slices.Reverse(items)
	}
	for _, item := range items {
		a.enqueue(item, position == "next")
	}
	return nil
}

func (a *app) mcpPlayNow(id string) error {
	if !mcpID.MatchString(id) {
		return errors.New("use a video ID from search or queue_list")
	}
	item := a.mcpTrackByID(id)
	if a.excludesItem(item) {
		return errors.New("track excluded by playback settings; choose another track")
	}
	if err := a.mcpCommandLimit(time.Now(), true); err != nil {
		return err
	}
	index := a.enqueue(item, true)
	if index != a.queue.index {
		a.start(a.queue.moveTo(index))
	}
	return nil
}

func (a *app) mcpPlayCollection(active func() bool, id string, shuffle bool) mcpReply {
	if !mcpCollection.MatchString(id) {
		return mcpReply{err: errors.New("use a collection ID from search")}
	}
	item := youtube.MusicItem{BrowseID: id, Title: id}
	if a.mcpSearchCache != nil {
		if cached, ok := a.mcpSearchCache.Get(id); ok {
			item = cached
		}
	}
	if a.opening.busy() {
		return mcpReply{err: errors.New("wait for Meiro to finish opening the current collection")}
	}
	if wait := a.mcpCollections.AllowAfter(time.Now(), 10, 3*time.Second, 1); wait > 0 {
		return mcpReply{err: mcpWaitError("collection playback", wait)}
	}
	done := make(chan error, 1)
	a.playCollectionResult(active, item, "mcp-collection", shuffle, func(err error) {
		a.recordMCPAction("play_now", err)
		done <- err
	})
	return mcpReply{pending: done, wait: true}
}

func (a *app) mcpTargetTrack(id string) (youtube.MusicItem, error) {
	if id == "" {
		if a.current.IsTrack() {
			return a.current, nil
		}
		return youtube.MusicItem{}, errors.New("choose a track in Meiro or provide a video ID")
	}
	if !mcpID.MatchString(id) {
		return youtube.MusicItem{}, errors.New("use a video ID from search or queue_list")
	}
	if id == a.current.VideoID {
		return a.current, nil
	}
	for i := range a.queue.len() {
		if item := a.queue.at(i); item.VideoID == id {
			return item, nil
		}
	}
	return a.mcpTrackByID(id), nil
}

type mcpRating struct {
	VideoID string `json:"video_id"`
	Rating  string `json:"rating"`
}

func (a *app) mcpLike(decode func(any) error) mcpReply {
	var args mcpRating
	if err := decode(&args); err != nil {
		return mcpReply{err: err}
	}
	if !a.signedIn() {
		return mcpReply{err: errors.New("sign in to Meiro to rate tracks")}
	}
	item, err := a.mcpTargetTrack(args.VideoID)
	if err != nil {
		return mcpReply{err: err}
	}
	rating, ok := map[string]youtube.LikeStatus{"like": youtube.Liked, "dislike": youtube.Disliked, "none": youtube.Indifferent}[args.Rating]
	if !ok {
		return mcpReply{err: errors.New("use like, dislike or none for rating")}
	}
	if a.writing["like:"+item.VideoID] {
		return mcpReply{err: errors.New("wait for Meiro to finish rating this track")}
	}
	if wait := a.mcpRatings.AllowAfter(time.Now(), 10, 0, 1); wait > 0 {
		return mcpReply{err: mcpWaitError("rating", wait)}
	}
	done := make(chan error, 1)
	a.rateTrack(item, rating, func(err error) {
		if err != nil {
			err = errors.New("could not rate the track; check your YouTube Music sign-in in Meiro")
		}
		a.recordMCPAction("like", err)
		done <- err
	})
	return mcpReply{pending: done, value: mcpRating{item.VideoID, args.Rating}}
}

func (a *app) mutateMCPLocal(name string, decode func(any) error) error {
	var apply func()
	switch name {
	case "queue_remove":
		var args struct {
			Index int `json:"index"`
		}
		if err := decode(&args); err != nil {
			return err
		}
		if args.Index < 0 || args.Index >= a.queue.len() || args.Index == a.queue.index {
			return errors.New("read queue_list and choose an index other than the current track")
		}
		apply = func() { a.removeFromQueue(args.Index) }
	case "queue_move":
		var args struct {
			Index int `json:"index"`
			To    int `json:"to"`
		}
		if err := decode(&args); err != nil {
			return err
		}
		if args.Index < 0 || args.Index >= a.queue.len() || args.To < 0 || args.To >= a.queue.len() {
			return errors.New("read queue_list and use valid queue indexes")
		}
		apply = func() {
			if args.Index != args.To {
				to := args.To
				if to > args.Index {
					to++
				}
				a.moveQueue([]int{a.queue.rowOf(args.Index)}, a.queue.rowOf(to))
			}
		}
	case "queue_clear":
		var args struct {
			Confirm bool `json:"confirm"`
		}
		if err := decode(&args); err != nil {
			return err
		}
		if !args.Confirm {
			return errors.New(mcpcontrol.ConfirmClear)
		}
		apply = a.clearUpcoming
	case "seek":
		var args struct {
			Seconds float64 `json:"seconds"`
		}
		if err := decode(&args); err != nil {
			return err
		}
		if args.Seconds < 0 || args.Seconds > 86400 {
			return errors.New("use seconds from 0 to 86400")
		}
		if !a.player.Active() {
			return errors.New("start playback before seeking")
		}
		apply = func() { a.seek(time.Duration(args.Seconds * float64(time.Second))) }
	case "set_volume":
		var args struct {
			Volume float64 `json:"volume"`
		}
		if err := decode(&args); err != nil {
			return err
		}
		if args.Volume < 0 || args.Volume > 100 {
			return errors.New("use a volume from 0 to 100")
		}
		apply = func() {
			a.setVolume(args.Volume)
			a.saveSettings()
		}
	case "set_shuffle":
		var args struct {
			Enabled bool `json:"enabled"`
		}
		if err := decode(&args); err != nil {
			return err
		}
		apply = func() {
			if a.shuffle != args.Enabled {
				a.setShuffle(args.Enabled)
			}
		}
	case "set_repeat":
		var args struct {
			Mode int `json:"mode"`
		}
		if err := decode(&args); err != nil {
			return err
		}
		if args.Mode < 0 || args.Mode > 2 {
			return errors.New("use mode 0, 1 or 2")
		}
		apply = func() {
			a.repeat = repeatMode(args.Mode)
			a.syncPreload()
			a.syncSystemMedia()
		}
	default:
		return errors.New("unknown tool; list the Meiro tools and use a listed name")
	}
	if err := a.mcpCommandLimit(time.Now(), false); err != nil {
		return err
	}
	apply()
	return nil
}

func (a *app) waitForMCPSnapshot(ctx context.Context) (mcpState, error) {
	ticker := time.NewTicker(75 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return mcpState{}, ctx.Err()
		case <-deadline.C:
			return onMCPThread(ctx, a, a.mcpSnapshot)
		case <-ticker.C:
			state, err := onMCPThread(ctx, a, a.mcpSnapshot)
			if err != nil || (state.Status != "loading" && !state.Seeking) {
				return state, err
			}
		}
	}
}

func (a *app) mcpQueueIndex() int {
	if a.queue.len() == 0 {
		return -1
	}
	return a.queue.index
}

func (a *app) mcpSnapshot() mcpState {
	status := "stopped"
	switch {
	case a.loading():
		status = "loading"
	case a.player.Playing():
		status = "playing"
	case a.player.Paused():
		status = "paused"
	}
	return mcpState{
		Track: mcpTrackFromItem(a.current, a.signedIn()), Status: status, Position: a.playbackPosition().Seconds(),
		Index: a.mcpQueueIndex(), Total: a.queue.len(), Shuffle: a.shuffle, Repeat: a.repeat, Volume: a.volume, Seeking: a.seeking.busy(), PlaybackError: a.playErr,
	}
}

func (a *app) mcpCommandLimit(now time.Time, network bool) error {
	if network {
		if wait := a.mcpPlayback.AllowAfter(now, 20, 2*time.Second, 1); wait > 0 {
			return mcpWaitError("network playback", wait)
		}
	} else if wait := a.mcpLocal.AllowAfter(now, 120, 0, 1); wait > 0 {
		return mcpWaitError("local command", wait)
	}
	return nil
}

func mcpWaitError(action string, wait time.Duration) error {
	seconds := (wait + time.Second - 1) / time.Second
	if seconds == 1 {
		return fmt.Errorf("%s rate limit: wait 1 second and retry once", action)
	}
	return fmt.Errorf("%s rate limit: wait %d seconds and retry once", action, seconds)
}
