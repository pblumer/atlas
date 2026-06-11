package state

import (
	"bytes"
	"testing"
)

func TestOrderedInt64Sorts(t *testing.T) {
	// Encoded big-endian bytes must sort in numeric order, including negatives.
	ordered := []int64{-1 << 62, -1000, -1, 0, 1, 1000, 1 << 62}
	var prev []byte
	for i, v := range ordered {
		got := appendOrderedInt64(nil, v)
		if i > 0 && bytes.Compare(prev, got) >= 0 {
			t.Errorf("encoding not increasing at %d (value %d): % x !< % x", i, v, prev, got)
		}
		prev = got
	}
}

func TestPrefixEnd(t *testing.T) {
	tests := []struct {
		in   []byte
		want []byte
	}{
		{[]byte{0x01}, []byte{0x02}},
		{[]byte{0x01, 0x02}, []byte{0x01, 0x03}},
		{[]byte{0x01, 0xff}, []byte{0x02}}, // carry past trailing 0xff
		{[]byte{0xff, 0xff}, nil},          // no finite bound
	}
	for _, tt := range tests {
		got := prefixEnd(tt.in)
		if !bytes.Equal(got, tt.want) {
			t.Errorf("prefixEnd(% x) = % x, want % x", tt.in, got, tt.want)
		}
	}
}

func TestTimerKeyOrdersByDueDate(t *testing.T) {
	// For the range scan to work, earlier due dates must sort before later ones
	// regardless of the timer key.
	early := keyTimer(100, 9_999_999)
	late := keyTimer(200, 1)
	if bytes.Compare(early, late) >= 0 {
		t.Errorf("earlier due date did not sort first: % x !< % x", early, late)
	}
}
