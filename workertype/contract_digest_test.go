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

func readAccountContract(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/technical-account-create.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestTheDigestIsTheSHA256OfTheFilesExactBytes: ADR-0483 names a contract by its
// file's bytes, never by a re-serialization, so any tool in any language can
// recompute it, and a reformatted file is a different artifact.
func TestTheDigestIsTheSHA256OfTheFilesExactBytes(t *testing.T) {
	raw := readAccountContract(t)
	digest, err := DigestTechnicalContractJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	if want := "sha256:" + hex.EncodeToString(hash[:]); digest != want {
		t.Fatalf("digest = %s, want the SHA-256 of the file's bytes %s", digest, want)
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		t.Fatal(err)
	}
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
	for name, other := range map[string][]byte{"compacted": compact.Bytes(), "reordered": reordered} {
		got, err := DigestTechnicalContractJSON(other)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got == digest {
			t.Errorf("%s file kept the digest of the original; a reformatted file is a different artifact", name)
		}
	}
}

func TestTechnicalContractDigestChangesWithMeaning(t *testing.T) {
	raw := readAccountContract(t)
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

// TestTheDigestRefusesBytesACheckoutCouldChange: a byte-order mark, a carriage
// return or invalid UTF-8 would make the same contract hash differently on
// another machine or editor, so such a file gets no digest at all.
func TestTheDigestRefusesBytesACheckoutCouldChange(t *testing.T) {
	raw := readAccountContract(t)
	for name, bad := range map[string][]byte{
		"byte-order mark":   append([]byte("\xef\xbb\xbf"), raw...),
		"CRLF line endings": bytes.ReplaceAll(raw, []byte("\n"), []byte("\r\n")),
		"a lone CR":         bytes.Replace(raw, []byte("\n"), []byte("\r"), 1),
		"invalid UTF-8":     bytes.Replace(raw, []byte(`"kind"`), []byte("\"kind\xff\""), 1),
	} {
		if _, err := DigestTechnicalContractJSON(bad); err == nil {
			t.Errorf("%s: a digest was computed", name)
		}
	}
}

func TestTechnicalContractDigestRejectsDuplicateMembersAtAnyDepth(t *testing.T) {
	raw := readAccountContract(t)
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
	raw := readAccountContract(t)
	for _, bad := range [][]byte{
		nil,
		[]byte(`{}`),
		append(bytes.Clone(raw), []byte(` {}`)...),
		[]byte(`{"apiVersion":false}`),
		bytes.Repeat([]byte("x"), maxTechnicalContractBytes+1),
		raw[:len(raw)/2],
		[]byte(`{"a":` + strings.Repeat("[", maxTechnicalDigestJSONDepth+2) + strings.Repeat("]", maxTechnicalDigestJSONDepth+2) + `}`),
	} {
		if _, err := DigestTechnicalContractJSON(bad); err == nil {
			t.Fatalf("invalid artifact (length %d) was accepted", len(bad))
		}
	}
	deep := []byte(`{"a":` + strings.Repeat("[", maxTechnicalDigestJSONDepth+2) + strings.Repeat("]", maxTechnicalDigestJSONDepth+2) + `}`)
	if _, err := DigestTechnicalContractJSON(deep); err == nil || !strings.Contains(err.Error(), "nesting") {
		t.Fatalf("deep nesting: err %v, want the nesting bound named", err)
	}
}

func TestTechnicalContractDigestDoesNotCertifyWorkerClaims(t *testing.T) {
	digest, err := DigestTechnicalContractJSON(readAccountContract(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		t.Fatalf("invalid digest format: %q", digest)
	}
	// A content digest only binds contract bytes. It does not identify the
	// package digest, reviewed evidence or executing identity of a Worker.
}
