package vault

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/model"
)

// W5 of ADR-0314, the enciphering half. What these tests pin is not that AES works —
// the standard library's business — but the three properties the record rests its claim
// on, each of which is a decision that could have been made differently:
//
//  1. destroying one subject's key makes that subject's values permanently unreadable
//     and leaves everything else in the installation intact;
//  2. the ciphertext is self-contained, so the copies nobody can reach any more (a
//     backup tape, an exported snapshot) are covered by the same single act;
//  3. a sealed value cannot be moved — not to another variable, not to another
//     subject — and still open.

// TestASealedValueOpensBackToItself is the round trip, including the kind: a number
// that came back as a string would be a silent type change in every model that carries
// one.
func TestASealedValueOpensBackToItself(t *testing.T) {
	v := newTestVault(t)
	for _, tc := range []struct {
		name  string
		kind  uint8
		plain string
	}{
		{"vorname", 3, "Ida"},
		{"betrag", 2, "1234.50"},
		{"profil", 4, `{"kuerzel":"ib"}`},
		{"leer", 3, ""},
	} {
		env, err := v.Seal("P-4711", tc.name, tc.kind, tc.plain)
		if err != nil {
			t.Fatalf("Seal %s: %v", tc.name, err)
		}
		got, err := v.Open(tc.name, env)
		if err != nil {
			t.Fatalf("Open %s: %v", tc.name, err)
		}
		if got != tc.plain {
			t.Errorf("%s: opened %q, want %q", tc.name, got, tc.plain)
		}
		if env.Kind != tc.kind {
			t.Errorf("%s: kind %d survived as %d", tc.name, tc.kind, env.Kind)
		}
		if env.Subject != "P-4711" {
			t.Errorf("%s: subject %q", tc.name, env.Subject)
		}
	}
}

// TestTheStoredTextHoldsNoPlaintext is the assertion that makes every copy safe at
// once. It is deliberately crude — a substring search over the bytes that get written —
// because that is exactly the question an auditor asks of a backup tape: is the name in
// there or not.
func TestTheStoredTextHoldsNoPlaintext(t *testing.T) {
	v := newTestVault(t)
	env, err := v.Seal("P-4711", "vorname", 3, "Ida")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	text := env.Text()
	if strings.Contains(text, "Ida") {
		t.Errorf("the stored value carries the plaintext: %s", text)
	}
	if !json.Valid([]byte(text)) {
		t.Errorf("the stored value is not valid JSON: %s", text)
	}
	// The subject is in the clear on purpose: ADR-0314's primary defence is that
	// people are named by reference, so the id is the part that was never hidden.
	if !strings.Contains(text, "P-4711") {
		t.Errorf("the envelope does not name its subject, so a reader cannot tell whose key opens it: %s", text)
	}
}

// TestErasingASubjectMakesTheirValuesUnreadableAndNothingElse is the record's central
// claim, and the reason a data key exists per subject rather than per installation.
func TestErasingASubjectMakesTheirValuesUnreadableAndNothingElse(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.Set("gmail_ops", "s3cr3t"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	erased, err := v.Seal("P-4711", "vorname", 3, "Ida")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	kept, err := v.Seal("P-0815", "vorname", 3, "Jon")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if err := v.Erase("P-4711"); err != nil {
		t.Fatalf("Erase: %v", err)
	}

	if _, err := v.Open("vorname", erased); !errors.Is(err, ErrErased) {
		t.Errorf("opening an erased subject's value gave %v, want ErrErased — an erasure has to be an answer, not a fault to debug", err)
	}
	if got, err := v.Open("vorname", kept); err != nil || got != "Jon" {
		t.Errorf("another subject's value became unreadable: %q, %v", got, err)
	}
	if got, ok, err := v.Get("gmail_ops"); err != nil || !ok || got != "s3cr3t" {
		t.Errorf("an ordinary secret became unreadable: %q, %v, %v", got, ok, err)
	}
	if err := v.Erase("P-4711"); err != nil {
		t.Errorf("a repeated erasure is an error: %v", err)
	}
	if has, err := v.HasDataKey("P-4711"); err != nil || has {
		t.Errorf("HasDataKey after erasure = %v, %v", has, err)
	}
}

// TestAnErasedSubjectIsUnreadableFromADifferentProcess is what "reaches the backup
// tape" means concretely: the envelope is bytes and the vault directory is a file
// tree, so a second Vault opened over the same directory — a restored copy, another
// process, a forensic read — is in exactly the position of every other copy. Nothing
// about the running server is what made the value unreadable.
func TestAnErasedSubjectIsUnreadableFromADifferentProcess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	key := testVaultKey(t)
	first, err := New(dir, key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	env, err := first.Seal("P-4711", "vorname", 3, "Ida")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// The value travels as text, the way it travels through a WAL segment or an export.
	parsed, ok := ParseEnvelope(env.Text())
	if !ok {
		t.Fatal("the stored text does not parse back as an envelope")
	}

	second, err := New(dir, key)
	if err != nil {
		t.Fatalf("New (second): %v", err)
	}
	if got, err := second.Open("vorname", parsed); err != nil || got != "Ida" {
		t.Fatalf("a second reader over the same vault could not open it: %q, %v", got, err)
	}
	if err := first.Erase("P-4711"); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if _, err := second.Open("vorname", parsed); !errors.Is(err, ErrErased) {
		t.Errorf("the second reader can still open an erased value: %v", err)
	}
}

// TestASealedValueCannotBeMoved is the additional-data binding. Without it an operator
// with write access to state could swap one subject's ciphertext into another's
// variable and the value would open — which would make the envelope a transferable
// token rather than a value belonging to one person and one name.
func TestASealedValueCannotBeMoved(t *testing.T) {
	v := newTestVault(t)
	env, err := v.Seal("P-4711", "vorname", 3, "Ida")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := v.Open("nachname", env); err == nil {
		t.Error("an envelope opened under a different variable name")
	}
	if _, err := v.Seal("P-0815", "x", 3, "x"); err != nil { // give the other subject a key
		t.Fatalf("Seal: %v", err)
	}
	moved := env
	moved.Subject = "P-0815"
	if _, err := v.Open("vorname", moved); err == nil {
		t.Error("an envelope relabelled to another subject opened under that subject's key")
	}
}

// TestOneKeyPerSubjectAcrossValues is why erasure is one act and not a search: the
// second value of the same subject reuses the first value's key, so there is exactly
// one thing to destroy however many values, instances and years accumulate.
func TestOneKeyPerSubjectAcrossValues(t *testing.T) {
	v := newTestVault(t)
	a, err := v.Seal("P-4711", "vorname", 3, "Ida")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	b, err := v.Seal("P-4711", "nachname", 3, "Iversen")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := v.Erase("P-4711"); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	for _, tc := range []struct {
		name string
		env  Envelope
	}{{"vorname", a}, {"nachname", b}} {
		if _, err := v.Open(tc.name, tc.env); !errors.Is(err, ErrErased) {
			t.Errorf("%s survived the one erasure: %v", tc.name, err)
		}
	}
}

// TestConcurrentFirstSealsAllOpen is the test the lock in dataKeyAEAD exists for, and
// it is the failure that would have been worst to ship: two handlers sealing the first
// two values of a new subject at the same time, one of them under a key the other's
// write replaced. Nothing would look wrong until somebody tried to read the value.
func TestConcurrentFirstSealsAllOpen(t *testing.T) {
	v := newTestVault(t)
	const n = 16
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		envs = map[string]Envelope{}
	)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := string(rune('a'+i)) + "var"
			env, err := v.Seal("P-4711", name, 3, "Ida")
			if err != nil {
				t.Errorf("Seal %s: %v", name, err)
				return
			}
			mu.Lock()
			envs[name] = env
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if len(envs) != n {
		t.Fatalf("sealed %d of %d values", len(envs), n)
	}
	for name, env := range envs {
		if got, err := v.Open(name, env); err != nil || got != "Ida" {
			t.Errorf("%s does not open: %q, %v — two first seals raced and one key won", name, got, err)
		}
	}
}

// TestADataKeyIsNotAnOperatorSecret keeps the two name spaces apart. A data key
// showing up in the secrets list is an invitation to delete it, and deleting it is an
// erasure of business data rather than the removal of a credential.
func TestADataKeyIsNotAnOperatorSecret(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.Set("gmail_ops", "s3cr3t"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := v.Seal("P-4711", "vorname", 3, "Ida"); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	metas, err := v.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 1 || metas[0].Name != "gmail_ops" {
		t.Errorf("List = %+v, want only the operator's secret", metas)
	}
	subjects, err := v.DataSubjects()
	if err != nil {
		t.Fatalf("DataSubjects: %v", err)
	}
	if len(subjects) != 1 || subjects[0].Name != "P-4711" {
		t.Errorf("DataSubjects = %+v, want [P-4711]", subjects)
	}
	if !IsDataKeyName(DataKeyName("P-4711")) {
		t.Error("a data key name is not recognised as one")
	}
	if IsDataKeyName("gmail_ops") {
		t.Error("an operator secret is taken for a data key")
	}
}

// TestParseEnvelopeIsStrict matters because the opening edges work from the value
// alone. Everything that is not unmistakably an envelope has to come back as not one,
// or an ordinary variable would be treated as enciphered.
func TestParseEnvelopeIsStrict(t *testing.T) {
	v := newTestVault(t)
	env, err := v.Seal("P-4711", "vorname", 3, "Ida")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !IsEnciphered(env.Text()) {
		t.Error("a real envelope is not recognised")
	}
	for _, bad := range []string{
		``,
		`"Ida"`,
		`{"vorname":"Ida"}`,
		`{"atlas:personal":{}}`,
		`{"atlas:personal":{"subject":"P-4711","kind":3,"nonce":"","value":""}}`,
		`{"atlas:personal":{"subject":"","kind":3,"nonce":"AAAA","value":"AAAA"}}`,
		`{"atlas:personal":{"subject":"P","kind":3,"nonce":"not base64!","value":"AAAA"}}`,
		`{"atlas:personal":{"subject":"P","kind":3,"nonce":"AAAAAAAAAAAAAAAA","value":""}}`,
		`{"atlas:personal":{"subject":"P","kind":3,"nonce":"AAAAAAAAAAAAAAAA","value":"not base64!"}}`,
		`{"atlas:personal":{"subject":"P","kind":3,"nonce":"AAAA","value":"AAAA"},"x":1}`,
	} {
		if IsEnciphered(bad) {
			t.Errorf("%q is taken for an envelope", bad)
		}
	}
	// A forged envelope parses — it is well formed — and must not open.
	forged := `{"atlas:personal":{"subject":"P-4711","kind":3,"nonce":"AAAAAAAAAAAAAAAA","value":"AAAAAAAAAAAAAAAAAAAAAAAA"}}`
	parsed, ok := ParseEnvelope(forged)
	if !ok {
		t.Fatal("a well-formed forgery does not parse; the strictness above is now too strict")
	}
	if _, err := v.Open("vorname", parsed); err == nil {
		t.Error("a forged envelope opened")
	}
}

// TestSealWithoutASubjectIsRefused is the fail-closed end of the edge. If an
// enciphering edge ever reaches the vault without having resolved a subject, the only
// acceptable outcome is a refusal: returning the plaintext, or sealing under an empty
// subject that every instance shares, would each defeat the mechanism quietly.
func TestSealWithoutASubjectIsRefused(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.Seal("", "vorname", 3, "Ida"); err == nil {
		t.Error("Seal accepted an empty data subject")
	}
	if err := v.Erase(""); err == nil {
		t.Error("Erase accepted an empty data subject")
	}
	if _, err := v.Open("vorname", Envelope{}); err == nil {
		t.Error("Open accepted an envelope with no subject")
	}
}

// TestACorruptedDataKeyIsNamedNotGuessed is about what an operator sees when the vault
// directory has been restored from an inconsistent backup, or a key file edited by hand.
// The failure has to say that the *key* is wrong, because the alternative — a raw AEAD
// authentication error on every value — reads like the data being corrupt and sends the
// reader looking in the wrong place.
func TestACorruptedDataKeyIsNamedNotGuessed(t *testing.T) {
	v := newTestVault(t)
	env, err := v.Seal("P-4711", "vorname", 3, "Ida")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// Not through the personal API: this is what a restore or a hand edit leaves behind.
	if _, err := v.Set(DataKeyName("P-4711"), "not-a-key"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"open", func() error { _, e := v.Open("vorname", env); return e }()},
		{"seal", func() error { _, e := v.Seal("P-4711", "nachname", 3, "Iversen"); return e }()},
	} {
		if tc.err == nil {
			t.Errorf("%s accepted a data key that is not 32 bytes", tc.name)
			continue
		}
		if !strings.Contains(tc.err.Error(), "32 bytes") || !strings.Contains(tc.err.Error(), "P-4711") {
			t.Errorf("%s error %q names neither the size nor the subject", tc.name, tc.err)
		}
	}
}

// TestTheEnvelopeMarkerIsTheOneTheEngineLooksFor keeps the two spellings of the same thing
// from drifting. The engine recognises a sealed value by model.EncipheredMarker without
// being able to open one; this package builds the envelope from its own member name. If the
// two stopped agreeing, the writer's refusal would stop seeing values this package seals —
// and a personal value would be accepted in the clear.
func TestTheEnvelopeMarkerIsTheOneTheEngineLooksFor(t *testing.T) {
	if want := `{"` + envelopeKey + `":`; model.EncipheredMarker != want {
		t.Errorf("model.EncipheredMarker = %q, but this package writes %q", model.EncipheredMarker, want)
	}
}

// TestADataKeyFromAnotherInstallationIsNamed is the operator error this has to survive: a
// vault directory copied from another installation, or restored beside a regenerated key
// file. The data key is itself a secret sealed under the master key, so the master key it
// was sealed under is what the failure has to name — otherwise every value of that subject
// reports its own decrypt failure and the reader looks for corrupt data instead of a wrong
// key.
func TestADataKeyFromAnotherInstallationIsNamed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	first, err := New(dir, testVaultKey(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	env, err := first.Seal("P-4711", "vorname", 3, "Ida")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// The same directory, a different master key: what a regenerated key file leaves.
	second, err := New(dir, testVaultKey(t))
	if err != nil {
		t.Fatalf("New (second): %v", err)
	}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"open", func() error { _, e := second.Open("vorname", env); return e }()},
		{"seal", func() error { _, e := second.Seal("P-4711", "nachname", 3, "Iversen"); return e }()},
	} {
		if tc.err == nil {
			t.Errorf("%s worked under a different master key", tc.name)
			continue
		}
		if !strings.Contains(tc.err.Error(), "different master key") {
			t.Errorf("%s error %q does not say the master key is wrong", tc.name, tc.err)
		}
	}
}
