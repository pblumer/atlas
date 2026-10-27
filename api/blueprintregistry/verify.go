// Package blueprintregistry verifies signed Blueprint catalogues and pinned
// package bytes without network access, mutations, or engine hot-path work.
package blueprintregistry

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// These limits constrain untrusted metadata before any JSON processing.
const (
	MaxIndexBytes   = 1 << 20
	MaxReleases     = 512
	MaxPackageBytes = 50 << 20
)

var (
	namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*\.[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)
	needsPattern     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)
	licensePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]*$`)
	versionPattern   = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
)

// Index represents an index whose signature and structural validity have
// already been checked by VerifySignedIndex. Constructed values are not trusted.
type Index struct {
	SchemaVersion int       `json:"schemaVersion"`
	RegistryID    string    `json:"registryId"`
	Releases      []Release `json:"releases"`
}

// Release is a version-pinned Blueprint description, distinct from a local
// application release or from a bpmn.io element template.
type Release struct {
	BlueprintID string       `json:"blueprintId"`
	Version     string       `json:"version"`
	PublisherID string       `json:"publisherId"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	License     string       `json:"license"`
	Atlas       Compatibility `json:"atlas"`
	Artifact    Artifact     `json:"artifact"`
	Needs       Needs        `json:"needs"`
}

// Compatibility uses an inclusive minimum and optional exclusive maximum.
type Compatibility struct {
	MinVersion          string `json:"minVersion"`
	MaxVersionExclusive string `json:"maxVersionExclusive,omitempty"`
}

// Artifact pins the digest of the exact compressed ADR-0467 package bytes.
type Artifact struct {
	Path      string `json:"path"`
	Digest    string `json:"digest"`
	SizeBytes int64  `json:"sizeBytes"`
}

// Needs names dependencies for presentation, never credential values; imports
// must separately derive their actual requirements from the package source.
type Needs struct {
	WorkerTypes     []string `json:"workerTypes"`
	ServerConstants []string `json:"serverConstants"`
}

// VerifySignedIndex authenticates exact JSON bytes against an operator-pinned
// Ed25519 key, and only then decodes a bounded, strict registry v1 manifest.
// The signature file is RFC 4648 base64 without whitespace or wrapping.
func VerifySignedIndex(raw, signatureFile []byte, trustedPublicKey ed25519.PublicKey) (Index, error) {
	var zero Index
	if len(raw) == 0 || len(raw) > MaxIndexBytes {
		return zero, fmt.Errorf("registry index size must be 1..%d bytes", MaxIndexBytes)
	}
	if len(trustedPublicKey) != ed25519.PublicKeySize {
		return zero, errors.New("registry public key must be 32 bytes")
	}
	if len(signatureFile) != base64.StdEncoding.EncodedLen(ed25519.SignatureSize) {
		return zero, errors.New("registry detached signature must be unwrapped base64")
	}
	var signature [ed25519.SignatureSize]byte
	n, err := base64.StdEncoding.Strict().Decode(signature[:], signatureFile)
	if err != nil || n != ed25519.SignatureSize {
		return zero, errors.New("registry detached signature is invalid base64")
	}
	if !ed25519.Verify(trustedPublicKey, raw, signature[:]) {
		return zero, errors.New("registry signature does not match the pinned key")
	}
	if !utf8.Valid(raw) {
		return zero, errors.New("registry index is not valid UTF-8")
	}
	scanner := json.NewDecoder(bytes.NewReader(raw))
	if err := checkUniqueJSON(scanner, 0); err != nil {
		return zero, fmt.Errorf("registry JSON: %w", err)
	}
	if _, err := scanner.Token(); !errors.Is(err, io.EOF) {
		return zero, errors.New("registry JSON must contain exactly one value")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var index Index
	if err := decoder.Decode(&index); err != nil {
		return zero, fmt.Errorf("registry index decoding: %w", err)
	}
	if err := validateIndex(index); err != nil {
		return zero, err
	}
	return index, nil
}

// checkUniqueJSON rejects ambiguous duplicate object keys before Go's normal
// decoder could silently keep only the last value. It does not execute code.
func checkUniqueJSON(decoder *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("JSON is nested too deeply")
	}
	value, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := value.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			tok, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := tok.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, found := seen[key]; found {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			seen[key] = struct{}{}
			if err := checkUniqueJSON(decoder, depth+1); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := checkUniqueJSON(decoder, depth+1); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

func validateIndex(index Index) error {
	if index.SchemaVersion != 1 {
		return fmt.Errorf("unsupported registry schema version %d", index.SchemaVersion)
	}
	if !validNamespace(index.RegistryID) {
		return errors.New("invalid registry id")
	}
	if index.Releases == nil || len(index.Releases) > MaxReleases {
		return fmt.Errorf("releases missing or more than %d", MaxReleases)
	}
	seen := make(map[string]struct{}, len(index.Releases))
	for i, release := range index.Releases {
		if err := validateRelease(release); err != nil {
			return fmt.Errorf("release %d: %w", i, err)
		}
		key := release.BlueprintID + "\x00" + release.Version
		if _, found := seen[key]; found {
			return fmt.Errorf("duplicate Blueprint release %s@%s", release.BlueprintID, release.Version)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateRelease(r Release) error {
	if !validNamespace(r.PublisherID) || !validNamespace(r.BlueprintID) || !strings.HasPrefix(r.BlueprintID, r.PublisherID+".") {
		return errors.New("Blueprint id does not belong to its publisher namespace")
	}
	if !validVersion(r.Version) {
		return errors.New("Blueprint release version must be semantic")
	}
	if len(r.Title) < 1 || len(r.Title) > 120 || len(r.Description) < 1 || len(r.Description) > 1000 {
		return errors.New("invalid release title or description")
	}
	if len(r.License) < 1 || len(r.License) > 160 || !licensePattern.MatchString(r.License) {
		return errors.New("invalid release licence")
	}
	if !validVersion(r.Atlas.MinVersion) {
		return errors.New("invalid minimum atlas version")
	}
	if r.Atlas.MaxVersionExclusive != "" {
		if !validVersion(r.Atlas.MaxVersionExclusive) || compareVersion(r.Atlas.MinVersion, r.Atlas.MaxVersionExclusive) >= 0 {
			return errors.New("atlas compatibility range must be increasing")
		}
	}
	if !validArtifact(r.Artifact) {
		return errors.New("invalid artifact path, size or SHA-256 digest")
	}
	if err := validateNeeds(r.Needs.WorkerTypes); err != nil {
		return fmt.Errorf("Worker Types: %w", err)
	}
	if err := validateNeeds(r.Needs.ServerConstants); err != nil {
		return fmt.Errorf("server constants: %w", err)
	}
	return nil
}

func validNamespace(value string) bool {
	return len(value) >= 5 && len(value) <= 180 && namespacePattern.MatchString(value)
}

func validateNeeds(values []string) error {
	if values == nil || len(values) > 128 {
		return errors.New("missing or oversized requirement list")
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if len(value) > 160 || !needsPattern.MatchString(value) {
			return fmt.Errorf("invalid requirement name %q", value)
		}
		if _, found := seen[value]; found {
			return fmt.Errorf("duplicate requirement %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validArtifact(a Artifact) bool {
	if a.SizeBytes <= 0 || a.SizeBytes > MaxPackageBytes || !validDigest(a.Digest) {
		return false
	}
	if len(a.Path) < 8 || len(a.Path) > 256 || !strings.HasSuffix(a.Path, ".tar.gz") {
		return false
	}
	for _, segment := range strings.Split(a.Path, "/") {
		if segment == "" || !isASCIILetterOrDigit(segment[0]) {
			return false
		}
		for i := 1; i < len(segment); i++ {
			c := segment[i]
			if !isASCIILetterOrDigit(c) && c != '_' && c != '-' && c != '.' {
				return false
			}
		}
	}
	return true
}

func isASCIILetterOrDigit(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for i := len("sha256:"); i < len(value); i++ {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// VerifyArchive validates the byte count and SHA-256 of the exact compressed
// package bytes against an already verified release descriptor. It does not
// unpack, import, deploy or publish the archive.
func VerifyArchive(release Release, compressed []byte) error {
	if !validDigest(release.Artifact.Digest) {
		return errors.New("package has an invalid expected digest")
	}
	if release.Artifact.SizeBytes <= 0 || release.Artifact.SizeBytes > MaxPackageBytes || int64(len(compressed)) != release.Artifact.SizeBytes {
		return errors.New("package byte size differs from the signed registry index")
	}
	sum := sha256.Sum256(compressed)
	if "sha256:"+hex.EncodeToString(sum[:]) != release.Artifact.Digest {
		return errors.New("package SHA-256 differs from the signed registry index")
	}
	return nil
}
