package main

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
)

// Build metadata. These are overridden at build time via -ldflags
// "-X main.version=… -X main.commit=… -X main.date=…" (see the Makefile's
// `server`/`dist` targets and .github/workflows/release.yml). When built with a
// plain `go build`/`go install` and no ldflags, they fall back to the version
// control information Go embeds in the binary (module version, VCS revision and
// time), so `go install github.com/pblumer/atlas/cmd/atlas@v0.1.0` still reports
// a meaningful version.
var (
	version = ""
	commit  = ""
	date    = ""
)

// buildInfo resolves the effective version, commit, and build date, filling any
// value the linker did not set from the embedded build information. It is pure
// (its only input is the provided BuildInfo) so it can be tested without
// depending on how this particular binary was built.
func buildInfo(bi *debug.BuildInfo, ok bool) (v, c, d string) {
	v, c, d = version, commit, date

	if ok && bi != nil {
		if v == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if c == "" {
					c = s.Value
				}
			case "vcs.time":
				if d == "" {
					d = s.Value
				}
			}
		}
	}

	if v == "" {
		v = "dev"
	}
	if c == "" {
		c = "unknown"
	}
	if d == "" {
		d = "unknown"
	}
	return v, c, d
}

// printVersion writes a one-line human-readable version banner.
func printVersion(w io.Writer) {
	v, c, d := buildInfo(debug.ReadBuildInfo())
	fmt.Fprintf(w, "atlas %s (commit %s, built %s, %s/%s, %s)\n",
		v, c, d, runtime.GOOS, runtime.GOARCH, runtime.Version())
}
