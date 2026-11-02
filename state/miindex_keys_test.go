package state

import (
	"bytes"
	"testing"
)

func TestMIIndexKeysKeepRepeatedBodyActivationsSeparate(t *testing.T) {
	first := keyMIIteration(10, 20, 100, 30)
	second := keyMIIteration(10, 20, 101, 30)
	if bytes.Equal(first, second) {
		t.Fatal("repeated body activations collided")
	}
	if !bytes.HasPrefix(first, miIterationPrefix(10, 20, 100)) {
		t.Fatal("iteration key outside body prefix")
	}
	if bytes.HasPrefix(second, miIterationPrefix(10, 20, 100)) {
		t.Fatal("different activation leaked into body prefix")
	}
	if bytes.Compare(first, second) >= 0 {
		t.Fatal("activation position not ordered")
	}
}

func TestMIIndexKeysRoundTripAndRejectCorruption(t *testing.T) {
	key := keyMIIteration(1, 2, 3, 4)
	pi, body, pos, round, ok := decodeMIIterationKey(key)
	if !ok || pi != 1 || body != 2 || pos != 3 || round != 4 {
		t.Fatalf("round trip: %d %d %d %d %v", pi, body, pos, round, ok)
	}
	for _, bad := range [][]byte{nil, key[:32], append([]byte{byte(cfMIBody)}, key[1:]...)} {
		if _, _, _, _, valid := decodeMIIterationKey(bad); valid {
			t.Fatalf("accepted invalid key %x", bad)
		}
	}
}

func TestMIIndexKeysBoundBodyAndExactRound(t *testing.T) {
	a := keyMIBody(42, 100, 7)
	b := keyMIBody(42, 101, 7)
	if !bytes.HasPrefix(a, miBodyPrefix(42)) || bytes.Compare(a, b) >= 0 {
		t.Fatal("body keys not bounded and ordered")
	}
	if bytes.Equal(keyMIRoundByElement(42, 77), keyMIRoundByElement(43, 77)) {
		t.Fatal("exact lookup crossed instance boundary")
	}
}

func TestMIIndexKeyEncodingAllocations(t *testing.T) {
	// This tests the encoding operations in isolation. The final event-apply
	// integration must reuse buffers rather than calling these allocating helpers.
	var buffer [33]byte
	allocs := testing.AllocsPerRun(100, func() {
		dst := buffer[:0]
		dst = append(dst, byte(cfMIIteration))
		dst = appendBE64(dst, 1)
		dst = appendBE64(dst, 2)
		dst = appendBE64(dst, 3)
		dst = appendBE64(dst, 4)
		if len(dst) != len(buffer) {
			panic("bad key length")
		}
	})
	if allocs != 0 {
		t.Fatalf("encoding allocated %.2f times", allocs)
	}
}
