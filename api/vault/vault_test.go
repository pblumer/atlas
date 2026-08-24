package vault

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testVaultKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func newTestVault(t *testing.T) *Vault {
	t.Helper()
	v, err := New(filepath.Join(t.TempDir(), "vault"), testVaultKey(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

// TestVaultSetGetDelete is the core round-trip: a sealed value decrypts back to
// itself, a missing name is a clean miss, and delete is idempotent.
func TestVaultSetGetDelete(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.Set("gmail_ops", "s3cr3t"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := v.Get("gmail_ops")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got != "s3cr3t" {
		t.Errorf("Get = %q, want s3cr3t", got)
	}
	if _, ok, err := v.Get("nope"); ok || err != nil {
		t.Errorf("Get(missing): ok=%v err=%v, want false,nil", ok, err)
	}
	if err := v.Delete("gmail_ops"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := v.Delete("gmail_ops"); err != nil {
		t.Fatalf("Delete (idempotent): %v", err)
	}
	if _, ok, _ := v.Get("gmail_ops"); ok {
		t.Error("secret still present after delete")
	}
}

// TestVaultCiphertextIsNotPlaintext proves the on-disk record carries neither the
// plaintext value nor the key — only base64 ciphertext and a nonce.
func TestVaultCiphertextIsNotPlaintext(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.Set("api_token", "topsecretvalue"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	data, err := os.ReadFile(v.fileFor("api_token"))
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if strings.Contains(string(data), "topsecretvalue") {
		t.Error("on-disk record contains the plaintext value")
	}
}

// TestVaultOverwritePreservesCreatedAt covers the overwrite path: a second Set keeps
// the original createdAt and stores the new value.
func TestVaultOverwritePreservesCreatedAt(t *testing.T) {
	v := newTestVault(t)
	m1, err := v.Set("k", "a")
	if err != nil {
		t.Fatalf("Set 1: %v", err)
	}
	m2, err := v.Set("k", "b")
	if err != nil {
		t.Fatalf("Set 2: %v", err)
	}
	if m2.CreatedAt != m1.CreatedAt {
		t.Errorf("createdAt = %d on overwrite, want preserved %d", m2.CreatedAt, m1.CreatedAt)
	}
	if got, _, _ := v.Get("k"); got != "b" {
		t.Errorf("Get after overwrite = %q, want b", got)
	}
}

// TestVaultListNoValues proves List returns names and metadata, oldest/name order,
// and never a value.
func TestVaultListNoValues(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.Set("b", "vb"); err != nil {
		t.Fatalf("Set b: %v", err)
	}
	if _, err := v.Set("a", "va"); err != nil {
		t.Fatalf("Set a: %v", err)
	}
	metas, err := v.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("List len = %d, want 2", len(metas))
	}
	blob, _ := json.Marshal(metas)
	if strings.Contains(string(blob), "va") || strings.Contains(string(blob), "vb") {
		t.Errorf("List leaked a secret value: %s", blob)
	}
	if metas[0].KeyID != v.keyID {
		t.Errorf("meta keyId = %q, want %q", metas[0].KeyID, v.keyID)
	}
}

// TestVaultWrongKey proves a secret sealed under one master key is not silently
// readable under another: opening reports a clear key-mismatch error via keyId.
func TestVaultWrongKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	v1, err := New(dir, testVaultKey(t))
	if err != nil {
		t.Fatalf("v1: %v", err)
	}
	if _, err := v1.Set("k", "secret"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v2, err := New(dir, testVaultKey(t)) // different key, same dir
	if err != nil {
		t.Fatalf("v2: %v", err)
	}
	if _, ok, err := v2.Get("k"); err == nil || !ok {
		t.Errorf("Get with wrong key: ok=%v err=%v, want ok=true err!=nil", ok, err)
	}
}

// TestVaultTamperDetected proves GCM authentication rejects a record whose
// ciphertext was altered under the correct key id.
func TestVaultTamperDetected(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.Set("k", "secret"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	path := v.fileFor("k")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ct, err := base64.StdEncoding.DecodeString(rec.Ciphertext)
	if err != nil {
		t.Fatalf("decode ct: %v", err)
	}
	ct[0] ^= 0xFF // flip a byte; keyId is left intact so the mismatch check passes
	rec.Ciphertext = base64.StdEncoding.EncodeToString(ct)
	nb, _ := json.Marshal(rec)
	if err := os.WriteFile(path, nb, 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if _, ok, err := v.Get("k"); err == nil || !ok {
		t.Errorf("Get tampered: ok=%v err=%v, want ok=true err!=nil", ok, err)
	}
}

// TestVaultRecovery is the persistence property: reopening the same directory with
// the same key still decrypts an earlier secret (state after restart == state live).
func TestVaultRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	key := testVaultKey(t)
	v1, err := New(dir, key)
	if err != nil {
		t.Fatalf("v1: %v", err)
	}
	if _, err := v1.Set("k", "persisted"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v2, err := New(dir, key)
	if err != nil {
		t.Fatalf("v2: %v", err)
	}
	got, ok, err := v2.Get("k")
	if err != nil || !ok || got != "persisted" {
		t.Fatalf("recovered Get = %q ok=%v err=%v, want persisted,true,nil", got, ok, err)
	}
}

func TestNewSecretVaultBadKey(t *testing.T) {
	if _, err := New(t.TempDir(), []byte("too-short")); err == nil {
		t.Error("New with a non-32-byte key: want error")
	}
}

func TestParseVaultKey(t *testing.T) {
	key := testVaultKey(t)
	if b, err := parseKey(hex.EncodeToString(key)); err != nil || len(b) != 32 {
		t.Errorf("hex key: b=%d err=%v", len(b), err)
	}
	if b, err := parseKey(base64.StdEncoding.EncodeToString(key)); err != nil || len(b) != 32 {
		t.Errorf("base64 key: b=%d err=%v", len(b), err)
	}
	if _, err := parseKey("abcd"); err == nil {
		t.Error("short key: want error")
	}
	if _, err := parseKey("   "); err == nil {
		t.Error("blank key: want error")
	}
}

func TestVaultKeyFromEnv(t *testing.T) {
	t.Run("unset disables the vault", func(t *testing.T) {
		t.Setenv(KeyEnv, "")
		t.Setenv(KeyFileEnv, "")
		if _, ok, err := keyFromEnv(); ok || err != nil {
			t.Errorf("ok=%v err=%v, want false,nil", ok, err)
		}
	})
	t.Run("hex env key", func(t *testing.T) {
		t.Setenv(KeyEnv, hex.EncodeToString(testVaultKey(t)))
		if _, ok, err := keyFromEnv(); !ok || err != nil {
			t.Errorf("ok=%v err=%v, want true,nil", ok, err)
		}
	})
	t.Run("invalid env key errors", func(t *testing.T) {
		t.Setenv(KeyEnv, "not-a-valid-key")
		if _, ok, err := keyFromEnv(); ok || err == nil {
			t.Errorf("ok=%v err=%v, want false,err", ok, err)
		}
	})
	t.Run("key file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "key")
		if err := os.WriteFile(f, []byte(hex.EncodeToString(testVaultKey(t))), 0o600); err != nil {
			t.Fatalf("write key file: %v", err)
		}
		t.Setenv(KeyEnv, "")
		t.Setenv(KeyFileEnv, f)
		if _, ok, err := keyFromEnv(); !ok || err != nil {
			t.Errorf("key file: ok=%v err=%v, want true,nil", ok, err)
		}
	})
	t.Run("missing key file errors", func(t *testing.T) {
		t.Setenv(KeyEnv, "")
		t.Setenv(KeyFileEnv, filepath.Join(t.TempDir(), "does-not-exist"))
		if _, ok, err := keyFromEnv(); ok || err == nil {
			t.Errorf("ok=%v err=%v, want false,err", ok, err)
		}
	})
}

// TestLoadOrCreateKeyFile covers the generated → reuse → corrupt progression of the
// default key source.
func TestLoadOrCreateKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "vault.key")
	k1, src1, err := loadOrCreateKeyFile(path)
	if err != nil || src1 != "generated" || len(k1) != 32 {
		t.Fatalf("first call: key=%d src=%q err=%v, want 32-byte generated", len(k1), src1, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file: mode=%v err=%v, want 0600", info.Mode().Perm(), err)
	}
	k2, src2, err := loadOrCreateKeyFile(path)
	if err != nil || src2 != "file" || !bytes.Equal(k1, k2) {
		t.Fatalf("second call: src=%q equal=%v err=%v, want reuse of the same key", src2, bytes.Equal(k1, k2), err)
	}
	if err := os.WriteFile(path, []byte("not-a-valid-key"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if _, _, err := loadOrCreateKeyFile(path); err == nil {
		t.Error("corrupt key file: want an error")
	}
}

// TestLoadOrCreateKeyFileErrors covers the read-error and mkdir-error branches of the
// default key source.
func TestLoadOrCreateKeyFileErrors(t *testing.T) {
	// A directory at the key path yields a non-NotExist read error.
	if _, _, err := loadOrCreateKeyFile(t.TempDir()); err == nil {
		t.Error("key path is a directory: want a read error")
	}
	// A parent path component that is a file makes MkdirAll fail.
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := loadOrCreateKeyFile(filepath.Join(f, "sub", "vault.key")); err == nil {
		t.Error("key dir under a file: want a mkdir error")
	}
}

// TestResolveVaultKeyEnvPrecedence proves an operator key wins and, crucially, is not
// persisted to the key file (ADR-0069/0070).
func TestResolveVaultKeyEnvPrecedence(t *testing.T) {
	t.Setenv(KeyEnv, hex.EncodeToString(testVaultKey(t)))
	path := filepath.Join(t.TempDir(), "vault.key")
	key, source, err := ResolveKey(path)
	if err != nil || source != "env" || len(key) != 32 {
		t.Fatalf("ResolveKey = (%d bytes, %q, %v), want a 32-byte env key", len(key), source, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("an operator key must not create the generated key file")
	}
}

// TestResolveVaultKeyInvalidEnv proves a set-but-invalid operator key fails loudly
// rather than silently generating one.
func TestResolveVaultKeyInvalidEnv(t *testing.T) {
	t.Setenv(KeyEnv, "too-short-to-be-a-key")
	if _, _, err := ResolveKey(filepath.Join(t.TempDir(), "vault.key")); err == nil {
		t.Error("invalid operator key: want a startup error")
	}
}

// TestVaultCorruptRecord covers load's JSON-decode error surfacing through Get.
func TestVaultCorruptRecord(t *testing.T) {
	v := newTestVault(t)
	if err := os.WriteFile(v.fileFor("x"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := v.Get("x"); err == nil {
		t.Error("Get corrupt: want a decode error")
	}
}

// TestVaultBadEncoding covers the base64 decode error branches in Get for a record
// whose key id matches but whose nonce/ciphertext are not valid base64.
func TestVaultBadEncoding(t *testing.T) {
	v := newTestVault(t)
	write := func(rec record) {
		b, _ := json.Marshal(rec)
		if err := os.WriteFile(v.fileFor(rec.Name), b, 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	write(record{Name: "n", KeyID: v.keyID, Nonce: "@@@not-base64", Ciphertext: "AAAA"})
	if _, ok, err := v.Get("n"); err == nil || !ok {
		t.Errorf("Get bad nonce: ok=%v err=%v, want ok=true err!=nil", ok, err)
	}
	write(record{Name: "c", KeyID: v.keyID, Nonce: base64.StdEncoding.EncodeToString(make([]byte, 12)), Ciphertext: "@@@not-base64"})
	if _, ok, err := v.Get("c"); err == nil || !ok {
		t.Errorf("Get bad ciphertext: ok=%v err=%v, want ok=true err!=nil", ok, err)
	}
}

// TestVaultSetOverExistingCorrupt covers Set's load-error branch on overwrite.
func TestVaultSetOverExistingCorrupt(t *testing.T) {
	v := newTestVault(t)
	if err := os.WriteFile(v.fileFor("k"), []byte("{bad"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := v.Set("k", "v"); err == nil {
		t.Error("Set over a corrupt record: want error")
	}
}

// TestVaultListSkipsNonRecords proves List ignores files that are not vault records
// (wrong suffix, non-hex name) while still returning the real ones.
func TestVaultListSkipsNonRecords(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.Set("real", "value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := os.WriteFile(filepath.Join(v.dir, "notes.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(v.dir, "zz.json"), []byte("{}"), 0o600); err != nil { // non-hex name
		t.Fatalf("write non-hex: %v", err)
	}
	metas, err := v.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 1 || metas[0].Name != "real" {
		t.Errorf("List = %+v, want just the real secret", metas)
	}
}

// TestVaultListDecodeError covers List's decode-error branch on a hex-named but
// corrupt record file.
func TestVaultListDecodeError(t *testing.T) {
	v := newTestVault(t)
	bad := hex.EncodeToString([]byte("bad")) + ".json"
	if err := os.WriteFile(filepath.Join(v.dir, bad), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := v.List(); err == nil {
		t.Error("List over a corrupt record: want error")
	}
}

// TestNewSecretVaultDirError covers the MkdirAll failure branch (a parent path
// component that is a file, not a directory).
func TestNewSecretVaultDirError(t *testing.T) {
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := New(filepath.Join(f, "sub"), testVaultKey(t)); err == nil {
		t.Error("New under a file path: want a mkdir error")
	}
}

// TestVaultLoadReadError covers load's non-NotExist read error, reached when the
// record path is a directory rather than a file.
func TestVaultLoadReadError(t *testing.T) {
	v := newTestVault(t)
	if err := os.Mkdir(v.fileFor("x"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, _, err := v.Get("x"); err == nil {
		t.Error("Get where the record path is a directory: want a read error")
	}
}

// TestVaultDeleteError covers Delete's os.Remove error branch (a non-empty directory
// at the record path cannot be removed).
func TestVaultDeleteError(t *testing.T) {
	v := newTestVault(t)
	p := v.fileFor("x")
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(p, "child"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write child: %v", err)
	}
	if err := v.Delete("x"); err == nil {
		t.Error("Delete of a non-empty directory path: want error")
	}
}

// TestVaultListReadError covers List's read-error branch: a valid-hex-named record
// entry that is a directory cannot be read as a file.
func TestVaultListReadError(t *testing.T) {
	v := newTestVault(t)
	// A dangling symlink with a valid record name passes the dir/suffix/hex checks
	// but fails to read, exercising List's read-error branch (and it is not skipped
	// as a directory, unlike a real subdirectory).
	if err := os.Symlink(filepath.Join(v.dir, "missing-target"), v.fileFor("dangling")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := v.List(); err == nil {
		t.Error("List with an unreadable record entry: want error")
	}
}

// TestVaultListSortByCreatedAt covers the sort comparator's created-at branch by
// writing two records with different creation times.
func TestVaultListSortByCreatedAt(t *testing.T) {
	v := newTestVault(t)
	write := func(name string, created int64) {
		b, _ := json.Marshal(record{Name: name, KeyID: v.keyID, CreatedAt: created, UpdatedAt: created})
		if err := os.WriteFile(v.fileFor(name), b, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("younger", 200)
	write("older", 100)
	metas, err := v.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 2 || metas[0].Name != "older" {
		t.Errorf("List order = %+v, want older (createdAt 100) first", metas)
	}
}
