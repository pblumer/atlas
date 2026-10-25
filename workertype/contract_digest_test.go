package workertype

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCanonicalTechnicalContractIgnoresWhitespaceAndMemberOrder(t *testing.T) {
	raw, err := os.ReadFile("testdata/technical-account-create.json")
	if err != nil {
		t.Fatal(err)
	}
	original, err := CanonicalTechnicalContractJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := DigestTechnicalContractJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		t.Fatal(err)
	}
	compactDigest, err := DigestTechnicalContractJSON(compact.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if digest != compactDigest {
		t.Fatalf("whitespace changed contract digest: %s != %s", digest, compactDigest)
	}
	// Reordering the top-level properties is a presentation change.
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	reordered := append([]byte(`{"spec":`), object["spec"]...)
	reordered = append(reordered, []byte(`,"metadata":`)...)
	reordered = append(reordered, object["metadata"]...)
	reordered = append(reordered, []byte(`,"kind":`)...)
	reordered = append(reordered, object["kind"]...)
	reordered = append(reordered, []byte(`,"apiVersion":`)...)
	reordered = append(reordered, object["apiVersion"]...)
	reordered = append(reordered, '}')
	reorderedDigest, err := DigestTechnicalContractJSON(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if digest != reorderedDigest {
		t.Fatalf("property order changed contract digest: %s != %s", digest, reorderedDigest)
	}
	repeat, err := CanonicalTechnicalContractJSON(original)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, repeat) {
		t.Fatalf("canonical encoding is not idempotent: %s != %s", original, repeat)
	}
	hash := sha256.Sum256(original)
	if digest != "sha256:"+hex.EncodeToString(hash[:]) {
		t.Fatalf("digest must hash the canonical artifact: %q", digest)
	}
}

func TestTechnicalContractDigestChangesWithMeaning(t *testing.T) {
	raw, err := os.ReadFile("testdata/technical-account-create.json")
	if err != nil {
		t.Fatal(err)
	}
	base, err := DigestTechnicalContractJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*TechnicalContract)
	}{
		{"new contract version", func(c *TechnicalContract) { c.Metadata.Version = "1.0.1" }},
		{"new profile", func(c *TechnicalContract) { c.Metadata.Profile = "another-directory" }},
		{"different resource kind", func(c *TechnicalContract) { c.Spec.ResourceKind = "contact-record" }},
		{"changed schema requirements", func(c *TechnicalContract) {
			c.Spec.InputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["newField"]}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ParseTechnicalContractJSON(raw)
			if err != nil {
				t.Fatal(err)
			}
			tc.edit(&c)
			other, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DigestTechnicalContractJSON(other)
			if err != nil {
				t.Fatal(err)
			}
			if got == base {
				t.Fatalf("%s must invalidate digest", tc.name)
			}
		})
	}
}

func TestTechnicalContractDigestRejectsDuplicateMembersAtAnyDepth(t *testing.T) {
	raw, err := os.ReadFile("testdata/technical-account-create.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(string(raw), `"kind":`, `"kind":"TechnicalCapabilityContract","kind":`, 1),
		strings.Replace(string(raw), `"type": "object"`, `"type":"object","type": "object"`, 1),
		strings.Replace(string(raw), `"userName": { "type": "string" }`, `"userName":{"type":"string","type":"string"}`, 1),
	} {
		if _, err := DigestTechnicalContractJSON([]byte(bad)); err == nil {
			t.Fatalf("duplicate JSON member was accepted")
		}
	}
}

func TestTechnicalContractDigestRejectsInvalidOrMultipleDocuments(t *testing.T) {
	raw, err := os.ReadFile("testdata/technical-account-create.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{
		nil,
		[]byte(`{}`),
		append(bytes.Clone(raw), []byte(` {}`)...),
		[]byte(`{"apiVersion":false}`),
		bytes.Repeat([]byte("x"), maxTechnicalContractBytes+1),
	} {
		if _, err := CanonicalTechnicalContractJSON(bad); err == nil {
			t.Fatalf("invalid artifact (length %d) was accepted", len(bad))
		}
	}
}

func TestTechnicalContractDigestDoesNotCertifyWorkerClaims(t *testing.T) {
	raw, err := os.ReadFile("testdata/technical-account-create.json")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := DigestTechnicalContractJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		t.Fatalf("invalid digest format: %q", digest)
	}
	// A content digest only binds contract bytes. It does not identify the
	// package digest, reviewed evidence or executing identity of a Worker.
}
