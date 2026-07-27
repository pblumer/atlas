package api

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
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Environment variables that configure the vault master key (ADR-0069). The key is
// 32 bytes (AES-256), given as hex (64 chars) or base64. It is read once at startup,
// held only in memory, and NEVER written to the data directory — the engine
// custodies ciphertext at rest but not the key that protects it.
const (
	vaultKeyEnv     = "ATLAS_VAULT_KEY"
	vaultKeyFileEnv = "ATLAS_VAULT_KEY_FILE"
)

// secretRecord is the on-disk form of one sealed secret. It holds a nonce and
// ciphertext, never plaintext and never the key. keyId is a non-secret fingerprint
// of the master key, so a wrong or rotated key is reported as a clear error rather
// than a raw GCM auth failure (ADR-0069). The value is bound to its Name as the
// AEAD's additional data, so a record cannot be moved to another name and still open.
type secretRecord struct {
	Name       string `json:"name"`
	KeyID      string `json:"keyId"`
	Nonce      string `json:"nonce"`      // base64
	Ciphertext string `json:"ciphertext"` // base64
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// secretMeta is the value-free view of a secret returned by the API and List: it
// proves a secret exists and under which key, but never reveals the value.
type secretMeta struct {
	Name      string `json:"name"`
	KeyID     string `json:"keyId"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// secretVault is the engine-internal encrypted secret store (ADR-0069, closing the
// A3 option deferred by ADR-0041). It owns a sidecar directory — one JSON file per
// secret, hex-named, atomic write + dir fsync, the connectorStore pattern — and an
// AES-256-GCM cipher built from the master key. Like the other sidecar stores it is
// owned by the run-loop goroutine, so it needs no locking; and it writes only
// ciphertext, so no secret value ever reaches the WAL, an event, or a variable (I6).
type secretVault struct {
	dir   string
	aead  cipher.AEAD
	keyID string
}

// newSecretVault opens (creating if needed) the vault directory and builds the
// AES-256-GCM cipher from a 32-byte master key.
func newSecretVault(dir string, key []byte) (*secretVault, error) {
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
	return &secretVault{dir: dir, aead: aead, keyID: hex.EncodeToString(sum[:8])}, nil
}

func (v *secretVault) fileFor(name string) string {
	return filepath.Join(v.dir, hex.EncodeToString([]byte(name))+".json")
}

// Set seals value under name and stores it durably, returning the value-free
// metadata. The plaintext is sealed at once and never persisted; an overwrite keeps
// the original createdAt.
func (v *secretVault) Set(name, value string) (secretMeta, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return secretMeta{}, fmt.Errorf("vault: nonce: %w", err)
	}
	ct := v.aead.Seal(nil, nonce, []byte(value), []byte(name))
	now := time.Now().Unix()
	created := now
	if existing, ok, err := v.load(name); err != nil {
		return secretMeta{}, err
	} else if ok {
		created = existing.CreatedAt
	}
	rec := secretRecord{
		Name:       name,
		KeyID:      v.keyID,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
		CreatedAt:  created,
		UpdatedAt:  now,
	}
	if err := atomicWriteJSON(v.dir, v.fileFor(name), rec); err != nil {
		return secretMeta{}, err
	}
	return secretMeta{Name: name, KeyID: v.keyID, CreatedAt: created, UpdatedAt: now}, nil
}

// load reads the raw record for a name (ok=false when there is none).
func (v *secretVault) load(name string) (secretRecord, bool, error) {
	data, err := os.ReadFile(v.fileFor(name))
	if err != nil {
		if os.IsNotExist(err) {
			return secretRecord{}, false, nil
		}
		return secretRecord{}, false, fmt.Errorf("vault: read: %w", err)
	}
	var rec secretRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return secretRecord{}, false, fmt.Errorf("vault: decode: %w", err)
	}
	return rec, true, nil
}

// Get returns the decrypted value for a name, ok=false when no such secret exists.
// A record sealed under a different master key, or one that has been tampered with,
// is an error rather than a silent miss.
func (v *secretVault) Get(name string) (string, bool, error) {
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
func (v *secretVault) Delete(name string) error {
	if err := os.Remove(v.fileFor(name)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("vault: remove: %w", err)
	}
	return fsyncDir(v.dir)
}

// List returns value-free metadata for every stored secret, oldest first. Non-record
// files are ignored.
func (v *secretVault) List() ([]secretMeta, error) {
	entries, err := os.ReadDir(v.dir)
	if err != nil {
		return nil, fmt.Errorf("vault: read dir: %w", err)
	}
	var out []secretMeta
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
		var rec secretRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("vault: decode %s: %w", e.Name(), err)
		}
		out = append(out, secretMeta{Name: rec.Name, KeyID: rec.KeyID, CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// parseVaultKey decodes a master key from hex (64 chars) or base64; it must be
// exactly 32 bytes (AES-256).
func parseVaultKey(raw string) ([]byte, error) {
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

// vaultKeyFromEnv sources the master key from ATLAS_VAULT_KEY, or from the file at
// ATLAS_VAULT_KEY_FILE. It returns ok=false when neither is set — the vault stays
// disabled and A2 (environment references) remains the default. A key that is set
// but invalid is an error, so a misconfiguration fails loudly at startup rather than
// silently leaving the vault off.
func vaultKeyFromEnv() ([]byte, bool, error) {
	raw := strings.TrimSpace(os.Getenv(vaultKeyEnv))
	if raw == "" {
		if path := strings.TrimSpace(os.Getenv(vaultKeyFileEnv)); path != "" {
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
	key, err := parseVaultKey(raw)
	if err != nil {
		return nil, false, err
	}
	return key, true, nil
}

// handleListSecrets lists secret names and metadata in the vault — never values
// (ADR-0069). Admin-guarded like the other operator surfaces.
func (s *Server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.vault == nil {
		writeError(w, http.StatusServiceUnavailable, "vault not configured")
		return
	}
	var (
		metas   []secretMeta
		loadErr error
	)
	s.do(func() { metas, loadErr = s.vault.List() })
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list secrets: "+loadErr.Error())
		return
	}
	if metas == nil {
		metas = []secretMeta{}
	}
	writeJSON(w, http.StatusOK, metas)
}

// handleSetSecret seals a secret value under the {name} in the path. The plaintext
// arrives in the request body, is sealed immediately, and is never persisted in the
// clear, logged, or echoed back — the response carries only value-free metadata.
func (s *Server) handleSetSecret(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.vault == nil {
		writeError(w, http.StatusServiceUnavailable, "vault not configured")
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "secret name is required")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var p struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if p.Value == "" {
		writeError(w, http.StatusBadRequest, "secret value is required")
		return
	}
	var (
		meta    secretMeta
		saveErr error
	)
	s.do(func() { meta, saveErr = s.vault.Set(name, p.Value) })
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, "set secret: "+saveErr.Error())
		return
	}
	if meta.Name == "" {
		writeError(w, http.StatusServiceUnavailable, "server unavailable")
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

// handleDeleteSecret removes a secret from the vault. A connector that referenced it
// resolves to no token again (and parks) until reconfigured.
func (s *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.vault == nil {
		writeError(w, http.StatusServiceUnavailable, "vault not configured")
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "secret name is required")
		return
	}
	var delErr error
	s.do(func() { delErr = s.vault.Delete(name) })
	if delErr != nil {
		writeError(w, http.StatusInternalServerError, "delete secret: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
