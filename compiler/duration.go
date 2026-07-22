package compiler

import (
	"fmt"
	"regexp"
	"strconv"
)

// iso8601Duration matches the day-and-time subset of ISO-8601 durations Atlas
// supports for timers: P[nD]T[nH][nM][nS]. Years and months are intentionally
// excluded (their length depends on the calendar), matching Zeebe's timeDuration.
var iso8601Duration = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// parseISO8601Duration converts an ISO-8601 duration like "PT30S" or "P1DT2H" to
// nanoseconds. It rejects the empty duration ("P" / "PT" with no components) and
// anything outside the supported day/hour/minute/second subset.
func parseISO8601Duration(s string) (int64, error) {
	m := iso8601Duration.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("not an ISO-8601 duration (use e.g. PT30S, PT5M, P1DT2H): %q", s)
	}
	var total int64
	units := []struct {
		text string
		mult int64
	}{
		{m[1], int64(24 * 3600)}, // days
		{m[2], int64(3600)},      // hours
		{m[3], int64(60)},        // minutes
		{m[4], int64(1)},         // seconds
	}
	any := false
	for _, u := range units {
		if u.text == "" {
			continue
		}
		any = true
		n, err := strconv.ParseInt(u.text, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("duration component %q: %w", u.text, err)
		}
		total += n * u.mult
	}
	if !any {
		return 0, fmt.Errorf("empty duration %q", s)
	}
	return total * int64(1e9), nil
}
