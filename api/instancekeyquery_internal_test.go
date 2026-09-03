package api

import "testing"

// TestInstanceKeyQuery pins what counts as a bare instance key. Only a plain
// number is one: anything else must stay a content search, because a query that
// looks numeric but is not a key still has to find zip=3098.
func TestInstanceKeyQuery(t *testing.T) {
	for _, tc := range []struct {
		in   string
		key  uint64
		want bool
	}{
		{"281474978949437", 281474978949437, true},
		{" 3098 ", 3098, true}, // trimmed, like anything else pasted into a box
		{"0", 0, true},
		{"", 0, false},
		{"+123", 0, false},
		{"-123", 0, false},
		{"12a", 0, false},
		{"MT-1998", 0, false},
		{"customerType=1998", 0, false},
		{"18446744073709551615", 18446744073709551615, true}, // the largest uint64
		{"18446744073709551616", 0, false},                   // one past it: not a key
		{"999999999999999999999999999999", 0, false},         // and nowhere near one
	} {
		key, ok := instanceKeyQuery(tc.in)
		if ok != tc.want || (ok && key != tc.key) {
			t.Errorf("instanceKeyQuery(%q) = (%d, %v), want (%d, %v)", tc.in, key, ok, tc.key, tc.want)
		}
	}
}
