package main

import (
	"strings"
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

// TestEnvSwitch is the one env helper that refuses rather than falls back. It backs
// switches that turn a part of the server off (ATLAS_CATALOGUE), and there the
// fallback the other helpers take is the wrong way round: a typo in "ATLAS_CATALOGUE=of"
// would leave the shop served to everybody by an operator who believes it is off.
func TestEnvSwitch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		set     bool
		val     string
		def     bool
		want    bool
		refused bool
	}{
		{"unset keeps on", false, "", true, true, false},
		{"unset keeps off", false, "", false, false, false},
		{"empty", true, "", true, true, false},
		{"blank", true, "   ", true, true, false},
		{"false", true, "false", true, false, false},
		{"zero", true, "0", true, false, false},
		{"upper", true, " FALSE ", true, false, false},
		{"true", true, "true", false, true, false},
		{"one", true, "1", false, true, false},
		{"typo", true, "of", true, false, true},
		{"word", true, "off", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("ATLAS_TEST_SWITCH", tc.val)
			}
			got, err := envSwitch("ATLAS_TEST_SWITCH", tc.def)
			if tc.refused {
				if err == nil || !strings.Contains(err.Error(), "ATLAS_TEST_SWITCH") {
					t.Errorf("envSwitch(%q) = %v, %v; want an error naming the variable", tc.val, got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("envSwitch(%q) = %v, %v; want %v, nil", tc.val, got, err, tc.want)
			}
		})
	}
}

// TestNumberedPath covers how an export of several workflows lands beside the
// file --out names: the first keeps that name, the rest are numbered.
func TestNumberedPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"onboarding.bpmn", "onboarding-2.bpmn"},
		{"/tmp/a/b.xml", "/tmp/a/b-2.xml"},
		{"noext", "noext-2"},
	} {
		if got := numberedPath(tc.in, 2); got != tc.want {
			t.Errorf("numberedPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
