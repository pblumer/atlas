package api

import (
	"strings"
	"testing"
)

// The parts of dynamic registration that decide something on their own, tested
// without a server: what an application ends up being called on a consent screen,
// and what the cap is allowed to throw away.

// TestRegistrationNameFallsBackToSomethingTruthful. client_name is optional in RFC
// 7591, and the consent screen still has to call the application something. What it
// falls back to is a claim the client had to be able to back — a host it can receive
// a redirect on — rather than a name, which is free.
func TestRegistrationNameFallsBackToSomethingTruthful(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		uri     string
		want    string
		wantErr bool
	}{
		{"a name it gave", "Some Connector", "https://c.example.com/cb", "Some Connector", false},
		{"trimmed", "  Some Connector  ", "https://c.example.com/cb", "Some Connector", false},
		{"no name: the host it registered", "", "https://c.example.com/cb", "c.example.com", false},
		{"no name and no host", "", "https://", "", true},
		{"a name that would not fit on the screen", strings.Repeat("s", maxClientNameRunes+1), "https://c.example.com/cb", "", true},
		{"a name at the limit", strings.Repeat("s", maxClientNameRunes), "https://c.example.com/cb", strings.Repeat("s", maxClientNameRunes), false},
		// The page escapes it, so this is not about markup: it is about a name that
		// restructures the question a person is being asked.
		{"a name with a line break", "Atlas\nApprove this", "https://c.example.com/cb", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := registrationName(tc.raw, tc.uri)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("= %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEvictionOrderIgnoresWhatAnOperatorRegistered: the cap governs
// self-registration and nothing else. An administrator's decision is not something
// an unauthenticated endpoint may undo, however full the table is.
func TestEvictionOrderIgnoresWhatAnOperatorRegistered(t *testing.T) {
	got := evictionOrder([]oauthClient{
		{ID: "vetted", CreatedAt: 1},
		{ID: "third", Dynamic: true, Seq: 3},
		{ID: "first", Dynamic: true, Seq: 1},
		{ID: "second", Dynamic: true, Seq: 2},
	})
	if len(got) != 3 {
		t.Fatalf("%d candidates, want 3 — an operator-registered client is not one", len(got))
	}
	// Oldest first, by sequence rather than by the clock: a flood registers many
	// within one second, and ordering by CreatedAt alone would pick an arbitrary
	// member of it.
	for i, want := range []string{"first", "second", "third"} {
		if got[i].ID != want {
			t.Errorf("candidate %d = %q, want %q", i, got[i].ID, want)
		}
	}

	// Records written before Seq existed all carry zero. The order still has to be
	// total, or which one is evicted would depend on the order the store happened to
	// read them in.
	tied := evictionOrder([]oauthClient{
		{ID: "b", Dynamic: true}, {ID: "a", Dynamic: true}, {ID: "c", Dynamic: true},
	})
	for i, want := range []string{"a", "b", "c"} {
		if tied[i].ID != want {
			t.Errorf("tied candidate %d = %q, want %q — the order must be total", i, tied[i].ID, want)
		}
	}
}

// TestFirstUnapprovedReportsWhenThereIsNothingToEvict. The caller must be able to
// tell "here is what to drop" from "there is nothing here that may be dropped" —
// the second is the one case where registering is refused rather than making room.
func TestFirstUnapprovedReportsWhenThereIsNothingToEvict(t *testing.T) {
	candidates := []oauthClient{{ID: "a"}, {ID: "b"}}

	if got, ok := firstUnapproved(candidates, map[string]bool{"a": true}); !ok || got != "b" {
		t.Errorf("= %q, %v; want b, true — the approved one must be passed over", got, ok)
	}
	if _, ok := firstUnapproved(candidates, map[string]bool{"a": true, "b": true}); ok {
		t.Error("reported something to evict when a person had approved every client")
	}
	if _, ok := firstUnapproved(nil, nil); ok {
		t.Error("reported something to evict out of an empty table")
	}
}
