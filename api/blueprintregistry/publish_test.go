package blueprintregistry

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"strings"
	"testing"
)

func publishTestKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("publisher-side deterministic test-only key"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func publishTestFiles() []SourceFile {
	return []SourceFile{
		{Path: "processes/b.bpmn", Data: []byte("<definitions id=\"b\"/>\n")},
		{Path: "atlas.json", Data: []byte(`{"formatVersion":1,"key":"sample","name":"Sample","processes":[],"forms":[],"decisions":[]}`)},
		{Path: "katalog.json", Data: []byte(`{"publish":true,"catalogs":[{"id":"c1","items":["p1"]}],"products":[{"id":"p1","homeCatalog":"c1"}]}`)},
	}
}

// The archive is a function of its files: order, time and environment do not
// reach the bytes, so a release lock can compare digests across builds.
func TestWritePackageIsDeterministicAndInspectable(t *testing.T) {
	files := publishTestFiles()
	first, err := WritePackage(files)
	if err != nil {
		t.Fatal(err)
	}
	reversed := []SourceFile{files[2], files[1], files[0]}
	second, err := WritePackage(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("the same files in another order gave other archive bytes")
	}
	release := Release{Artifact: ArtifactOf("packages/sample-1.0.0.tar.gz", first)}
	inspected, err := InspectPackage(release, first)
	if err != nil {
		t.Fatalf("a written package fails inspection: %v", err)
	}
	if inspected.ApplicationKey != "sample" || len(inspected.Files) != 3 || inspected.Files[0].Path != "atlas.json" {
		t.Fatalf("inspection = %+v", inspected)
	}
	if len(inspected.Catalogues) != 1 || !inspected.CataloguePublish {
		t.Fatalf("catalogue preview lost: %+v", inspected)
	}
}

func TestWritePackageRefusesUnsafeOrAmbiguousPaths(t *testing.T) {
	for _, name := range []string{"../escape.bpmn", "/abs.bpmn", "a\\b.bpmn", "x/../y.bpmn", ""} {
		if _, err := WritePackage([]SourceFile{{Path: name, Data: []byte("x")}}); err == nil {
			t.Errorf("path %q was written", name)
		}
	}
	if _, err := WritePackage([]SourceFile{{Path: "A.bpmn"}, {Path: "a.bpmn"}}); err == nil {
		t.Error("two members differing only in case were written")
	}
	// USTAR cannot hold a name this long without a PAX record, which the
	// Console's tar reader would not read back as the same name.
	long := strings.Repeat("n", 120) + ".bpmn"
	if _, err := WritePackage([]SourceFile{{Path: long}}); err == nil {
		t.Error("a name USTAR cannot carry was written")
	}
	if _, err := WritePackage(nil); err == nil {
		t.Error("an empty package was written")
	}
}

// What SignIndex returns is exactly what a server accepts with the public half,
// and nothing a server would refuse can be signed.
func TestSignIndexRoundTripsThroughTheVerifier(t *testing.T) {
	key := publishTestKey()
	archive, err := WritePackage(publishTestFiles())
	if err != nil {
		t.Fatal(err)
	}
	index := Index{SchemaVersion: 1, RegistryID: "org.example.hub", Releases: []Release{{
		BlueprintID: "org.example.sample", Version: "1.0.0", PublisherID: "org.example",
		Title: "Sample", Description: "A sample", License: "AGPL-3.0-only",
		Atlas:    Compatibility{MinVersion: "0.21.0-0"},
		Artifact: ArtifactOf("packages/org.example.sample-1.0.0.tar.gz", archive),
		Needs:    Needs{WorkerTypes: []string{"mail"}, ServerConstants: []string{}},
	}}}
	raw, sig, err := SignIndex(index, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifySignedIndex(raw, sig, key.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatalf("signed index does not verify: %v", err)
	}
	if _, err := InspectPackage(got.Releases[0], archive); err != nil {
		t.Fatalf("listed package does not verify: %v", err)
	}
	other := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	if _, err := VerifySignedIndex(raw, sig, other.Public().(ed25519.PublicKey)); err == nil {
		t.Fatal("index verified under a key that did not sign it")
	}

	bad := index
	bad.Releases = []Release{index.Releases[0]}
	bad.Releases[0].Needs.ServerConstants = nil // the verifier requires a list
	if _, _, err := SignIndex(bad, key); err == nil {
		t.Fatal("an index the verifier refuses was signed")
	}
}

func TestSigningKeyTextRoundTrips(t *testing.T) {
	key := publishTestKey()
	parsed, err := ParseSigningKey([]byte("  " + EncodeSigningKey(key) + "\n"))
	if err != nil || !bytes.Equal(parsed, key) {
		t.Fatalf("key did not round-trip: %v", err)
	}
	for _, text := range []string{"", "not base64!", EncodePublicKey(key.Public().(ed25519.PublicKey))[:20]} {
		if _, err := ParseSigningKey([]byte(text)); err == nil {
			t.Errorf("accepted %q as a signing key", text)
		}
	}
	if fp := KeyFingerprint(key.Public().(ed25519.PublicKey)); !strings.HasPrefix(fp, "sha256:") || len(fp) != 71 {
		t.Errorf("fingerprint = %q", fp)
	}
}

func TestNextPatch(t *testing.T) {
	for in, want := range map[string]string{"1.0.0": "1.0.1", "1.2.9": "1.2.10", "2.3.0-rc.1": "2.3.1"} {
		if got, err := NextPatch(in); err != nil || got != want {
			t.Errorf("NextPatch(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NextPatch("1.0"); err == nil {
		t.Error("NextPatch accepted a non-semantic version")
	}
}
