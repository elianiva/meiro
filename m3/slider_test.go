package m3

import (
	"testing"
)

// The active track of a wavy slider is a wave that travels from the handle
// back to the start while the music plays. The travel is a pure function of
// the wall-clock time, so it can be asserted without rendering or sleeping.
func TestSliderWaveTravelsBackToTheStart(t *testing.T) {
	if got := wavePhase(0); got != 0 {
		t.Errorf("wave phase at the start of a period = %v, want 0", got)
	}

	// However far into the period, the wave has moved toward the start since
	// the moment before. It only ever travels one way.
	previous := float32(0)
	for millis := int64(1); millis < wavePeriodMS; millis++ {
		phase := wavePhase(millis)
		if phase > previous {
			t.Fatalf("the wave moved back toward the handle at %dms: %v after %v", millis, phase, previous)
		}
		previous = phase
	}
	if previous > -float32(waveWavelength)*0.99 {
		t.Errorf("the wave travelled %v over a period, want about %v", -previous, waveWavelength)
	}

	// The wave repeats exactly once per period.
	if wavePhase(wavePeriodMS) != wavePhase(0) {
		t.Errorf("phase %v after one period, want %v", wavePhase(wavePeriodMS), wavePhase(0))
	}
	if wavePhase(wavePeriodMS+450) != wavePhase(450) {
		t.Errorf("phase %v 450ms into the next period, want %v", wavePhase(wavePeriodMS+450), wavePhase(450))
	}
}
