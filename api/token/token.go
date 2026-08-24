// Package token mints and validates Atlas's opaque share tokens.
//
// A share token is the whole authorization for an unauthenticated URL: a public
// process start link (ADR-0029) or a shared documentation version (ADR-0143) is
// readable by whoever holds the token and nobody else. Two properties follow, and
// they are why this is a package rather than a helper beside one of its callers:
//
//   - It must be unguessable, so it is 32 bytes of CSPRNG output. There is nothing
//     to brute-force and nothing to derive it from.
//   - It must be *shaped*, because these tokens name their own files on disk. A
//     token that is not bare hex is refused before it reaches the filesystem, which
//     is what stops a request-supplied one from addressing a path (see
//     api/sidecar, whose stores apply the same predicate to keys on the way in).
//
// Both callers use the same two functions, so a token minted by one is valid by
// the other's rule — the alternative, a second copy of the hex check, is how the
// two definitions drift apart and one of them stops refusing something.
package token

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// tokenBytes is the entropy behind a token. 32 bytes is the same budget the
// deploy-token credential uses (ADR-0129) — far past guessing, and short enough
// that the URL stays pasteable.
const tokenBytes = 32

// New mints a fresh opaque token: 32 random bytes as lowercase hex.
func New() (string, error) {
	var b [tokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("token: random: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// IsHex reports whether s has the shape [New] produces: non-empty, even-length,
// hex. It is the guard that keeps a token supplied by a request from naming a
// foreign file or traversing out of the directory it is looked up in, so it is
// applied before a token is ever used as a path.
func IsHex(s string) bool {
	if s == "" || len(s)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
