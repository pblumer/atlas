package formlookup

import (
	"strings"
	"testing"
	"time"
)

func testSealer(now *time.Time) *sealer {
	s := newSealer()
	s.now = func() time.Time { return *now }
	return s
}

func TestACursorOpensOnlyForTheQueryItWasSealedFor(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := testSealer(&now)
	c := cursorBinding{Task: 7, Field: "user", Principal: "u1", Query: "h1"}
	sealed := s.seal(c, "https://graph.microsoft.com/v1.0/users?$skiptoken=abc")
	if strings.Contains(sealed, "graph.microsoft.com") || strings.Contains(sealed, "skiptoken") {
		t.Fatalf("the cursor %q shows the provider's link", sealed)
	}
	next, err := s.open(sealed, c)
	if err != nil || next != "https://graph.microsoft.com/v1.0/users?$skiptoken=abc" {
		t.Fatalf("open = %q, %v", next, err)
	}
	for name, other := range map[string]cursorBinding{
		"another task":   {Task: 8, Field: "user", Principal: "u1", Query: "h1"},
		"another field":  {Task: 7, Field: "boss", Principal: "u1", Query: "h1"},
		"another person": {Task: 7, Field: "user", Principal: "u2", Query: "h1"},
		"another query":  {Task: 7, Field: "user", Principal: "u1", Query: "h2"},
	} {
		if _, err := s.open(sealed, other); err == nil {
			t.Errorf("%s: the cursor opened", name)
		}
	}
}

func TestACursorExpiresAndDoesNotSurviveItsKey(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := testSealer(&now)
	c := cursorBinding{Task: 7, Field: "user", Principal: "u1", Query: "h1"}
	sealed := s.seal(c, "next")
	now = now.Add(cursorLifetime + time.Second)
	if _, err := s.open(sealed, c); err == nil {
		t.Error("an expired cursor opened")
	}
	// A restart is a new key: every cursor the last process handed out is refused.
	now = time.Unix(1_800_000_000, 0)
	restarted := testSealer(&now)
	if _, err := restarted.open(sealed, c); err == nil {
		t.Error("a cursor opened under another process's key")
	}
}

func TestATamperedCursorIsRefused(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := testSealer(&now)
	c := cursorBinding{Task: 7, Field: "user", Principal: "u1", Query: "h1"}
	sealed := s.seal(c, "next")
	for _, bad := range []string{"", "x", sealed[:len(sealed)-2], "A" + sealed[1:], sealed + "A", "!!!!"} {
		if bad == sealed {
			continue
		}
		if _, err := s.open(bad, c); err == nil {
			t.Errorf("%q opened", bad)
		}
	}
}
