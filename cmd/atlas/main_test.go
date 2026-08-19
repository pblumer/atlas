package main

import (
	"testing"
	"time"
)

// The env helpers back the flag defaults, so every serve flag can also be set from the
// environment (the container case). A malformed or empty value must fall back to the
// stated default rather than silently becoming zero — a zero retention interval or
// batch would otherwise read as "disabled" to the reader of --help while the server
// quietly restored its own default.
func TestEnvDurationOr(t *testing.T) {
	const def = 5 * time.Minute
	for _, tc := range []struct {
		name string
		set  bool
		val  string
		want time.Duration
	}{
		{"unset", false, "", def},
		{"empty", true, "", def},
		{"blank", true, "   ", def},
		{"malformed", true, "5 minutes", def},
		{"parsed", true, "90s", 90 * time.Second},
		{"trimmed", true, " 2h ", 2 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("ATLAS_TEST_DURATION", tc.val)
			}
			if got := envDurationOr("ATLAS_TEST_DURATION", def); got != tc.want {
				t.Errorf("envDurationOr = %s, want %s", got, tc.want)
			}
		})
	}
	// envDuration is the zero-default spelling of the same helper.
	t.Setenv("ATLAS_TEST_DURATION", "3h")
	if got := envDuration("ATLAS_TEST_DURATION"); got != 3*time.Hour {
		t.Errorf("envDuration = %s, want 3h", got)
	}
	t.Setenv("ATLAS_TEST_DURATION", "nonsense")
	if got := envDuration("ATLAS_TEST_DURATION"); got != 0 {
		t.Errorf("envDuration of a malformed value = %s, want 0", got)
	}
}

func TestEnvIntOr(t *testing.T) {
	const def = 1000
	for _, tc := range []struct {
		name string
		set  bool
		val  string
		want int
	}{
		{"unset", false, "", def},
		{"empty", true, "", def},
		{"malformed", true, "many", def},
		{"parsed", true, "250", 250},
		{"trimmed", true, " 42 ", 42},
		{"negative", true, "-1", -1}, // passed through; the option layer restores its default
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("ATLAS_TEST_INT", tc.val)
			}
			if got := envIntOr("ATLAS_TEST_INT", def); got != tc.want {
				t.Errorf("envIntOr = %d, want %d", got, tc.want)
			}
		})
	}
}
