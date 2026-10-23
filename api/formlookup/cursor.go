package formlookup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

// A cursor continues a search where its last page ended. What it continues with is the
// provider's own continuation — Graph's @odata.nextLink, which Graph says to use whole
// — and that link must never come from the browser: it is a URL with the query in it,
// and a person who could edit it could change the filter the server built.
//
// So the server seals it. The cursor is the link and what it is bound to — the task,
// the field, the person and the normalised query — encrypted and authenticated with
// AES-GCM under a key that exists only in this process's memory. It is opaque to the
// person, cannot be forged or altered, opens only for the same query by the same
// person on the same task, and expires. A restart makes a new key and every cursor
// invalid, which costs nothing but a fresh search: nothing about a lookup survives a
// restart, by design.

// cursorLifetime bounds how long a page may be continued. A person reads a page in
// seconds; this is only there so a cursor is not a capability that lasts.
const cursorLifetime = 15 * time.Minute

var errBadCursor = errors.New("the cursor is not one this server issued for this search")

// cursorBinding is what a cursor is valid for.
type cursorBinding struct {
	Task      uint64 `json:"t"`
	Field     string `json:"f"`
	Principal string `json:"p"`
	Query     string `json:"q"`
}

type sealedCursor struct {
	cursorBinding
	Next    string `json:"n"`
	Expires int64  `json:"e"`
}

type sealer struct {
	aead cipher.AEAD
	now  func() time.Time
}

func newSealer() *sealer {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("formlookup: no randomness for the cursor key: " + err.Error())
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic("formlookup: " + err.Error())
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic("formlookup: " + err.Error())
	}
	return &sealer{aead: aead, now: time.Now}
}

func (s *sealer) seal(b cursorBinding, next string) string {
	plain, _ := json.Marshal(sealedCursor{cursorBinding: b, Next: next, Expires: s.now().Add(cursorLifetime).Unix()})
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic("formlookup: no randomness for a cursor: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, plain, nil))
}

func (s *sealer) open(cursor string, want cursorBinding) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return "", errBadCursor
	}
	plain, err := s.aead.Open(nil, raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():], nil)
	if err != nil {
		return "", errBadCursor
	}
	var c sealedCursor
	if err := json.Unmarshal(plain, &c); err != nil {
		return "", errBadCursor
	}
	if c.cursorBinding != want || s.now().Unix() > c.Expires || c.Next == "" {
		return "", errBadCursor
	}
	return c.Next, nil
}
