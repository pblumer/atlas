package blueprintregistry

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// A key rotation statement is how a registry announces its next signing key
// through a channel an installation can check on its own: the statement names
// the new public key and is signed with the key the installation already pins
// (ADR-0499 amendment 2026-11-02 c). A forged announcement — a mail, a chat
// message, a page on the registry host — carries no such signature, so it
// cannot talk an operator into pinning somebody else's key.
//
// It proves that whoever holds the current key wants the new one. It cannot
// prove more: a stolen current key signs a statement just as well. That is why
// the statement is only half of the fixed channel — it is also published at a
// fixed place in the repository, behind its review, and a compromised or lost
// key is never rotated this way but confirmed in person (examples/hub/README.md).

// MinRotationLead is the shortest time between announcing a key and the
// earliest day the registry may switch to it: long enough for every operator to
// pin both keys, and for the owner to notice an announcement they did not make.
const MinRotationLead = 7 * 24 * time.Hour

// KeyRotation is the signed statement.
type KeyRotation struct {
	// Kind is fixed, so a signature over some other document of the registry —
	// its index — can never be presented as a rotation.
	Kind       string `json:"kind"`
	RegistryID string `json:"registryId"`
	// OldFingerprint names the key that signs the statement; NewPublicKey is the
	// successor as ATLAS_BLUEPRINT_REGISTRY_KEY takes it, NewFingerprint its
	// SHA-256 for comparing by eye or by phone.
	OldFingerprint string `json:"oldFingerprint"`
	NewPublicKey   string `json:"newPublicKey"`
	NewFingerprint string `json:"newFingerprint"`
	// Announced and SwitchNotBefore are calendar days (YYYY-MM-DD, UTC): the
	// registry signs with the new key on SwitchNotBefore at the earliest.
	Announced       string `json:"announced"`
	SwitchNotBefore string `json:"switchNotBefore"`
}

// KeyRotationKind is the only Kind a statement may carry.
const KeyRotationKind = "atlas-hub-key-rotation/v1"

const rotationDay = "2006-01-02"

// NewKeyRotation fills in a statement for rotating from old to next, announced
// on announced, switching not before switchOn.
func NewKeyRotation(registryID string, old, next ed25519.PublicKey, announced, switchOn time.Time) KeyRotation {
	return KeyRotation{
		Kind: KeyRotationKind, RegistryID: registryID,
		OldFingerprint: KeyFingerprint(old),
		NewPublicKey:   EncodePublicKey(next), NewFingerprint: KeyFingerprint(next),
		Announced: announced.UTC().Format(rotationDay), SwitchNotBefore: switchOn.UTC().Format(rotationDay),
	}
}

// NewKey decodes the successor key of a statement validate accepted.
func (r KeyRotation) NewKey() ed25519.PublicKey {
	raw, _ := base64.StdEncoding.Strict().DecodeString(r.NewPublicKey)
	return ed25519.PublicKey(raw)
}

func (r KeyRotation) validate() error {
	if r.Kind != KeyRotationKind {
		return fmt.Errorf("statement kind must be %q", KeyRotationKind)
	}
	if !validNamespace(r.RegistryID) {
		return errors.New("statement names an invalid registry id")
	}
	next, err := base64.StdEncoding.Strict().DecodeString(r.NewPublicKey)
	if err != nil || len(next) != ed25519.PublicKeySize {
		return errors.New("statement's new key is not a base64 32-byte Ed25519 public key")
	}
	if KeyFingerprint(next) != r.NewFingerprint {
		return errors.New("statement's new fingerprint does not belong to its new key")
	}
	if r.NewFingerprint == r.OldFingerprint {
		return errors.New("statement rotates a key to itself")
	}
	announced, err := time.Parse(rotationDay, r.Announced)
	if err != nil {
		return errors.New("statement's announced day is not YYYY-MM-DD")
	}
	switchOn, err := time.Parse(rotationDay, r.SwitchNotBefore)
	if err != nil {
		return errors.New("statement's switch day is not YYYY-MM-DD")
	}
	if switchOn.Sub(announced) < MinRotationLead {
		return fmt.Errorf("statement gives operators less than %d days between announcement and switch", int(MinRotationLead.Hours()/24))
	}
	return nil
}

// SignKeyRotation renders the statement as the exact bytes that are published
// and signs them with the current key. It refuses a statement a verifier would
// refuse, and one whose old fingerprint is not the signing key's.
func SignKeyRotation(r KeyRotation, current ed25519.PrivateKey) (raw, signature []byte, err error) {
	if len(current) != ed25519.PrivateKeySize {
		return nil, nil, errors.New("rotation must be signed with an Ed25519 private key")
	}
	if r.OldFingerprint != KeyFingerprint(current.Public().(ed25519.PublicKey)) {
		return nil, nil, errors.New("rotation must be signed by the key it rotates from")
	}
	if err := r.validate(); err != nil {
		return nil, nil, err
	}
	raw, err = json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	raw = append(raw, '\n')
	signature = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(current, raw)))
	return raw, signature, nil
}

// VerifyKeyRotation checks a published statement against the keys an
// installation pins and returns it when one of them signed it: the exact bytes,
// a strict document, the signer named in the statement, and a successor other
// than the signer, announced with at least MinRotationLead before the switch.
func VerifyKeyRotation(raw, signatureFile []byte, pinned []ed25519.PublicKey) (KeyRotation, error) {
	var zero KeyRotation
	if len(pinned) == 0 || len(pinned) > MaxPinnedKeys {
		return zero, fmt.Errorf("verification needs 1..%d pinned keys", MaxPinnedKeys)
	}
	if len(raw) == 0 || len(raw) > 64<<10 {
		return zero, errors.New("rotation statement is empty or too large")
	}
	if len(signatureFile) != base64.StdEncoding.EncodedLen(ed25519.SignatureSize) {
		return zero, errors.New("rotation signature must be unwrapped base64")
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(string(signatureFile))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return zero, errors.New("rotation signature is invalid base64")
	}
	var signer ed25519.PublicKey
	for _, key := range pinned {
		if len(key) == ed25519.PublicKeySize && ed25519.Verify(key, raw, sig) {
			signer = key
			break
		}
	}
	if signer == nil {
		return zero, errors.New("rotation statement is not signed by a key this installation pins")
	}
	dup := json.NewDecoder(bytes.NewReader(raw))
	if err := checkUniqueJSON(dup, 0); err != nil {
		return zero, fmt.Errorf("rotation statement JSON: %w", err)
	}
	if _, err := dup.Token(); !errors.Is(err, io.EOF) {
		return zero, errors.New("rotation statement must contain exactly one JSON value")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var r KeyRotation
	if err := dec.Decode(&r); err != nil {
		return zero, fmt.Errorf("rotation statement: %w", err)
	}
	if err := r.validate(); err != nil {
		return zero, err
	}
	if r.OldFingerprint != KeyFingerprint(signer) {
		return zero, errors.New("rotation statement names another key than the one that signed it")
	}
	return r, nil
}
