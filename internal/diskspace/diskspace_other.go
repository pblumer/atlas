//go:build !linux && !darwin && !freebsd && !windows

package diskspace

// Of reports ErrUnsupported: this platform has no reading this package knows how to
// take. The storage monitor then shows nothing and freezes nothing, rather than
// guessing a number.
func Of(string) (Usage, error) { return Usage{}, ErrUnsupported }
