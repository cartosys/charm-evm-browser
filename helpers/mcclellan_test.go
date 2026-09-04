package helpers

import (
	"math"
	"testing"
)

func TestNetAdvances(t *testing.T) {
	days := []string{"d0", "d1", "d2", "d3"}
	closes := map[string][]float64{
		"A": {10, 11, 11, 9},  // up, flat, down
		"B": {5, 4, 6, 6},     // down, up, flat
		"C": {1, 2, 3, 4},     // up, up, up
	}

	got := NetAdvances(days, closes)
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	if got[0] != 1 {
		t.Errorf("day1 net advances = %v, want 1", got[0])
	}
	if got[1] != 2 {
		t.Errorf("day2 net advances = %v, want 2", got[1])
	}
	if got[2] != 0 {
		t.Errorf("day3 net advances = %v, want 0", got[2])
	}
}

func TestNetAdvancesSkipsNaN(t *testing.T) {
	days := []string{"d0", "d1"}
	closes := map[string][]float64{
		"A": {10, 11},
		"B": {math.NaN(), 5},
	}
	got := NetAdvances(days, closes)
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("got %v, want [1] (B's NaN gap should not count)", got)
	}
}

func TestEMASeedAndRecursion(t *testing.T) {
	series := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	got := EMA(series, 3)
	if len(got) != len(series) {
		t.Fatalf("expected %d entries, got %d", len(series), len(got))
	}
	for i := 0; i < 2; i++ {
		if !math.IsNaN(got[i]) {
			t.Errorf("index %d = %v, want NaN", i, got[i])
		}
	}
	// Seed at index 2 is the simple average of the first 3 values: (1+2+3)/3 = 2.
	// For this linear series (slope 1, alpha 2/(3+1)=0.5) the EMA settles onto
	// series[i]-1 immediately since a linear input has a fixed-point offset.
	for i := 2; i < len(series); i++ {
		want := series[i] - 1
		if math.Abs(got[i]-want) > 1e-9 {
			t.Errorf("index %d = %v, want %v", i, got[i], want)
		}
	}
}

func TestEMAInsufficientData(t *testing.T) {
	got := EMA([]float64{1, 2}, 5)
	for i, v := range got {
		if !math.IsNaN(v) {
			t.Errorf("index %d = %v, want NaN (insufficient data)", i, v)
		}
	}
}

func TestOscillatorConstantSeries(t *testing.T) {
	series := make([]float64, 40)
	for i := range series {
		series[i] = 5
	}
	got := Oscillator(series)
	if len(got) != 40 {
		t.Fatalf("expected 40 entries, got %d", len(got))
	}
	for i := 0; i < 38; i++ {
		if !math.IsNaN(got[i]) {
			t.Errorf("index %d = %v, want NaN (EMA39 not seeded yet)", i, got[i])
		}
	}
	for i := 38; i < 40; i++ {
		if math.Abs(got[i]) > 1e-9 {
			t.Errorf("index %d = %v, want 0 (constant series -> EMA19 == EMA39)", i, got[i])
		}
	}
}
