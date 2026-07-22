package compiler

import "testing"

func TestParseISO8601Duration(t *testing.T) {
	ok := []struct {
		in   string
		want int64
	}{
		{"PT30S", 30e9},
		{"PT5M", 5 * 60 * 1e9},
		{"PT1H", 3600 * 1e9},
		{"P1D", 24 * 3600 * 1e9},
		{"P1DT2H30M15S", (24*3600 + 2*3600 + 30*60 + 15) * int64(1e9)},
	}
	for _, tc := range ok {
		got, err := parseISO8601Duration(tc.in)
		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.in, err)
		} else if got != tc.want {
			t.Errorf("%s = %d, want %d", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"", "P", "PT", "30S", "P1Y", "PT1.5S", "abc"} {
		if _, err := parseISO8601Duration(bad); err == nil {
			t.Errorf("%q: want error, got nil", bad)
		}
	}
}
