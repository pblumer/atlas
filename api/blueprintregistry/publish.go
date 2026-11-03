package blueprintregistry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// The publisher's side of the v1 contract: what a registry operator runs to
// produce the bytes PublicClient and VerifySignedIndex accept. It is build and
// release tooling, never server code — nothing here is reachable from an HTTP
// route, and a server only ever holds the public half of the key.
//
// Every function is a pure function of its input, so the same files always give
// the same archive bytes and the same digest. That is what lets a release lock
// prove that a version was never published with two different contents.

// SourceFile is one file of an application package being written.
type SourceFile struct {
	Path string
	Data []byte
}

// packageEpoch is the modification time every archive member carries. A fixed
// time keeps the archive a function of its content alone.
var packageEpoch = time.Unix(0, 0).UTC()

// WritePackage renders files as the deterministic gzip tar the registry
// publishes: members sorted by path, regular files only, mode 0644, no owner
// names and a fixed time, written in USTAR so that every reader — the server's
// inspection and the Console's own tar reader alike — sees the same names. The
// result passes InspectPackage only if it carries atlas.json; WritePackage itself
// does not judge the content.
func WritePackage(files []SourceFile) ([]byte, error) {
	if len(files) == 0 || len(files) > maxPackageEntries {
		return nil, fmt.Errorf("a package carries 1..%d files", maxPackageEntries)
	}
	sorted := append([]SourceFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	seen := make(map[string]struct{}, len(sorted))
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, f := range sorted {
		if !validPackagePath(f.Path) {
			return nil, fmt.Errorf("unsafe package path %q", f.Path)
		}
		key := strings.ToLower(f.Path)
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("duplicate package path %q", f.Path)
		}
		seen[key] = struct{}{}
		hdr := &tar.Header{
			Typeflag: tar.TypeReg, Name: f.Path, Mode: 0o644, Size: int64(len(f.Data)),
			ModTime: packageEpoch, Format: tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("package member %q: %w", f.Path, err)
		}
		if _, err := tw.Write(f.Data); err != nil {
			return nil, fmt.Errorf("package member %q: %w", f.Path, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if int64(raw.Len()) > MaxPackageExpandedBytes {
		return nil, errors.New("package exceeds the expanded archive limit")
	}
	var out bytes.Buffer
	gz, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	// The gzip header carries no name and no time: Go writes a zero ModTime as
	// zero, so the header is constant.
	if _, err := io.Copy(gz, &raw); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	if out.Len() > MaxPackageBytes {
		return nil, errors.New("package exceeds the compressed archive limit")
	}
	return out.Bytes(), nil
}

// ArtifactOf pins archive bytes at a registry-relative path: their size and the
// SHA-256 the signed index will carry.
func ArtifactOf(path string, archive []byte) Artifact {
	sum := sha256.Sum256(archive)
	return Artifact{Path: path, Digest: "sha256:" + hex.EncodeToString(sum[:]), SizeBytes: int64(len(archive))}
}

// SignIndex renders an index as the exact bytes a registry serves and signs them.
// It refuses to sign what a server would refuse to read: the result is checked
// with VerifySignedIndex against the key's own public half before it is returned,
// so a publisher cannot release an index no installation accepts.
func SignIndex(index Index, key ed25519.PrivateKey) (raw, signature []byte, err error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, nil, errors.New("registry signing key must be an Ed25519 private key")
	}
	raw, err = json.MarshalIndent(index, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("render registry index: %w", err)
	}
	raw = append(raw, '\n')
	signature = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, raw)))
	if _, err := VerifySignedIndex(raw, signature, key.Public().(ed25519.PublicKey)); err != nil {
		return nil, nil, fmt.Errorf("the index would not verify: %w", err)
	}
	return raw, signature, nil
}

// ParseSigningKey reads a registry signing key as `atlas hub keygen` writes it:
// the base64 of the 32-byte Ed25519 seed, surrounding whitespace ignored.
func ParseSigningKey(text []byte) (ed25519.PrivateKey, error) {
	seed, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(text)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("registry signing key must be the base64 of a 32-byte Ed25519 seed")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// EncodeSigningKey is ParseSigningKey's inverse: the seed, base64.
func EncodeSigningKey(key ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(key.Seed())
}

// EncodePublicKey is a public key as ATLAS_BLUEPRINT_REGISTRY_KEY takes it.
func EncodePublicKey(key ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(key)
}

// KeyFingerprint names a public key the way an operator compares two: the
// SHA-256 of its 32 bytes, hex.
func KeyFingerprint(key ed25519.PublicKey) string {
	sum := sha256.Sum256(key)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// NextPatch is the release after version on the same minor line, its prerelease
// dropped: 1.0.0 → 1.0.1, 1.2.0-rc.1 → 1.2.1.
func NextPatch(version string) (string, error) {
	m := versionPattern.FindStringSubmatch(version)
	if m == nil {
		return "", fmt.Errorf("%q is not a semantic version", version)
	}
	patch := 0
	for _, c := range m[3] {
		if patch > 1<<30 {
			return "", fmt.Errorf("%q has an oversized patch number", version)
		}
		patch = patch*10 + int(c-'0')
	}
	return fmt.Sprintf("%s.%s.%d", m[1], m[2], patch+1), nil
}

// ValidVersion reports whether value is a semantic version the index accepts.
func ValidVersion(value string) bool { return validVersion(value) }

// ValidNamespace reports whether value is a registry, publisher or Blueprint id
// the index accepts.
func ValidNamespace(value string) bool { return validNamespace(value) }
