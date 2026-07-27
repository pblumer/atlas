package api

import (
	"runtime"
	"runtime/debug"
	"sync"
)

// buildMeta is the version-control metadata Go embeds in the binary at build time:
// `go build` from a git checkout records vcs.revision/time/modified automatically
// (no -ldflags needed). It lets an operator confirm, from the web UI, exactly which
// commit a running server was built from — the answer to "is the deployed binary
// the merged version, or a stale one?".
type buildMeta struct {
	Revision string `json:"revision"`  // git commit, "" if built outside a VCS checkout
	Time     string `json:"buildTime"` // commit time (RFC3339), "" if unknown
	Modified bool   `json:"modified"`  // true if built from a dirty working tree
	Go       string `json:"go"`        // Go toolchain version
}

var (
	buildOnce sync.Once
	buildVal  buildMeta
)

// buildInfo returns the binary's embedded VCS metadata, read once and cached.
func buildInfo() buildMeta {
	buildOnce.Do(func() {
		buildVal.Go = runtime.Version()
		if info, ok := debug.ReadBuildInfo(); ok {
			applyBuildSettings(&buildVal, info.Settings)
		}
	})
	return buildVal
}

// applyBuildSettings folds the vcs.* build settings into m. Split out so it is
// unit-testable with synthetic settings (ReadBuildInfo can't be faked).
func applyBuildSettings(m *buildMeta, settings []debug.BuildSetting) {
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			m.Revision = s.Value
		case "vcs.time":
			m.Time = s.Value
		case "vcs.modified":
			m.Modified = s.Value == "true"
		}
	}
}
