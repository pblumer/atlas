package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// Who a transition is attributed to (ADR-0359).
//
// Every transition kept forever in an order needs a name: a status that says
// somebody decided, without saying who, is a decision nobody made. The three
// answers are different and none of them is "leave it blank".

func TestATransitionIsAlwaysAttributedToSomebody(t *testing.T) {
	enforced := &Server{authEnabled: true}
	open := &Server{authEnabled: false}
	ada := &httpapi.Principal{UserID: "usr_ada"}

	// Signed in: the account, whether or not enforcement is on.
	for _, s := range []*Server{enforced, open} {
		if by, ok := s.actorFor(httptest.NewRecorder(), ada, "withdrawing a position"); !ok || by != "usr_ada" {
			t.Errorf("a signed-in caller is attributed %q (ok=%v), want their own id", by, ok)
		}
	}

	// Enforcement on and nobody signed in: refused, and the refusal says what the
	// act would have recorded, because "bad request" alone is not actionable.
	rec := httptest.NewRecorder()
	if by, ok := enforced.actorFor(rec, nil, "withdrawing a position"); ok || by != "" {
		t.Errorf("an unidentified caller was attributed %q while enforcement is on", by)
	}
	if body := rec.Body.String(); !strings.Contains(body, "withdrawing a position") {
		t.Errorf("the refusal does not name the act: %s", body)
	}

	// Enforcement off: there is a single user, and the record still needs a name to
	// hold. Blank would be a transition nobody made.
	if by, ok := open.actorFor(httptest.NewRecorder(), nil, "withdrawing a position"); !ok || by == "" {
		t.Errorf("with enforcement off a transition was attributed to %q (ok=%v); the "+
			"record still needs a name", by, ok)
	}
}
