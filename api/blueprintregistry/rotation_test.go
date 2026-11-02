package blueprintregistry

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func rotationKeys() (current, next, stranger ed25519.PrivateKey) {
	k := func(s string) ed25519.PrivateKey {
		seed := sha256.Sum256([]byte(s))
		return ed25519.NewKeyFromSeed(seed[:])
	}
	return k("rotation statement: current"), k("rotation statement: next"), k("rotation statement: stranger")
}

func pub(k ed25519.PrivateKey) ed25519.PublicKey { return k.Public().(ed25519.PublicKey) }

var announcedDay = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)

// A statement signed with the pinned key verifies and names the successor; any
// change to its bytes, another signer, or a key not pinned here is refused.
func TestKeyRotationRoundTripsAndRefusesForgeries(t *testing.T) {
	current, next, stranger := rotationKeys()
	stmt := NewKeyRotation("io.github.pblumer.atlas.hub", pub(current), pub(next), announcedDay, announcedDay.AddDate(0, 0, 14))
	raw, sig, err := SignKeyRotation(stmt, current)
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifyKeyRotation(raw, sig, []ed25519.PublicKey{pub(current)})
	if err != nil {
		t.Fatalf("own statement refused: %v", err)
	}
	if string(got.NewKey()) != string(pub(next)) || got.SwitchNotBefore != "2026-11-16" {
		t.Fatalf("statement = %+v", got)
	}
	// Pinned as the second of two keys, it still verifies.
	if _, err := VerifyKeyRotation(raw, sig, []ed25519.PublicKey{pub(stranger), pub(current)}); err != nil {
		t.Fatalf("statement refused with two pinned keys: %v", err)
	}

	// A forger without the current key: signed by a stranger, naming the stranger's
	// successor — the announcement an attacker would mail around.
	forged := NewKeyRotation("io.github.pblumer.atlas.hub", pub(current), pub(stranger), announcedDay, announcedDay.AddDate(0, 0, 14))
	forged.OldFingerprint = KeyFingerprint(pub(current))
	if _, _, err := SignKeyRotation(forged, stranger); err == nil {
		t.Fatal("a statement was signed by a key it does not rotate from")
	}
	forgedRaw := []byte(strings.Replace(string(raw), stmt.NewPublicKey, EncodePublicKey(pub(stranger)), 1))
	forgedSig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(stranger, forgedRaw)))
	if _, err := VerifyKeyRotation(forgedRaw, forgedSig, []ed25519.PublicKey{pub(current)}); err == nil {
		t.Fatal("a statement signed by an unpinned key verified")
	}
	if _, err := VerifyKeyRotation(forgedRaw, sig, []ed25519.PublicKey{pub(current)}); err == nil {
		t.Fatal("altered statement bytes verified")
	}

	// Signed by a pinned key that the statement does not name as its old key.
	other := NewKeyRotation("io.github.pblumer.atlas.hub", pub(stranger), pub(next), announcedDay, announcedDay.AddDate(0, 0, 14))
	otherRaw, otherSig, err := SignKeyRotation(other, stranger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyKeyRotation(otherRaw, otherSig, []ed25519.PublicKey{pub(stranger)}); err != nil {
		t.Fatalf("a correctly self-described statement by a pinned key was refused: %v", err)
	}
	if _, err := VerifyKeyRotation(raw, sig, nil); err == nil {
		t.Fatal("verified against no pinned key")
	}
}

func TestKeyRotationRefusesMalformedStatements(t *testing.T) {
	current, next, _ := rotationKeys()
	base := NewKeyRotation("io.github.pblumer.atlas.hub", pub(current), pub(next), announcedDay, announcedDay.AddDate(0, 0, 14))
	for name, mutate := range map[string]func(*KeyRotation){
		"too short a lead":        func(r *KeyRotation) { r.SwitchNotBefore = "2026-11-05" },
		"switch before announce":  func(r *KeyRotation) { r.SwitchNotBefore = "2026-10-01" },
		"a fingerprint that lies": func(r *KeyRotation) { r.NewFingerprint = r.OldFingerprint[:len(r.OldFingerprint)-1] + "0" },
		"a key to itself": func(r *KeyRotation) {
			r.NewPublicKey, r.NewFingerprint = EncodePublicKey(pub(current)), KeyFingerprint(pub(current))
		},
		"another kind":       func(r *KeyRotation) { r.Kind = "something-else" },
		"a bad registry id":  func(r *KeyRotation) { r.RegistryID = "Hub" },
		"a malformed day":    func(r *KeyRotation) { r.Announced = "2.11.2026" },
		"a malformed newkey": func(r *KeyRotation) { r.NewPublicKey = "not-a-key" },
	} {
		r := base
		mutate(&r)
		if _, _, err := SignKeyRotation(r, current); err == nil {
			t.Errorf("%s was signed", name)
		}
	}
	// Unknown fields and duplicate keys are refused even under a valid signature.
	raw, _, err := SignKeyRotation(base, current)
	if err != nil {
		t.Fatal(err)
	}
	for name, doc := range map[string]string{
		"an unknown field": strings.Replace(string(raw), `"kind"`, `"extra": 1, "kind"`, 1),
		"a duplicate key":  strings.Replace(string(raw), `"kind"`, `"registryId": "x.y", "kind"`, 1),
	} {
		sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(current, []byte(doc))))
		if _, err := VerifyKeyRotation([]byte(doc), sig, []ed25519.PublicKey{pub(current)}); err == nil {
			t.Errorf("%s verified", name)
		}
	}
}
