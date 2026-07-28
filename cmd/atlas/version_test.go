package main

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"
)

// withBuildVars sets the linker-injected build vars for the duration of a test
// and restores them afterwards, so cases don't leak into one another.
func withBuildVars(t *testing.T, v, c, d string) {
	t.Helper()
	ov, oc, od := version, commit, date
	version, commit, date = v, c, d
	t.Cleanup(func() { version, commit, date = ov, oc, od })
}

func TestBuildInfo(t *testing.T) {
	tests := []struct {
		name             string
		v, c, d          string // linker-injected values
		bi               *debug.BuildInfo
		ok               bool
		wantV, wantC, wD string
	}{
		{
			name: "ldflags win over build info",
			v:    "v0.1.0", c: "abc123", d: "2026-07-28",
			bi: &debug.BuildInfo{
				Main:     debug.Module{Version: "v9.9.9"},
				Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "zzz"}},
			},
			ok:    true,
			wantV: "v0.1.0", wantC: "abc123", wD: "2026-07-28",
		},
		{
			name: "fall back to embedded build info",
			bi: &debug.BuildInfo{
				Main: debug.Module{Version: "v0.2.0"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "deadbeef"},
					{Key: "vcs.time", Value: "2026-01-01T00:00:00Z"},
				},
			},
			ok:    true,
			wantV: "v0.2.0", wantC: "deadbeef", wD: "2026-01-01T00:00:00Z",
		},
		{
			name: "devel module version is ignored",
			bi: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
			},
			ok:    true,
			wantV: "dev", wantC: "unknown", wD: "unknown",
		},
		{
			name:  "no build info yields sentinels",
			ok:    false,
			wantV: "dev", wantC: "unknown", wD: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withBuildVars(t, tt.v, tt.c, tt.d)
			gotV, gotC, gotD := buildInfo(tt.bi, tt.ok)
			if gotV != tt.wantV || gotC != tt.wantC || gotD != tt.wD {
				t.Fatalf("buildInfo = (%q, %q, %q), want (%q, %q, %q)",
					gotV, gotC, gotD, tt.wantV, tt.wantC, tt.wD)
			}
		})
	}
}

func TestPrintVersion(t *testing.T) {
	withBuildVars(t, "v0.1.0", "abc123", "2026-07-28")
	var buf bytes.Buffer
	printVersion(&buf)
	out := buf.String()
	if !strings.HasPrefix(out, "atlas v0.1.0 ") {
		t.Fatalf("banner %q does not start with the injected version", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("banner %q is not newline-terminated", out)
	}
}
