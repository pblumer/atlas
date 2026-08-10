package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedUser writes a user directly into the on-disk store the reset command
// operates on, so the tests exercise the same files an operator would touch.
func seedUser(t *testing.T, dataDir string, u User) {
	t.Helper()
	st, err := newUserStore(filepath.Join(dataDir, "users"))
	if err != nil {
		t.Fatalf("newUserStore: %v", err)
	}
	if err := st.save(u); err != nil {
		t.Fatalf("save: %v", err)
	}
}

// loadUser reads a user back from the store by username for assertions.
func loadUser(t *testing.T, dataDir, username string) (User, bool) {
	t.Helper()
	st, err := newUserStore(filepath.Join(dataDir, "users"))
	if err != nil {
		t.Fatalf("newUserStore: %v", err)
	}
	u, ok, err := st.byUsername(username)
	if err != nil {
		t.Fatalf("byUsername: %v", err)
	}
	return u, ok
}

func TestResetPasswordExistingUser(t *testing.T) {
	dir := t.TempDir()
	seedUser(t, dir, User{
		ID:           "usr_abc",
		Username:     "Patrick",
		Roles:        []string{RoleAdmin},
		Source:       SourceLocal,
		PasswordHash: "$2a$10$oldhasholdhasholdhasholdhasholdhasholdhasholdhasholdhas",
		Disabled:     false,
		CreatedAt:    100,
		UpdatedAt:    100,
	})

	res, err := ResetPassword(ResetPasswordOptions{
		DataDir:     dir,
		Username:    "patrick", // case-insensitive match
		NewPassword: "s3cret-pw",
		Now:         200,
	})
	if err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if res.Created {
		t.Errorf("Created = true, want false for an existing user")
	}
	if res.UserID != "usr_abc" || res.Username != "Patrick" {
		t.Errorf("result = %+v, want id usr_abc / username Patrick", res)
	}

	u, ok := loadUser(t, dir, "patrick")
	if !ok {
		t.Fatal("user vanished after reset")
	}
	if !checkPassword(u.PasswordHash, "s3cret-pw") {
		t.Error("new password does not verify against stored hash")
	}
	if u.UpdatedAt != 200 {
		t.Errorf("UpdatedAt = %d, want 200", u.UpdatedAt)
	}
	if u.CreatedAt != 100 {
		t.Errorf("CreatedAt = %d, want 100 (unchanged)", u.CreatedAt)
	}
	if len(u.Roles) != 1 || u.Roles[0] != RoleAdmin {
		t.Errorf("Roles = %v, want [admin] preserved", u.Roles)
	}
}

func TestResetPasswordUserNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := ResetPassword(ResetPasswordOptions{
		DataDir:     dir,
		Username:    "ghost",
		NewPassword: "s3cret-pw",
		Now:         200,
	})
	if err == nil {
		t.Fatal("expected an error for an unknown user without --create-admin")
	}
}

func TestResetPasswordCreateAdmin(t *testing.T) {
	dir := t.TempDir()
	res, err := ResetPassword(ResetPasswordOptions{
		DataDir:     dir,
		Username:    "patrick",
		NewPassword: "s3cret-pw",
		CreateAdmin: true,
		Now:         200,
	})
	if err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if !res.Created {
		t.Error("Created = false, want true when creating a fresh admin")
	}
	if res.UserID == "" {
		t.Error("created user has no id")
	}
	u, ok := loadUser(t, dir, "patrick")
	if !ok {
		t.Fatal("created user not found in store")
	}
	if !u.hasRole(RoleAdmin) {
		t.Errorf("created user roles = %v, want admin", u.Roles)
	}
	if u.Source != SourceLocal {
		t.Errorf("created user source = %q, want %q", u.Source, SourceLocal)
	}
	if u.Disabled {
		t.Error("created admin is disabled; want enabled so it can sign in")
	}
	if !checkPassword(u.PasswordHash, "s3cret-pw") {
		t.Error("created admin password does not verify")
	}
	if u.CreatedAt != 200 || u.UpdatedAt != 200 {
		t.Errorf("timestamps = (%d,%d), want (200,200)", u.CreatedAt, u.UpdatedAt)
	}
}

func TestResetPasswordCreateAdminDoesNotDuplicateExisting(t *testing.T) {
	dir := t.TempDir()
	seedUser(t, dir, User{
		ID: "usr_abc", Username: "patrick", Roles: []string{RoleUser},
		Source: SourceLocal, CreatedAt: 100, UpdatedAt: 100,
	})
	// --create-admin on an existing user must update it in place, not create a
	// second account (and must not silently grant admin).
	res, err := ResetPassword(ResetPasswordOptions{
		DataDir: dir, Username: "patrick", NewPassword: "s3cret-pw",
		CreateAdmin: true, Now: 200,
	})
	if err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if res.Created {
		t.Error("Created = true, want false: the user already existed")
	}
	if res.UserID != "usr_abc" {
		t.Errorf("UserID = %q, want usr_abc (existing record reused)", res.UserID)
	}
}

func TestResetPasswordTooShort(t *testing.T) {
	dir := t.TempDir()
	seedUser(t, dir, User{ID: "usr_abc", Username: "patrick", Source: SourceLocal})
	_, err := ResetPassword(ResetPasswordOptions{
		DataDir: dir, Username: "patrick", NewPassword: "123", Now: 200,
	})
	if err == nil {
		t.Fatalf("expected an error for a password shorter than %d characters", minPasswordLen)
	}
	// The stored hash must be untouched when the new password is rejected.
	u, _ := loadUser(t, dir, "patrick")
	if checkPassword(u.PasswordHash, "123") {
		t.Error("short password was written despite validation error")
	}
}

func TestResetPasswordBadDataDir(t *testing.T) {
	// Point at a data dir whose "users" path is occupied by a file, so the store
	// can't be opened — the operator mistyped --data-dir. It must error, not panic.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "users"), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	_, err := ResetPassword(ResetPasswordOptions{
		DataDir: dir, Username: "patrick", NewPassword: "s3cret-pw", Now: 200,
	})
	if err == nil {
		t.Fatal("expected an error when the user store cannot be opened")
	}
}

func TestResetPasswordOverLong(t *testing.T) {
	// bcrypt refuses inputs longer than 72 bytes; ResetPassword surfaces that as
	// an error rather than silently truncating.
	dir := t.TempDir()
	seedUser(t, dir, User{ID: "usr_abc", Username: "patrick", Source: SourceLocal})
	_, err := ResetPassword(ResetPasswordOptions{
		DataDir: dir, Username: "patrick", NewPassword: strings.Repeat("a", 73), Now: 200,
	})
	if err == nil {
		t.Fatal("expected an error for a password past bcrypt's 72-byte limit")
	}
}

func TestResetPasswordCorruptRecord(t *testing.T) {
	// A hex-named but unparseable record makes the username lookup fail; the error
	// must propagate, not be mistaken for "user not found".
	dir := t.TempDir()
	usersDir := filepath.Join(dir, "users")
	if err := os.MkdirAll(usersDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Filename must be valid hex (else loadAll skips it); contents are garbage.
	if err := os.WriteFile(filepath.Join(usersDir, "6162.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("seed corrupt record: %v", err)
	}
	_, err := ResetPassword(ResetPasswordOptions{
		DataDir: dir, Username: "patrick", NewPassword: "s3cret-pw", Now: 200,
	})
	if err == nil {
		t.Fatal("expected an error when a stored record can't be decoded")
	}
}

func TestResetPasswordBlankUsername(t *testing.T) {
	dir := t.TempDir()
	_, err := ResetPassword(ResetPasswordOptions{
		DataDir: dir, Username: "   ", NewPassword: "s3cret-pw", Now: 200,
	})
	if err == nil {
		t.Fatal("expected an error for a blank username")
	}
}
