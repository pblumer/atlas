package api

import (
	"path/filepath"
	"testing"
)

// directoryServer is a server with nothing but the two stores the directory
// reads. It resolves people and groups and touches no engine state, so it needs
// no engine.
func directoryServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	users, err := newUserStore(filepath.Join(dir, "users"))
	if err != nil {
		t.Fatalf("newUserStore: %v", err)
	}
	groups, err := newGroupStore(filepath.Join(dir, "groups"))
	if err != nil {
		t.Fatalf("newGroupStore: %v", err)
	}
	return &Server{users: users, groups: groups}
}

// The directory is what lets a model address somebody it only holds a reference
// to. Its cases are small and each one is a decision.

func TestTheDirectoryAnswersTheSpellingsThatDecideATask(t *testing.T) {
	s := directoryServer(t)
	d := mailDirectory{s}

	alice := User{ID: "usr_alice", Username: "alice", Email: "alice@example.ch"}
	if err := s.users.Save(alice); err != nil {
		t.Fatalf("save alice: %v", err)
	}
	quiet := User{ID: "usr_quiet", Username: "quiet"}
	if err := s.users.Save(quiet); err != nil {
		t.Fatalf("save quiet: %v", err)
	}
	g := group{ID: "grp_einkauf", Name: "Einkauf", Members: []string{"usr_alice", "usr_quiet", "usr_gone"}}
	if err := s.groups.Save(g); err != nil {
		t.Fatalf("save group: %v", err)
	}

	for _, tc := range []struct {
		name, ref string
		want      string
	}{
		// The four spellings holdsTask accepts when it decides who may act on a
		// task. They have to be the same four, or a notification reaches somebody
		// who then cannot do anything with it.
		{"by username", "alice", "alice@example.ch"},
		{"by principal id", "usr_alice", "alice@example.ch"},
		{"a group by id", "grp_einkauf", "alice@example.ch"},
		{"a group by name", "Einkauf", "alice@example.ch"},
		// An account with no address contributes nothing rather than failing the
		// send: a group of three where one has no mail should still reach the one
		// who does.
		{"a person with no address", "quiet", ""},
		// And a reference nobody answers to resolves to nothing, which the caller
		// reports as an error rather than sending to a shorter list.
		{"nobody", "niemand", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := d.Recipients(tc.ref)
			if err != nil {
				t.Fatalf("Recipients: %v", err)
			}
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("= %v, want nothing", got)
				}
				return
			}
			found := false
			for _, a := range got {
				if a == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("= %v, want it to contain %s", got, tc.want)
			}
		})
	}

	// A member the store no longer holds is skipped, not an error: a group with a
	// dangling id still reaches the people who are there.
	got, err := d.Recipients("grp_einkauf")
	if err != nil {
		t.Fatalf("Recipients(group): %v", err)
	}
	if len(got) != 1 || got[0] != "alice@example.ch" {
		t.Errorf("group = %v, want only the one member with an address", got)
	}

	if got, err := d.Recipients("  "); err != nil || len(got) != 0 {
		t.Errorf("an empty reference = %v, %v; want nothing and no error", got, err)
	}
}

// TestAPersonWinsOverAGroupOfTheSameName: an individual recipient is the commoner
// case, and a group named like a user would otherwise silently widen who is told.
func TestAPersonWinsOverAGroupOfTheSameName(t *testing.T) {
	s := directoryServer(t)
	d := mailDirectory{s}

	if err := s.users.Save(User{ID: "usr_leitung", Username: "leitung", Email: "chef@example.ch"}); err != nil {
		t.Fatalf("save user: %v", err)
	}
	if err := s.users.Save(User{ID: "usr_m", Username: "m", Email: "m@example.ch"}); err != nil {
		t.Fatalf("save member: %v", err)
	}
	if err := s.groups.Save(group{ID: "grp_l", Name: "leitung", Members: []string{"usr_m"}}); err != nil {
		t.Fatalf("save group: %v", err)
	}

	got, err := d.Recipients("leitung")
	if err != nil {
		t.Fatalf("Recipients: %v", err)
	}
	if len(got) != 1 || got[0] != "chef@example.ch" {
		t.Errorf("= %v, want only the person's own address", got)
	}
}
