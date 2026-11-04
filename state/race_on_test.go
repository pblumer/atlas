//go:build race

package state

// raceEnabled reports whether the race detector instruments this build. Its
// instrumentation allocates on Pebble's read path, so allocation counts measured
// under it describe the detector, not the code.
const raceEnabled = true
