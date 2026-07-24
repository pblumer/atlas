package api

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestUser builds a minimal local user record for store tests.
func newTestUser(id, username string) User {
	return User{
		ID:        id,
		Username:  username,
		Email:     username + "@example.com",
		Roles:     []string{"user"},
		Source:    SourceLocal,
		CreatedAt: 100,
		UpdatedAt: 100,
	}
}

func TestUserStoreSaveGetRoundTrip(t *testing.T) {
	st, err := newUserStore(t.TempDir())
	if err != nil {
		t.Fatalf("newUserStore: %v", err)
	}
	u := newTestUser("usr_1", "alice")
	u.PasswordHash = "hash"
	if err := st.save(u); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := st.get("usr_1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Username != "alice" || got.Email != "alice@example.com" || got.PasswordHash != "hash" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if len(got.Roles) != 1 || got.Roles[0] != "user" {
		t.Fatalf("roles not preserved: %+v", got.Roles)
	}
}

func TestUserStoreGetMissing(t *testing.T) {
	st, _ := newUserStore(t.TempDir())
	_, ok, err := st.get("nope")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok {
		t.Fatalf("expected not found")
	}
}

func TestUserStoreByUsernameCaseInsensitive(t *testing.T) {
	st, _ := newUserStore(t.TempDir())
	if err := st.save(newTestUser("usr_1", "Alice")); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := st.byUsername("alice")
	if err != nil || !ok {
		t.Fatalf("byUsername: ok=%v err=%v", ok, err)
	}
	if got.ID != "usr_1" {
		t.Fatalf("wrong user: %+v", got)
	}
	if _, ok, _ := st.byUsername("bob"); ok {
		t.Fatalf("expected bob missing")
	}
}

func TestUserStoreByUsernameBlank(t *testing.T) {
	st, _ := newUserStore(t.TempDir())
	_ = st.save(newTestUser("usr_1", "alice"))
	if _, ok, err := st.byUsername("   "); ok || err != nil {
		t.Fatalf("a blank username must never match: ok=%v err=%v", ok, err)
	}
}

func TestUserStoreLoadAllIgnoresNonRecords(t *testing.T) {
	dir := t.TempDir()
	st, _ := newUserStore(dir)
	_ = st.save(newTestUser("usr_1", "alice"))
	// Files that are not user records must be skipped, not decoded.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "zz.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	all, err := st.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 1 || all[0].ID != "usr_1" {
		t.Fatalf("non-records not ignored: %+v", all)
	}
}

func TestUserStoreByEmailCaseInsensitive(t *testing.T) {
	st, _ := newUserStore(t.TempDir())
	_ = st.save(newTestUser("usr_1", "alice"))
	got, ok, err := st.byEmail("ALICE@example.com")
	if err != nil || !ok {
		t.Fatalf("byEmail: ok=%v err=%v", ok, err)
	}
	if got.ID != "usr_1" {
		t.Fatalf("wrong user: %+v", got)
	}
	if _, ok, _ := st.byEmail(""); ok {
		t.Fatalf("empty email must never match")
	}
}

func TestUserStoreDeleteIdempotent(t *testing.T) {
	st, _ := newUserStore(t.TempDir())
	_ = st.save(newTestUser("usr_1", "alice"))
	if err := st.delete("usr_1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := st.get("usr_1"); ok {
		t.Fatalf("expected gone")
	}
	// Deleting a missing user is not an error.
	if err := st.delete("usr_1"); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

func TestUserStoreLoadAllSortedAndCount(t *testing.T) {
	st, _ := newUserStore(t.TempDir())
	a := newTestUser("usr_a", "alice")
	a.CreatedAt = 100
	b := newTestUser("usr_b", "bob")
	b.CreatedAt = 200
	_ = st.save(a)
	_ = st.save(b)
	n, err := st.count()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	all, err := st.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 2 || all[0].ID != "usr_a" || all[1].ID != "usr_b" {
		t.Fatalf("loadAll order (want by createdAt asc): %+v", all)
	}
}

func TestUserStoreSaveOverwrites(t *testing.T) {
	st, _ := newUserStore(t.TempDir())
	_ = st.save(newTestUser("usr_1", "alice"))
	u := newTestUser("usr_1", "alice")
	u.DisplayName = "Alice A."
	u.Disabled = true
	if err := st.save(u); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _, _ := st.get("usr_1")
	if got.DisplayName != "Alice A." || !got.Disabled {
		t.Fatalf("overwrite failed: %+v", got)
	}
	if n, _ := st.count(); n != 1 {
		t.Fatalf("overwrite duplicated record: count=%d", n)
	}
}
