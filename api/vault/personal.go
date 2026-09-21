package vault

// Crypto-shredding for declared personal data (ADR-0314).
//
// The vault already seals every secret under one master key. Erasing one data subject
// by destroying that key would make the whole installation unreadable, so this adds
// exactly one level of indirection and nothing else:
//
//   - each data subject gets a random 32-byte **data key**, stored as an ordinary
//     vault secret under a reserved name. Because it is an ordinary secret it is
//     itself sealed under the master key — no new key material on disk, no second
//     store, and the master key keeps rotating exactly as it does today.
//   - a variable a process declares personal is sealed under that data key at the
//     edge, before it becomes a command, and is opened again at the edge that hands
//     it to a worker or shows it to a person.
//   - erasing a subject deletes that one secret.
//
// The property that makes this an answer to R-06 rather than another retention knob:
// every copy carries the same ciphertext. The WAL segment, the state record, the
// checkpoint, the exported OpenSearch document, last year's backup, an instance
// snapshot somebody exported — all of them hold bytes the destroyed key decrypted.
// Nothing has to be found, coordinated or reached.
//
// Why the key is *stored* and not derived: a key derived from the master key and the
// subject id (HKDF, say) would need no storage at all, which is precisely what makes
// it useless here — there would be nothing to destroy. Erasure requires key material
// that can be deleted, so the data key is a stored secret by necessity, not by
// convenience.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// dataKeyPrefix reserves a region of the vault's name space for data keys. A data key
// is not a credential an operator sets or reads: the secrets API refuses to write or
// delete a name in this region, and List omits them, so the only way to remove one is
// the erasure route that says what it is doing.
const dataKeyPrefix = "atlas:personal-data-key/"

// envelopeKey is the single JSON member an enciphered value is wrapped in. It makes
// the ciphertext self-describing, which is what lets the opening edge work from the
// value alone: it does not have to re-resolve which variables a definition declared
// personal, and it therefore still opens a value whose process has since been
// migrated to a version that declares something else (ADR-0314).
const envelopeKey = "atlas:personal"

// ErrErased is returned by Open when the subject's data key is gone: the value is
// permanently unreadable, which is the intended outcome of an erasure and not a
// fault. Callers distinguish it from a genuine failure because the two need opposite
// handling — one is reported to an operator as "erased", the other as broken.
var ErrErased = errors.New("vault: the data subject's key has been erased; this value is permanently unreadable")

// DataKeyName is the vault name a subject's data key is stored under. Exported so the
// secrets API can refuse the region and an operator view can label it.
func DataKeyName(subject string) string { return dataKeyPrefix + subject }

// IsDataKeyName reports whether a vault name belongs to the data-key region.
func IsDataKeyName(name string) bool { return strings.HasPrefix(name, dataKeyPrefix) }

// SubjectOfDataKeyName returns the subject a data-key name belongs to.
func SubjectOfDataKeyName(name string) (string, bool) {
	if !IsDataKeyName(name) {
		return "", false
	}
	return strings.TrimPrefix(name, dataKeyPrefix), true
}

// Envelope is an enciphered value as it is carried everywhere else in Atlas: in a
// command, in an event, in the state record, in a checkpoint, in an export.
//
// Subject and Kind are readable because a reader that cannot open the value still has
// to render it — an operator inspecting an instance is shown whose data this is and
// what type it had, not a wall of base64. The nonce and the ciphertext are not
// exported: only this package can open one, which is the same boundary the package
// already draws around a secret's plaintext.
type Envelope struct {
	// Subject names whose data key opens this value. It is in the clear on purpose:
	// ADR-0314's primary defence is that people are named by reference, so the id is
	// exactly the part that was never meant to be hidden — and carrying it here is
	// what makes the ciphertext self-contained in a backup nobody can interpret any
	// more.
	Subject string
	// Kind is the caller's own value kind, carried so an opened value returns as the
	// type it had rather than as a string. This package does not interpret it.
	Kind  uint8
	nonce []byte
	ct    []byte
}

type envelopeJSON struct {
	Subject string `json:"subject"`
	Kind    uint8  `json:"kind"`
	Nonce   string `json:"nonce"` // base64
	Value   string `json:"value"` // base64 ciphertext
}

// Text renders the envelope as the canonical JSON that is stored and transported.
func (e Envelope) Text() string {
	raw, err := json.Marshal(map[string]envelopeJSON{envelopeKey: {
		Subject: e.Subject,
		Kind:    e.Kind,
		Nonce:   base64.StdEncoding.EncodeToString(e.nonce),
		Value:   base64.StdEncoding.EncodeToString(e.ct),
	}})
	if err != nil { // unreachable: every field is a string or a uint8
		return ""
	}
	return string(raw)
}

// ParseEnvelope reads a stored value back into an envelope, ok=false when the text is
// not one.
//
// The parse is strict — the marker member, all four fields present, decodable base64,
// a non-empty subject — because an ordinary variable whose value happened to resemble
// an envelope would otherwise be treated as enciphered. Strictness makes an accidental
// collision vanishingly unlikely; a deliberate one is possible and harmless, since the
// forged envelope simply fails to open and only affects the instance its author
// submitted it to.
func ParseEnvelope(text string) (Envelope, bool) {
	if !strings.HasPrefix(text, `{"`+envelopeKey+`":`) {
		return Envelope{}, false
	}
	var wrapper map[string]envelopeJSON
	if err := json.Unmarshal([]byte(text), &wrapper); err != nil {
		return Envelope{}, false
	}
	e, ok := wrapper[envelopeKey]
	if !ok || len(wrapper) != 1 || e.Subject == "" {
		return Envelope{}, false
	}
	nonce, err := base64.StdEncoding.DecodeString(e.Nonce)
	if err != nil || len(nonce) == 0 {
		return Envelope{}, false
	}
	ct, err := base64.StdEncoding.DecodeString(e.Value)
	if err != nil || len(ct) == 0 {
		return Envelope{}, false
	}
	return Envelope{Subject: e.Subject, Kind: e.Kind, nonce: nonce, ct: ct}, true
}

// IsEnciphered reports whether a stored value is an envelope. It needs no key, so any
// layer can ask — which is how the operator UI labels a value rather than showing it.
func IsEnciphered(text string) bool {
	_, ok := ParseEnvelope(text)
	return ok
}

// Seal enciphers one value of one variable under the subject's data key, creating that
// key on first use.
//
// name binds the ciphertext to the variable it belongs to (with the subject, as the
// AEAD's additional data), so an envelope cannot be moved to another variable — or to
// another subject's — and still open. It survives the operations that legitimately
// copy a value under its own name: a fork, a migration, a checkpoint.
func (v *Vault) Seal(subject, name string, kind uint8, plaintext string) (Envelope, error) {
	if subject == "" {
		return Envelope{}, fmt.Errorf("vault: seal %q: no data subject", name)
	}
	aead, err := v.dataKeyAEAD(subject, true)
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Envelope{}, fmt.Errorf("vault: nonce: %w", err)
	}
	ct := aead.Seal(nil, nonce, []byte(plaintext), personalAAD(subject, name))
	return Envelope{Subject: subject, Kind: kind, nonce: nonce, ct: ct}, nil
}

// Open deciphers an envelope belonging to the named variable. A subject whose key has
// been erased is ErrErased, which is an answer and not a failure.
func (v *Vault) Open(name string, e Envelope) (string, error) {
	aead, err := v.dataKeyAEAD(e.Subject, false)
	if err != nil {
		return "", err
	}
	plain, err := aead.Open(nil, e.nonce, e.ct, personalAAD(e.Subject, name))
	if err != nil {
		return "", fmt.Errorf("vault: open personal value %q of subject %q: %w", name, e.Subject, err)
	}
	return string(plain), nil
}

// Erase destroys a subject's data key, which is the whole of an erasure: every copy of
// every value ever sealed under it, in every store and every backup, becomes
// permanently unreadable in one act. It is idempotent, so a repeated request is not an
// error.
func (v *Vault) Erase(subject string) error {
	if subject == "" {
		return fmt.Errorf("vault: erase: no data subject")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.Delete(DataKeyName(subject))
}

// HasDataKey reports whether a subject still has a data key, i.e. whether their values
// are still readable.
func (v *Vault) HasDataKey(subject string) (bool, error) {
	_, ok, err := v.load(DataKeyName(subject))
	return ok, err
}

// DataSubjects lists the subjects that hold a data key, oldest first. It is the
// operator's view of what an erasure could still act on; it reveals ids, which are
// references by ADR-0314's design and not the content the keys protect.
func (v *Vault) DataSubjects() ([]Meta, error) {
	all, err := v.listAll()
	if err != nil {
		return nil, err
	}
	out := make([]Meta, 0, len(all))
	for _, m := range all {
		if subject, ok := SubjectOfDataKeyName(m.Name); ok {
			m.Name = subject
			out = append(out, m)
		}
	}
	return out, nil
}

// personalAAD binds a sealed value to its subject and its variable name. The separator
// cannot occur in either part of a well-formed pair, so no two different pairs share
// an AAD.
func personalAAD(subject, name string) []byte {
	return []byte(dataKeyPrefix + subject + "\x00" + name)
}

// dataKeyAEAD returns the AES-256-GCM cipher for a subject's data key, generating and
// storing the key when create is set and none exists.
//
// The lock is here and nowhere else in this package for one reason: unlike the rest of
// the vault, which the run-loop goroutine owns, this path runs in HTTP handlers and is
// therefore entered concurrently. Without it two requests for a subject with no key
// yet would both generate one, both store it, and one of them would have sealed a
// value under a key that the winner's write has already replaced — silent, permanent
// data loss for that value. Everything else the vault does is either immutable state
// or a whole-file read/atomic-rename write, which concurrency already tolerates.
//
// No cipher is cached. A seal costs a file read and two key schedules, which is an
// edge doing crypto and not the processor path I1 governs; caching would add an
// invalidation to get wrong on erasure for no measured gain.
func (v *Vault) dataKeyAEAD(subject string, create bool) (cipher.AEAD, error) {
	if subject == "" {
		return nil, fmt.Errorf("vault: no data subject")
	}
	name := DataKeyName(subject)
	if create {
		v.mu.Lock()
		defer v.mu.Unlock()
	}
	raw, ok, err := v.Get(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		if !create {
			return nil, fmt.Errorf("%w (subject %q)", ErrErased, subject)
		}
		key := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, fmt.Errorf("vault: generate data key: %w", err)
		}
		raw = base64.StdEncoding.EncodeToString(key)
		if _, err := v.Set(name, raw); err != nil {
			return nil, err
		}
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("vault: data key of subject %q is not 32 bytes", subject)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("vault: data key cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("vault: data key gcm: %w", err)
	}
	return aead, nil
}
