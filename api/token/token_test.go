package token

import (
	"strings"
	"testing"
)

// TestNewIsHexAndFullLength pins the shape: what New mints must satisfy IsHex,
// or a freshly minted token would be refused by the guard that protects its own
// lookup.
func TestNewIsHexAndFullLength(t *testing.T) {
	tok, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(tok) != 2*tokenBytes {
		t.Errorf("len = %d, want %d hex chars", len(tok), 2*tokenBytes)
	}
	if !IsHex(tok) {
		t.Errorf("a minted token %q is not accepted by IsHex", tok)
	}
	if strings.ToLower(tok) != tok {
		t.Errorf("token %q is not lowercase hex", tok)
	}
}

// TestNewIsUnpredictable is a smoke test for the entropy source: tokens must not
// repeat. A counter or a seeded PRNG would fail this immediately.
func TestNewIsUnpredictable(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := New()
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if seen[tok] {
			t.Fatalf("New repeated a token after %d draws: %q", i, tok)
		}
		seen[tok] = true
	}
}

// TestIsHexRefusesEverythingElse covers the guard's whole job: anything that
// could name a file other than a token's own is refused.
func TestIsHexRefusesEverythingElse(t *testing.T) {
	for _, bad := range []string{
		"",                  // empty
		"abc",               // odd length
		"zz",                // not hex
		"../secret",         // traversal
		"..",                // traversal
		"beef.json",         // a filename, not a token
		"be ef",             // whitespace
		"BEEF\x00",          // NUL
		"/etc/passwd",       // absolute path
		"beef/../../secret", // traversal behind a valid prefix
	} {
		if IsHex(bad) {
			t.Errorf("IsHex(%q) = true, want false", bad)
		}
	}
	// Uppercase hex is still hex: encoding/hex decodes it, and refusing it would
	// reject a token a case-preserving proxy handed back.
	if !IsHex("BEEF") {
		t.Error("IsHex(BEEF) = false, want true")
	}
}
