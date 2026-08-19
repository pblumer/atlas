// Package vault is Atlas's engine-internal encrypted secret store: connector
// credentials sealed at rest with AES-256-GCM under a master key that never
// leaves the operator's control (ADR-0069), on by default with a generated key
// file when no operator key is supplied (ADR-0070).
//
// It exists as its own package so the boundary is enforced rather than merely
// intended: nothing here returns a plaintext secret except [Vault.Get], nothing
// writes the master key anywhere but the key file, and a caller cannot reach
// past the API into a record. The rest of the server holds a *Vault and resolves
// secret *references*, so a credential never sits in a compiled process, an
// event, or a log line.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/sidecar"
)

// Environment variables that configure the vault master key. The key is 32 bytes
// (AES-256), given as hex (64 chars) or base64. An operator key from either of these
// is preferred and, per ADR-0069, is NEVER written to disk by Atlas. When neither is
// set the vault is on by default and generates its own key file (ADR-0070).
const (
	KeyEnv     = "ATLAS_VAULT_KEY"
	KeyFileEnv = "ATLAS_VAULT_KEY_FILE"
)

// record is the on-disk form of one sealed secret. It holds a nonce and
// ciphertext, never plaintext and never the key. keyId is a non-secret fingerprint
// of the master key, so a wrong or rotated key is reported as a clear error rather
// than a raw GCM auth failure (ADR-0069). The value is bound to its Name as the
// AEAD's additional data, so a record cannot be moved to another name and still open.
type record struct {
	Name       string `json:"name"`
	KeyID      string `json:"keyId"`
	Nonce      string `json:"nonce"`      // base64
	Ciphertext string `json:"ciphertext"` // base64
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// Meta is the value-free view of a secret returned by the API and List: it
// proves a secret exists and under which key, but never reveals the value.
type Meta struct {
	Name      string `json:"name"`
	KeyID     string `json:"keyId"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// Vault is the engine-internal encrypted secret store (ADR-0069, closing the
// A3 option deferred by ADR-0041). It owns a sidecar directory — one JSON file per
// secret, hex-named, atomic write + dir fsync, the connectorStore pattern — and an
// AES-256-GCM cipher built from the master key. Like the other sidecar stores it is
// owned by the run-loop goroutine, so it needs no locking; and it writes only
// ciphertext, so no secret value ever reaches the WAL, an event, or a variable (I6).
type Vault struct {
	dir   string
	aead  cipher.AEAD
	keyID string
}

// New opens (creating if needed) the vault directory and builds the
// AES-256-GCM cipher from a 32-byte master key.
func New(dir string, key []byte) (*Vault, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("vault: master key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("vault: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("vault: new gcm: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("vault: create dir: %w", err)
	}
	sum := sha256.Sum256(key)
	return &Vault{dir: dir, aead: aead, keyID: hex.EncodeToString(sum[:8])}, nil
}

func (v *Vault) fileFor(name string) string {
	return filepath.Join(v.dir, hex.EncodeToString([]byte(name))+".json")
}

// Set seals value under name and stores it durably, returning the value-free
// metadata. The plaintext is sealed at once and never persisted; an overwrite keeps
// the original createdAt.
func (v *Vault) Set(name, value string) (Meta, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Meta{}, fmt.Errorf("vault: nonce: %w", err)
	}
	ct := v.aead.Seal(nil, nonce, []byte(value), []byte(name))
	now := time.Now().Unix()
	created := now
	if existing, ok, err := v.load(name); err != nil {
		return Meta{}, err
	} else if ok {
		created = existing.CreatedAt
	}
	rec := record{
		Name:       name,
		KeyID:      v.keyID,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
		CreatedAt:  created,
		UpdatedAt:  now,
	}
	if err := sidecar.WriteJSON(v.dir, v.fileFor(name), rec); err != nil {
		return Meta{}, err
	}
	return Meta{Name: name, KeyID: v.keyID, CreatedAt: created, UpdatedAt: now}, nil
}

// load reads the raw record for a name (ok=false when there is none).
func (v *Vault) load(name string) (record, bool, error) {
	data, err := os.ReadFile(v.fileFor(name))
	if err != nil {
		if os.IsNotExist(err) {
			return record{}, false, nil
		}
		return record{}, false, fmt.Errorf("vault: read: %w", err)
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		return record{}, false, fmt.Errorf("vault: decode: %w", err)
	}
	return rec, true, nil
}

// Get returns the decrypted value for a name, ok=false when no such secret exists.
// A record sealed under a different master key, or one that has been tampered with,
// is an error rather than a silent miss.
func (v *Vault) Get(name string) (string, bool, error) {
	rec, ok, err := v.load(name)
	if err != nil || !ok {
		return "", ok, err
	}
	if rec.KeyID != v.keyID {
		return "", true, fmt.Errorf("vault: secret %q was sealed under a different master key (%s)", name, rec.KeyID)
	}
	nonce, err := base64.StdEncoding.DecodeString(rec.Nonce)
	if err != nil {
		return "", true, fmt.Errorf("vault: decode nonce for %q: %w", name, err)
	}
	ct, err := base64.StdEncoding.DecodeString(rec.Ciphertext)
	if err != nil {
		return "", true, fmt.Errorf("vault: decode ciphertext for %q: %w", name, err)
	}
	plain, err := v.aead.Open(nil, nonce, ct, []byte(name))
	if err != nil {
		return "", true, fmt.Errorf("vault: decrypt %q: %w", name, err)
	}
	return string(plain), true, nil
}

// Delete removes a secret. A missing secret is not an error (idempotent).
func (v *Vault) Delete(name string) error {
	if err := os.Remove(v.fileFor(name)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("vault: remove: %w", err)
	}
	return sidecar.FsyncDir(v.dir)
}

// List returns value-free metadata for every stored secret, oldest first. Non-record
// files are ignored.
func (v *Vault) List() ([]Meta, error) {
	entries, err := os.ReadDir(v.dir)
	if err != nil {
		return nil, fmt.Errorf("vault: read dir: %w", err)
	}
	var out []Meta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(e.Name(), ".json")); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(v.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("vault: read %s: %w", e.Name(), err)
		}
		var rec record
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("vault: decode %s: %w", e.Name(), err)
		}
		out = append(out, Meta{Name: rec.Name, KeyID: rec.KeyID, CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// parseKey decodes a master key from hex (64 chars) or base64; it must be
// exactly 32 bytes (AES-256).
func parseKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("vault: empty master key")
	}
	if b, err := hex.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, fmt.Errorf("vault: master key must be 32 bytes as hex (64 chars) or base64")
}

// ResolveKey sources the master key with operator precedence (ADR-0070): an
// operator key from the environment (ATLAS_VAULT_KEY / ATLAS_VAULT_KEY_FILE) wins and
// is never written to disk; absent one, the key is loaded from keyFile, or generated
// into it (mode 0600) so the vault is on by default without provisioning. source is
// "env", "file", or "generated" — the caller logs the generated case, which trades a
// weaker at-rest guarantee (key beside the ciphertext) for turnkey operation.
func ResolveKey(keyFile string) (key []byte, source string, err error) {
	k, ok, err := keyFromEnv()
	if err != nil {
		return nil, "", err
	}
	if ok {
		return k, "env", nil
	}
	return loadOrCreateKeyFile(keyFile)
}

// loadOrCreateKeyFile returns the master key stored at path, generating and persisting
// a fresh 32-byte key (0600, parent 0700, fsynced) when the file does not yet exist.
// It is the default key source when no operator key is set (ADR-0070).
func loadOrCreateKeyFile(path string) ([]byte, string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		key, perr := parseKey(strings.TrimSpace(string(data)))
		if perr != nil {
			return nil, "", fmt.Errorf("vault: key file %s: %w", path, perr)
		}
		return key, "file", nil
	}
	if !os.IsNotExist(err) {
		return nil, "", fmt.Errorf("vault: read key file: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, "", fmt.Errorf("vault: generate key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, "", fmt.Errorf("vault: create key dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return nil, "", fmt.Errorf("vault: write key file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil { // ensure 0600 even if the file pre-existed under a umask
		return nil, "", fmt.Errorf("vault: chmod key file: %w", err)
	}
	if err := sidecar.FsyncDir(filepath.Dir(path)); err != nil {
		return nil, "", err
	}
	return key, "generated", nil
}

// keyFromEnv sources the master key from ATLAS_VAULT_KEY, or from the file at
// ATLAS_VAULT_KEY_FILE. It returns ok=false when neither is set, so the caller falls
// back to the generated key file (ADR-0070). A key that is set but invalid is an
// error, so a misconfiguration fails loudly at startup.
func keyFromEnv() ([]byte, bool, error) {
	raw := strings.TrimSpace(os.Getenv(KeyEnv))
	if raw == "" {
		if path := strings.TrimSpace(os.Getenv(KeyFileEnv)); path != "" {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, false, fmt.Errorf("vault: read key file: %w", err)
			}
			raw = strings.TrimSpace(string(data))
		}
	}
	if raw == "" {
		return nil, false, nil
	}
	key, err := parseKey(raw)
	if err != nil {
		return nil, false, err
	}
	return key, true, nil
}
