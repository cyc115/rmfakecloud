package config

import (
	"testing"
	"time"
)

func TestParsePageWatchMinAge(t *testing.T) {
	for v, want := range map[string]time.Duration{
		"":    DefaultPageWatchMinAge,
		"0s":  0,
		"0":   0,
		"1ms": time.Millisecond,
		"15m": 15 * time.Minute,
	} {
		got, err := parsePageWatchMinAge(v)
		if err != nil || got != want {
			t.Fatalf("%q = %v, %v; want %v", v, got, err, want)
		}
	}
	for _, v := range []string{"-1s", "ten", "10"} {
		if _, err := parsePageWatchMinAge(v); err == nil {
			t.Fatalf("%q accepted", v)
		}
	}
}
