package main

import (
	"testing"
	"unicode/utf8"
)

func TestTruncateText_ShortStringUnchanged(t *testing.T) {
	if got := truncateText("short", 10); got != "short" {
		t.Errorf("got %q, want %q", got, "short")
	}
}

func TestTruncateText_ExactLengthUnchanged(t *testing.T) {
	if got := truncateText("1234567890", 10); got != "1234567890" {
		t.Errorf("got %q, want %q", got, "1234567890")
	}
}

func TestTruncateText_LongStringCutWithEllipsis(t *testing.T) {
	got := truncateText("this is a very long string that needs cutting", 10)
	want := "this is a…"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if n := utf8.RuneCountInString(got); n > 10 {
		t.Errorf("truncated string exceeds max rune length: %d > 10", n)
	}
}
