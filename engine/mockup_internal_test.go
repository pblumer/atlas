package engine

import "testing"

// TestMockupHashDeterministic proves the mixer is a pure function of (seed, salt):
// the same inputs always yield the same output, and the two salts decorrelate the
// duration and failure draws so they never move together.
func TestMockupHashDeterministic(t *testing.T) {
	if mockupHash(42, mockupDurationSalt) != mockupHash(42, mockupDurationSalt) {
		t.Fatal("mockupHash is not deterministic")
	}
	if mockupHash(42, mockupDurationSalt) == mockupHash(42, mockupFailSalt) {
		t.Fatal("the duration and fail salts produce identical draws; they must decorrelate")
	}
}

// TestMockupDurationNanosInRange proves the simulated duration always lands within
// [min, max], collapses to the fixed value when min == max, and varies across keys.
func TestMockupDurationNanosInRange(t *testing.T) {
	const min, max = int64(1e9), int64(5e9)
	seen := map[int64]bool{}
	for k := uint64(1); k <= 200; k++ {
		d := mockupDurationNanos(k, min, max)
		if d < min || d > max {
			t.Fatalf("duration for key %d = %d, out of [%d,%d]", k, d, min, max)
		}
		seen[d] = true
	}
	if len(seen) < 10 {
		t.Fatalf("duration barely varies across keys (%d distinct values); the draw looks degenerate", len(seen))
	}
	// A fixed duration (min == max) ignores the key entirely.
	if got := mockupDurationNanos(12345, max, max); got != max {
		t.Fatalf("fixed-duration mockup = %d, want %d", got, max)
	}
}

// TestMockupShouldFailEndpoints proves the probability endpoints short-circuit
// (0 never fails, 1_000_000 always does) and an intermediate rate splits the key
// space into both outcomes.
func TestMockupShouldFailEndpoints(t *testing.T) {
	for k := uint64(1); k <= 50; k++ {
		if mockupShouldFail(k, 0) {
			t.Fatalf("failRate 0 failed on key %d", k)
		}
		if !mockupShouldFail(k, 1_000_000) {
			t.Fatalf("failRate 1 succeeded on key %d", k)
		}
	}
	var fails, passes int
	for k := uint64(1); k <= 1000; k++ {
		if mockupShouldFail(k, 500_000) {
			fails++
		} else {
			passes++
		}
	}
	if fails == 0 || passes == 0 {
		t.Fatalf("a 50%% failRate produced %d fails and %d passes; expected both", fails, passes)
	}
}
