package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// The recipient field, and who it is for
// (ADR-0356).
//
// Two things about it are decided on the server and drawn in the browser, which
// is exactly the kind of agreement that rots without being noticed: the page
// keeps offering what the server has started refusing, or stops offering what it
// would still allow. Neither breaks a test that only exercises one side.
//
// So the constants are read out of the Go source the server gates on, and the
// page is held against them.

// portalSource is the recipient field's half of the page.
func portalSource(t *testing.T) string {
	t.Helper()
	return readWeb(t, "portal.js")
}

// TestThePortalOffersTheRecipientFieldOnlyToRolesTheServerAccepts.
//
// The field was drawn for every visitor while the server had just started
// refusing all but two roles, so an ordinary account saw an input that answered
// 403 — which reads as a permission that failed rather than one they never had.
//
// The role names are taken from the constants the gate itself uses, so renaming a
// role on the server fails here instead of silently leaving the page offering a
// field nobody may use.
func TestThePortalOffersTheRecipientFieldOnlyToRolesTheServerAccepts(t *testing.T) {
	src := portalSource(t)

	start := strings.Index(src, "function renderForWhom(")
	if start < 0 {
		t.Fatal("portal.js has no renderForWhom(); if the recipient field moved, this " +
			"test now passes vacuously and says so instead")
	}
	end := strings.Index(src[start:], "\n}")
	if end < 0 {
		t.Fatal("renderForWhom() is not closed where this test expects")
	}
	if !strings.Contains(src[start:start+end], "if (!state.mayOrderForOthers) return null;") {
		t.Error("renderForWhom does not leave the field out for an account that may not " +
			"order in somebody else's name; it will be drawn and then answered 403")
	}

	for _, role := range []string{RoleOperator, RoleAdmin} {
		if !strings.Contains(src, "'"+role+"'") {
			t.Errorf("the server lets the %q role order in somebody else's name and "+
				"portal.js never names it, so the page and the gate disagree about who "+
				"the field is for", role)
		}
	}
}

// TestTheRecipientPickerOffersNoGroups.
//
// The directory carries groups beside users, because a scope grant can name a
// team. An order cannot: an entitlement is held by a person, so offering a group
// here would produce a recipient the server refuses — after the orderer has
// filled a basket.
func TestTheRecipientPickerOffersNoGroups(t *testing.T) {
	src := portalSource(t)
	if !strings.Contains(src, "e.type === '"+PrincipalTypeUser+"'") {
		t.Errorf("portal.js does not narrow the principals directory to %q entries. "+
			"The same list carries groups, and a group cannot receive an order",
			PrincipalTypeUser)
	}
}

// TestTypingARecipientDoesNotRebuildTheFieldBeingTypedInto: the same defect as the
// catalogue search's, in the field beside it. A full render replaces the input
// mid-name and the caret lands at the end after every character.
func TestTypingARecipientDoesNotRebuildTheFieldBeingTypedInto(t *testing.T) {
	src := portalSource(t)
	start := strings.Index(src, "id: 'forwhom'")
	if start < 0 {
		t.Fatal("portal.js has no field with id 'forwhom'; if it moved, this test now " +
			"checks nothing and says so instead")
	}
	end := strings.Index(src[start:], "\n      }),")
	if end < 0 {
		t.Fatal("the recipient field's attributes are not closed where this test expects")
	}
	handler := src[start : start+end]
	if !strings.Contains(handler, "repaintPeople()") {
		t.Errorf("the recipient field's input handler does not repaint the suggestions "+
			"alone:\n%s", handler)
	}
	// And what is sent follows what is shown. Typing over a picked name must not
	// leave the order pointing at somebody whose name is no longer in the field.
	if !strings.Contains(handler, "state.forWhom = e.target.value") {
		t.Error("typing over a picked recipient does not clear the id that was picked, " +
			"so the order would be placed for whoever was chosen before")
	}
}

// TestAnUnreadableUserStoreIsNotAMissingPerson.
//
// The arm no HTTP test can reach: a user store that fails to load is not
// something a request can bring about, so the rule is stated where it can be.
// Answering 404 here told an operator their colleague has no account when what
// happened is that Atlas could not look — and "this person does not exist" is the
// kind of definite answer somebody acts on.
func TestAnUnreadableUserStoreIsNotAMissingPerson(t *testing.T) {
	code, msg := principalRefusal(errors.New("open users.json: input/output error"))
	if code != http.StatusInternalServerError {
		t.Errorf("an unreadable user store answers %d, want 500", code)
	}
	if !strings.Contains(msg, "input/output error") {
		t.Errorf("the message loses what actually failed: %q", msg)
	}

	missing := fmt.Errorf("%w: no account %q", httpapi.ErrNoSuchPrincipal, "nobody")
	code, msg = principalRefusal(missing)
	if code != http.StatusNotFound {
		t.Errorf("a name nobody holds answers %d, want 404", code)
	}
	// In the resolver's own words: they already name the spellings that resolve,
	// and a prefix here would put "resolve principal:" in front of a sentence
	// written for the person who typed the name.
	if msg != missing.Error() {
		t.Errorf("the refusal rewrites the resolver's sentence: %q", msg)
	}
}
