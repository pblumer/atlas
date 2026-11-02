package mihistory

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"
)

// A cursor is the server's own statement of where a page ended, handed to the
// client opaquely and accepted back only if the server can verify it wrote it
// (ADR-0517). It carries keys and log positions only — never a variable value or
// any other process payload — and is bound to the instance, the body activation
// and a digest of the whole filter set, so it cannot be replayed against
// another query. It is authenticated with HMAC-SHA-256 under a key that lives
// only in this process: a restart, like the TTL, retires every cursor, and the
// client starts again from the first page. No Pebble snapshot is held between
// requests; the frozen high-water position is what keeps the pages consistent.

const (
	cursorVersion = 1

	kindBodies     byte = 1
	kindIterations byte = 2
	kindOutline    byte = 3
	kindSteps      byte = 4

	cursorPayloadSize = 1 + 1 + 8*9
	cursorMACSize     = 16
)

// cursor is the decoded state of a page boundary.
type cursor struct {
	kind      byte
	instance  uint64
	body      uint64
	bodyPos   uint64
	filter    uint64 // first 8 bytes of SHA-256 over the canonical filter
	highWater uint64
	// bodyVersion is the body's Changes count when the first page was
	// read. A state filter is a filter over mutable state; if the body changed
	// since, the page sequence cannot be continued without drift.
	bodyVersion uint64
	afterA      uint64 // bodies: activation position; iterations: loopCounter
	afterB      uint64 // bodies: body key; iterations: iteration key
	issuedAt    int64  // unix seconds
}

var (
	errCursorMalformed = errors.New("malformed cursor")
	errCursorForged    = errors.New("cursor was not issued by this server")
	errCursorExpired   = errors.New("cursor expired")
)

func (s *Service) encodeCursor(c cursor) string {
	buf := make([]byte, 0, cursorPayloadSize+cursorMACSize)
	buf = append(buf, cursorVersion, c.kind)
	for _, v := range [...]uint64{c.instance, c.body, c.bodyPos, c.filter, c.highWater, c.bodyVersion, c.afterA, c.afterB, uint64(c.issuedAt)} {
		buf = binary.BigEndian.AppendUint64(buf, v)
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write(buf)
	buf = mac.Sum(buf)[:cursorPayloadSize+cursorMACSize]
	return base64.RawURLEncoding.EncodeToString(buf)
}

func (s *Service) decodeCursor(text string, now time.Time) (cursor, error) {
	// The encoded length is fixed; reject anything else before decoding, so a
	// hostile parameter costs nothing to refuse.
	if len(text) != base64.RawURLEncoding.EncodedLen(cursorPayloadSize+cursorMACSize) {
		return cursor{}, errCursorMalformed
	}
	raw, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil || len(raw) != cursorPayloadSize+cursorMACSize {
		return cursor{}, errCursorMalformed
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write(raw[:cursorPayloadSize])
	if !hmac.Equal(mac.Sum(nil)[:cursorMACSize], raw[cursorPayloadSize:]) {
		return cursor{}, errCursorForged
	}
	if raw[0] != cursorVersion {
		return cursor{}, errCursorMalformed
	}
	u := func(i int) uint64 { return binary.BigEndian.Uint64(raw[2+8*i:]) }
	c := cursor{
		kind: raw[1], instance: u(0), body: u(1), bodyPos: u(2), filter: u(3),
		highWater: u(4), bodyVersion: u(5), afterA: u(6), afterB: u(7), issuedAt: int64(u(8)),
	}
	issued := time.Unix(c.issuedAt, 0)
	if now.Sub(issued) > s.cursorTTL || issued.Sub(now) > time.Minute {
		return cursor{}, errCursorExpired
	}
	return c, nil
}

// filterDigest binds a cursor to the exact canonical filter set it was issued for.
func filterDigest(canonical string) uint64 {
	sum := sha256.Sum256([]byte(canonical))
	return binary.BigEndian.Uint64(sum[:8])
}
