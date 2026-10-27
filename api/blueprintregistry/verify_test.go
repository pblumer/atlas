package blueprintregistry

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func testIndex(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../docs/contracts/blueprint-registry/v1/examples/valid-index.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signIndex(raw []byte) ([]byte, ed25519.PublicKey) {
	seed := sha256.Sum256([]byte("bmp0 deterministic test-only signing key"))
	private := ed25519.NewKeyFromSeed(seed[:])
	signature := ed25519.Sign(private, raw)
	return []byte(base64.StdEncoding.EncodeToString(signature)), private.Public().(ed25519.PublicKey)
}

func verifyTestIndex(t *testing.T, raw []byte) error {
	t.Helper()
	signature, public := signIndex(raw)
	_, err := VerifySignedIndex(raw, signature, public)
	return err
}

func mutateTestIndex(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	var index map[string]any
	if err := json.Unmarshal(testIndex(t), &index); err != nil {
		t.Fatal(err)
	}
	mutate(index)
	raw, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func releaseFields(index map[string]any) map[string]any {
	return index["releases"].([]any)[0].(map[string]any)
}

func TestVerifySignedIndexAcceptsPinnedPublisher(t *testing.T) {
	raw := testIndex(t)
	signature, public := signIndex(raw)
	index, err := VerifySignedIndex(raw, signature, public)
	if err != nil {
		t.Fatal(err)
	}
	if index.SchemaVersion != 1 || index.RegistryID != "org.example.community" || len(index.Releases) != 1 {
		t.Fatalf("unexpected verified index: %+v", index)
	}
	r := index.Releases[0]
	if r.BlueprintID != "org.example.purchase-approval" || r.PublisherID != "org.example" || r.Version != "1.0.0" {
		t.Fatalf("invalid provenance: %+v", r)
	}
	if len(r.Needs.WorkerTypes) != 0 || r.Artifact.SizeBytes != 2048 {
		t.Fatalf("unexpected dependencies or size: %+v", r)
	}
}

func TestVerifySignedIndexRejectsUntrustedBytes(t *testing.T) {
	raw := testIndex(t)
	signature, public := signIndex(raw)
	wrongSeed := sha256.Sum256([]byte("another key"))
	wrong := ed25519.NewKeyFromSeed(wrongSeed[:]).Public().(ed25519.PublicKey)
	tampered := append([]byte(nil), raw...)
	tampered[0] ^= 1
	decoded, err := base64.StdEncoding.DecodeString(string(signature))
	if err != nil {
		t.Fatal(err)
	}
	decoded[0] ^= 1
	for _, tc := range []struct {
		name string
		raw  []byte
		sig  []byte
		key  []byte
	}{
		{"modified index", tampered, signature, public},
		{"modified signature", raw, []byte(base64.StdEncoding.EncodeToString(decoded)), public},
		{"wrong signing key", raw, signature, wrong},
		{"short key", raw, signature, public[:31]},
		{"invalid base64", raw, []byte("not-base64"), public},
		{"line wrapped signature", raw, append(append([]byte(nil), signature...), '\n'), public},
		{"oversized index", make([]byte, MaxIndexBytes+1), signature, public},
		{"empty index", nil, signature, public},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := VerifySignedIndex(tc.raw, tc.sig, tc.key); err == nil {
				t.Fatal("untrusted index accepted")
			}
		})
	}
}

func TestVerifySignedIndexRejectsUnknownAndDuplicateJSON(t *testing.T) {
	raw := testIndex(t)
	cases := [][]byte{
		append(append([]byte(nil), raw...), []byte("{}")...),
		bytesReplace(t, raw, "\"schemaVersion\": 1", "\"schemaVersion\": 1, \"schemaVersion\": 1"),
		mutateTestIndex(t, func(v map[string]any) { v["unknown"] = true }),
		mutateTestIndex(t, func(v map[string]any) { releaseFields(v)["credential"] = "must-not-exist" }),
		append(append([]byte(nil), raw...), 0xff),
	}
	for i, candidate := range cases {
		if err := verifyTestIndex(t, candidate); err == nil {
			t.Fatalf("invalid JSON case %d accepted", i)
		}
	}
}

func bytesReplace(t *testing.T, original []byte, old, replacement string) []byte {
	t.Helper()
	if !strings.Contains(string(original), old) {
		t.Fatalf("fixture does not contain %q", old)
	}
	return []byte(strings.Replace(string(original), old, replacement, 1))
}

func TestVerifySignedIndexRejectsInvalidReleaseMetadata(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unsupported version", func(v map[string]any) { v["schemaVersion"] = 2 }},
		{"bad registry", func(v map[string]any) { v["registryId"] = "UNTRUSTED" }},
		{"wrong publisher", func(v map[string]any) { releaseFields(v)["publisherId"] = "com.attacker" }},
		{"bad blueprint id", func(v map[string]any) { releaseFields(v)["blueprintId"] = "a" }},
		{"empty title", func(v map[string]any) { releaseFields(v)["title"] = "" }},
		{"invalid release", func(v map[string]any) { releaseFields(v)["version"] = "01.2.3" }},
		{"invalid prerelease", func(v map[string]any) { releaseFields(v)["version"] = "1.0.0-01" }},
		{"bad minimum", func(v map[string]any) { releaseFields(v)["atlas"].(map[string]any)["minVersion"] = "v1.2.3" }},
		{"inverted version range", func(v map[string]any) { releaseFields(v)["atlas"].(map[string]any)["maxVersionExclusive"] = "0.1.0" }},
		{"empty version range", func(v map[string]any) { releaseFields(v)["atlas"].(map[string]any)["maxVersionExclusive"] = "0.2.0" }},
		{"bad artifact digest", func(v map[string]any) { releaseFields(v)["artifact"].(map[string]any)["digest"] = "sha256:abc" }},
		{"relative traversal", func(v map[string]any) { releaseFields(v)["artifact"].(map[string]any)["path"] = "../archive.tar.gz" }},
		{"absolute artifact", func(v map[string]any) { releaseFields(v)["artifact"].(map[string]any)["path"] = "https://evil.invalid/archive.tar.gz" }},
		{"url-escaped traversal", func(v map[string]any) { releaseFields(v)["artifact"].(map[string]any)["path"] = "packages/%2e%2e/a.tar.gz" }},
		{"wrong extension", func(v map[string]any) { releaseFields(v)["artifact"].(map[string]any)["path"] = "p.zip" }},
		{"too large", func(v map[string]any) { releaseFields(v)["artifact"].(map[string]any)["sizeBytes"] = MaxPackageBytes + 1 }},
		{"zero size", func(v map[string]any) { releaseFields(v)["artifact"].(map[string]any)["sizeBytes"] = 0 }},
		{"secret-shaped need", func(v map[string]any) { releaseFields(v)["needs"].(map[string]any)["workerTypes"] = []string{"access/token"} }},
		{"duplicate need", func(v map[string]any) { releaseFields(v)["needs"].(map[string]any)["serverConstants"] = []string{"DB", "DB"} }},
		{"empty licence", func(v map[string]any) { releaseFields(v)["license"] = "" }},
		{"duplicate release", func(v map[string]any) {
			r := releaseFields(v)
			v["releases"] = []any{r, r}
		}},
		{"too many releases", func(v map[string]any) {
			r := releaseFields(v)
			releases := make([]any, MaxReleases+1)
			for i := range releases {
				releases[i] = r
			}
			v["releases"] = releases
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := verifyTestIndex(t, mutateTestIndex(t, tc.mutate)); err == nil {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
}

func TestVerifyArchiveMatchesCompressedBytes(t *testing.T) {
	data := []byte("a fake compressed tar byte stream for digest identity")
	sum := sha256.Sum256(data)
	release := Release{
		Artifact: Artifact{
			Path:      "packages/x.tar.gz",
			Digest:    "sha256:" + fmtHex(sum[:]),
			SizeBytes: int64(len(data)),
		},
	}
	if err := VerifyArchive(release, data); err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), data...)
	tampered[0] ^= 1
	if err := VerifyArchive(release, tampered); err == nil {
		t.Fatal("tampered bytes accepted")
	}
	if err := VerifyArchive(release, data[:len(data)-1]); err == nil {
		t.Fatal("wrong byte size accepted")
	}
	release.Artifact.Digest = "sha256:" + strings.Repeat("F", 64)
	if err := VerifyArchive(release, data); err == nil {
		t.Fatal("wrong digest syntax accepted")
	}
}

func fmtHex(data []byte) string {
	const digits = "0123456789abcdef"
	var builder strings.Builder
	for _, b := range data {
		builder.WriteByte(digits[b>>4])
		builder.WriteByte(digits[b&15])
	}
	return builder.String()
}
