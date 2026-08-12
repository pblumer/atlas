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

// BuildInfo is the exported view of the running binary's version and embedded
// VCS build metadata, for callers outside this package — the `atlas version`
// command. The GET /api/v1/info endpoint reports the same values.
type BuildInfo struct {
	Version  string // product version (api.Version), stampable via -ldflags
	Revision string // git commit, "" if built outside a VCS checkout
	Time     string // commit time (RFC3339), "" if unknown
	Modified bool   // true if built from a dirty working tree
	Go       string // Go toolchain version
}

// Build returns the running binary's version together with its embedded VCS
// build metadata. It reuses the same cached source as the /info endpoint, so
// the CLI and the HTTP surface never disagree about what is running.
func Build() BuildInfo {
	b := buildInfo()
	return BuildInfo{
		Version:  Version,
		Revision: b.Revision,
		Time:     b.Time,
		Modified: b.Modified,
		Go:       b.Go,
	}
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
