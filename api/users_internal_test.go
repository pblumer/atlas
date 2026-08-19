package api

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUserStoreErrorBranches exercises the store's failure paths via on-disk
// fault injection (corrupt record, missing directory, wrong file type). These
// run as root, where permission-based faults are bypassed, so the faults are
// structural rather than mode-based.
func TestUserStoreErrorBranches(t *testing.T) {
	// A corrupt record makes every decode path fail.
	dir := t.TempDir()
	st, _ := newUserStore(dir)
	if err := os.WriteFile(st.FileFor("bad"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if _, _, err := st.Get("bad"); err == nil {
		t.Fatalf("get should surface a decode error")
	}
	if _, err := st.LoadAll(); err == nil {
		t.Fatalf("loadAll should surface a decode error")
	}
	if _, _, err := st.byUsername("x"); err == nil {
		t.Fatalf("byUsername should propagate loadAll error")
	}
	if _, _, err := st.byEmail("x@y"); err == nil {
		t.Fatalf("byEmail should propagate loadAll error")
	}
	if _, err := st.count(); err == nil {
		t.Fatalf("count should propagate loadAll error")
	}

	// A record path that is a directory makes get's read (not the missing case)
	// fail.
	st2, _ := newUserStore(t.TempDir())
	if err := os.Mkdir(st2.FileFor("d"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, _, err := st2.Get("d"); err == nil {
		t.Fatalf("get on a directory should error")
	}

	// A missing directory makes listing, saving, and deleting fail.
	dir3 := t.TempDir()
	st3, _ := newUserStore(dir3)
	if err := os.RemoveAll(dir3); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if _, err := st3.LoadAll(); err == nil {
		t.Fatalf("loadAll on a missing dir should error")
	}
	if err := st3.Save(newTestUser("1", "a")); err == nil {
		t.Fatalf("save into a missing dir should error")
	}
	if err := st3.Delete("x"); err == nil {
		t.Fatalf("delete against a missing dir should error")
	}

	// delete surfaces a non-"not found" removal error (a non-empty directory
	// standing where a record file should be).
	st4dir := t.TempDir()
	st4, _ := newUserStore(st4dir)
	busy := st4.FileFor("busy")
	if err := os.Mkdir(busy, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(busy, "child"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write child: %v", err)
	}
	if err := st4.Delete("busy"); err == nil {
		t.Fatalf("delete of a non-empty record path should error")
	}

	// loadAll surfaces a read error on a hex-named .json entry it cannot read (a
	// dangling symlink).
	st5dir := t.TempDir()
	st5, _ := newUserStore(st5dir)
	if err := os.Symlink(filepath.Join(st5dir, "missing-target"), st5.FileFor("dangle")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := st5.LoadAll(); err == nil {
		t.Fatalf("loadAll should surface a read error on an unreadable entry")
	}

	// newUserStore fails when its path can't be a directory.
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := newUserStore(filepath.Join(f, "sub")); err == nil {
		t.Fatalf("newUserStore under a file should error")
	}
}

func TestNormalizeRoles(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{nil, []string{RoleUser}},
		{[]string{}, []string{RoleUser}},
		{[]string{"  "}, []string{RoleUser}},
		{[]string{"admin", "admin", " user "}, []string{"admin", "user"}},
		{[]string{"user", "", "admin"}, []string{"user", "admin"}},
	}
	for _, c := range cases {
		got := normalizeRoles(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("normalizeRoles(%v) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("normalizeRoles(%v) = %v, want %v", c.in, got, c.want)
			}
		}
	}
}

func TestEnabledAdminCount(t *testing.T) {
	all := []User{
		{ID: "1", Roles: []string{"admin"}},
		{ID: "2", Roles: []string{"admin"}, Disabled: true},
		{ID: "3", Roles: []string{"user"}},
		{ID: "4", Roles: []string{"admin", "user"}},
	}
	if n := enabledAdminCount(all, ""); n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	if n := enabledAdminCount(all, "1"); n != 1 {
		t.Fatalf("excluding 1: count = %d, want 1", n)
	}
	if n := enabledAdminCount(all, "4"); n != 1 {
		t.Fatalf("excluding 4: count = %d, want 1", n)
	}
}

func TestToPublicStripsSecretAndNormalizesRoles(t *testing.T) {
	u := User{ID: "usr_1", Username: "a", PasswordHash: "secret", Roles: nil}
	p := u.toPublic()
	if p.Roles == nil || len(p.Roles) != 0 {
		t.Fatalf("nil roles should project to empty slice, got %v", p.Roles)
	}
	// publicUser has no PasswordHash field at all — the type guarantees the secret
	// never leaves the server; this test documents that intent.
	u2 := User{ID: "usr_2", Roles: []string{"admin"}}
	if p2 := u2.toPublic(); p2.Roles[0] != "admin" {
		t.Fatalf("roles not preserved: %v", p2.Roles)
	}
}

func TestBootstrapAdmin(t *testing.T) {
	// Generated-password path: no env set, empty store.
	t.Setenv("ATLAS_ADMIN_USERNAME", "")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "")
	st, _ := newUserStore(t.TempDir())
	s := &Server{users: st}
	if err := s.bootstrapAdmin(100); err != nil {
		t.Fatalf("bootstrapAdmin: %v", err)
	}
	u, ok, _ := st.byUsername("admin")
	if !ok || !u.hasRole(RoleAdmin) || u.PasswordHash == "" || u.Source != SourceLocal {
		t.Fatalf("seeded admin wrong: %+v (ok=%v)", u, ok)
	}

	// Idempotent: a second call with a user present is a no-op.
	if err := s.bootstrapAdmin(200); err != nil {
		t.Fatalf("bootstrapAdmin (2): %v", err)
	}
	if n, _ := st.count(); n != 1 {
		t.Fatalf("bootstrap re-seeded: count=%d", n)
	}
}

func TestBootstrapAdminCountError(t *testing.T) {
	dir := t.TempDir()
	st, _ := newUserStore(dir)
	// A corrupt record makes the pre-seed count fail, which bootstrap surfaces.
	if err := os.WriteFile(st.FileFor("bad"), []byte("{nope"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := &Server{users: st}
	if err := s.bootstrapAdmin(100); err == nil {
		t.Fatalf("bootstrapAdmin should surface a count error")
	}
}

func TestBootstrapAdminWithEnvPassword(t *testing.T) {
	t.Setenv("ATLAS_ADMIN_USERNAME", "operator")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "supersecret")
	st, _ := newUserStore(t.TempDir())
	s := &Server{users: st}
	if err := s.bootstrapAdmin(100); err != nil {
		t.Fatalf("bootstrapAdmin: %v", err)
	}
	u, ok, _ := st.byUsername("operator")
	if !ok {
		t.Fatalf("operator not seeded")
	}
	if !checkPassword(u.PasswordHash, "supersecret") {
		t.Fatalf("env password not applied")
	}
}
