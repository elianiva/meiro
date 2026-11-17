package main

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/settings"
)

func TestTheSoundCardSwitchAndPreset(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 4400)
	tt.Frame()

	if err := tt.Click("Equaliser on"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.settings.EQOn {
		t.Fatal("the equaliser switch did not turn the equaliser on")
	}

	var rock []float64
	for _, preset := range eqPresets {
		if preset.name == "Rock" {
			rock = preset.bands
		}
	}
	if err := tt.Click("Rock"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !slices.Equal(a.settings.EQBands, rock) {
		t.Errorf("bands = %v, want the Rock preset %v", a.settings.EQBands, rock)
	}
	if !slices.Equal(a.eqApplied[:], rock) {
		t.Errorf("eqApplied = %v, want the Rock preset %v", a.eqApplied, rock)
	}

	if err := tt.Click("Equaliser on"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.EQOn {
		t.Error("the equaliser switch did not turn the equaliser off")
	}
	if got := a.eqBands(); got != nil {
		t.Errorf("eqBands with the equaliser off = %v, want nil", got)
	}
}

func TestCrossfadeSwitchAndSliderReachThePlayerAndSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := newTestApp(t)
	a.saver.Path = path
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 4400)
	tt.Frame()
	if got := a.player.Crossfade(); got != 0 {
		t.Fatalf("crossfade starts at %v, want off", got)
	}

	if err := tt.Click("Crossfade on"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if want := settings.DefaultCrossfade * time.Second; a.player.Crossfade() != want {
		t.Errorf("switching it on gave the player %v, want the default %v", a.player.Crossfade(), want)
	}

	if err := tt.Click("Crossfade duration"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	seconds := a.settings.CrossfadeSeconds
	if seconds <= settings.DefaultCrossfade || seconds >= settings.MaxCrossfade || a.player.Crossfade() != time.Duration(seconds)*time.Second {
		t.Errorf("the slider set %d s, and the player holds %v", seconds, a.player.Crossfade())
	}

	a.saver.Save(&a.settings)
	a.saver.Wait()
	restarted := newTestApp(t)
	restarted.settings = settings.Load(path)
	restarted.applyCrossfade()
	if restarted.player.Crossfade() != time.Duration(seconds)*time.Second {
		t.Errorf("after a restart the player holds %v, want %d s", restarted.player.Crossfade(), seconds)
	}

	if err := tt.Click("Crossfade on"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.player.Crossfade() != 0 || a.settings.CrossfadeSeconds != seconds {
		t.Errorf("switching it off left the player at %v and the length at %d s; want 0 and %d s kept", a.player.Crossfade(), a.settings.CrossfadeSeconds, seconds)
	}
}
