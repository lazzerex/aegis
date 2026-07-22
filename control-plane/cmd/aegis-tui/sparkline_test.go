package main

import (
	"strings"
	"testing"
)

func TestSparkline_Empty(t *testing.T) {
	if got := sparkline(nil); got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestSparkline_SingleSampleIsFlat(t *testing.T) {
	got := sparkline([]float64{5})
	if got != string(sparkBlocks[0]) {
		t.Errorf("got %q, want a single flat block", got)
	}
}

func TestSparkline_AllZeroIsFlat(t *testing.T) {
	got := sparkline([]float64{0, 0, 0, 0})
	want := strings.Repeat(string(sparkBlocks[0]), 4)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSparkline_LowestAndHighestMapToEndBlocks(t *testing.T) {
	got := []rune(sparkline([]float64{0, 10}))
	if len(got) != 2 {
		t.Fatalf("expected 2 runes, got %d", len(got))
	}
	if got[0] != sparkBlocks[0] {
		t.Errorf("lowest sample: got %q, want %q", got[0], sparkBlocks[0])
	}
	if got[1] != sparkBlocks[len(sparkBlocks)-1] {
		t.Errorf("highest sample: got %q, want %q", got[1], sparkBlocks[len(sparkBlocks)-1])
	}
}

func TestSparkline_CapsAtTwentyMostRecentSamples(t *testing.T) {
	samples := make([]float64, 30)
	for i := range samples {
		samples[i] = float64(i)
	}
	got := []rune(sparkline(samples))
	if len(got) != 20 {
		t.Fatalf("expected 20 runes, got %d", len(got))
	}
	if got[0] != sparkBlocks[0] {
		t.Errorf("first shown sample should be the min of the last 20 (value 10), got %q", got[0])
	}
	if got[19] != sparkBlocks[len(sparkBlocks)-1] {
		t.Errorf("last shown sample should be the max, got %q", got[19])
	}
}
