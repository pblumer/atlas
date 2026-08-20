package clientreg_test

import (
	"testing"

	"github.com/pblumer/atlas/connector/clientreg"
)

// TestClientAndProblemAreDifferentAnswers is the distinction the package exists for:
// "nobody configured this name" and "somebody configured it and it is broken" must not
// look the same to a worker, because they send an operator to different places.
func TestClientAndProblemAreDifferentAnswers(t *testing.T) {
	r := clientreg.New[string]()
	r.ReplaceWith(map[string]string{"good": "client"}, map[string]string{"broken": "no credential"})

	if c, ok := r.Client("good"); !ok || c != "client" {
		t.Errorf(`Client("good") = (%q, %v), want ("client", true)`, c, ok)
	}
	if _, ok := r.Problem("good"); ok {
		t.Errorf(`Problem("good") reported a problem for a usable connector`)
	}

	if _, ok := r.Client("broken"); ok {
		t.Errorf(`Client("broken") returned a client for a connector that could not be built`)
	}
	if why, ok := r.Problem("broken"); !ok || why != "no credential" {
		t.Errorf(`Problem("broken") = (%q, %v), want ("no credential", true)`, why, ok)
	}

	// Never configured at all: no client and, crucially, no problem either.
	if _, ok := r.Client("absent"); ok {
		t.Errorf(`Client("absent") returned a client`)
	}
	if why, ok := r.Problem("absent"); ok {
		t.Errorf(`Problem("absent") = %q, want no problem recorded`, why)
	}
}

// TestRegisterClearsAProblem covers the reconfiguration path: a connector that was
// broken and is fixed must stop being reported as broken.
func TestRegisterClearsAProblem(t *testing.T) {
	r := clientreg.New[string]()
	r.ReplaceWith(nil, map[string]string{"mail": "endpoint has no port"})
	r.Register("mail", "client")

	if _, ok := r.Problem("mail"); ok {
		t.Errorf("Problem stayed after the connector was registered")
	}
	if c, ok := r.Client("mail"); !ok || c != "client" {
		t.Errorf("Client after Register = (%q, %v), want (\"client\", true)", c, ok)
	}
}

// TestReplaceClearsStaleProblems: a rebuild is the whole truth, so the plain Replace
// (used where a caller has no problems to report) must not leave an earlier rebuild's
// explanations standing.
func TestReplaceClearsStaleProblems(t *testing.T) {
	r := clientreg.New[string]()
	r.ReplaceWith(map[string]string{"a": "1"}, map[string]string{"b": "broken"})
	r.Replace(map[string]string{"a": "1"})

	if why, ok := r.Problem("b"); ok {
		t.Errorf("Problem(%q) = %q after Replace, want it cleared", "b", why)
	}
	if len(r.Problems()) != 0 {
		t.Errorf("Problems() = %v after Replace, want empty", r.Problems())
	}
}

// TestNilMapsClearRatherThanPanic: rebuilding with nothing configured is an ordinary
// state, not an error, and the registry must be usable afterwards.
func TestNilMapsClearRatherThanPanic(t *testing.T) {
	r := clientreg.New[string]()
	r.Register("a", "1")
	r.ReplaceWith(nil, nil)

	if _, ok := r.Client("a"); ok {
		t.Errorf("Client survived a nil Replace")
	}
	r.Register("b", "2") // must not panic on a re-created map
	if _, ok := r.Client("b"); !ok {
		t.Errorf("Register after a nil Replace did not take")
	}
}

// TestUnresolvedSaysWhichProblemItIs pins the message an operator actually reads on a
// parked token. The old one claimed a configured-but-broken connector did not exist,
// which is the sentence that sent someone looking for a connector that was right there.
func TestUnresolvedSaysWhichProblemItIs(t *testing.T) {
	r := clientreg.New[string]()
	r.ReplaceWith(nil, map[string]string{"Patrick Blumer": "the connector is disabled"})

	configured := r.Unresolved("mail", "Patrick Blumer").Error()
	want := `mail: connector "Patrick Blumer" is configured but not usable: the connector is disabled`
	if configured != want {
		t.Errorf("Unresolved for a broken connector = %q, want %q", configured, want)
	}

	absent := r.Unresolved("mail", "Nobody").Error()
	if absent != `mail: no connector registered as "Nobody"` {
		t.Errorf("Unresolved for an unconfigured connector = %q", absent)
	}
}
