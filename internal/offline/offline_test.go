package offline

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/elianiva/meiro/youtube"
)

func song(id string, single bool) Song {
	return Song{Item: youtube.MusicItem{VideoID: id, Title: id}, File: id + ".webm", Single: single}
}

func playlist(id string, tracks ...string) Collection {
	return Collection{Item: youtube.MusicItem{BrowseID: id, Kind: youtube.KindPlaylist}, Tracks: tracks}
}

// Removing a playlist removes the songs downloaded only for it, and keeps the
// ones downloaded by themselves or held by another downloaded playlist.
func TestRemovingACollectionKeepsSongsWantedElsewhere(t *testing.T) {
	var lib Library
	for _, s := range []Song{song("only", false), song("single", true), song("shared", false), song("other", false)} {
		lib.Add(s)
	}
	lib.AddCollection(playlist("VLPLa", "only", "single", "shared"))
	lib.AddCollection(playlist("VLPLb", "shared", "other"))

	files := lib.RemoveCollection("VLPLa")
	if !slices.Equal(files, []string{"only.webm"}) {
		t.Errorf("removed files = %q, want only the song downloaded for the playlist alone", files)
	}
	for _, id := range []string{"single", "shared", "other"} {
		if _, ok := lib.Song(id); !ok {
			t.Errorf("song %q should stay", id)
		}
	}
	if _, ok := lib.Collection("VLPLa"); ok {
		t.Error("the playlist should be gone")
	}
}

// The library survives a restart, and files a stopped download left behind,
// which it does not list, are deleted while everything else in the folder
// stays.
func TestReadKeepsListedSongsAndSweepsUnlistedAudio(t *testing.T) {
	dir := t.TempDir()
	listed := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.webm"
	unlisted := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.m4a"
	for _, name := range []string{listed, unlisted, "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var lib Library
	lib.Add(Song{Item: youtube.MusicItem{VideoID: "v1", Title: "One"}, File: listed})
	lib.AddCollection(playlist("VLPLa", "v1"))
	if err := Write(dir, lib); err != nil {
		t.Fatal(err)
	}

	read := Read(dir)
	if s, ok := read.Song("v1"); !ok || s.File != listed || s.Item.Title != "One" {
		t.Fatalf("song after reading = %+v, %v", s, ok)
	}
	if c, ok := read.Collection("VLPLa"); !ok || !slices.Equal(c.Tracks, []string{"v1"}) {
		t.Fatalf("playlist after reading = %+v, %v", c, ok)
	}
	for name, want := range map[string]bool{listed: true, unlisted: false, "notes.txt": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", name, err == nil, want)
		}
	}
}
