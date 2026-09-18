package api

import (
	"strings"
	"testing"
)

// The portal page when a sign-in is required and nobody has one yet
// (ADR-draft-portal-carries-its-own-sign-in).
//
// This is the case the page had no answer for. With --auth on and no session,
// every route the portal reads answers 401: the catalogue read was swallowed and
// reported as "no catalogue is assigned to you", the orders read was not
// swallowed at all, and what a visitor saw was an error line with an HTTP status
// in it over an empty page. Nothing said a sign-in was needed and nothing offered
// one — on the one surface in Atlas whose readers are not operators and have
// nowhere else to go.
//
// The server half needs no change: 401 is the right answer to an anonymous call,
// and everything a sign-in screen reads before anybody has a session is already
// public (see wantPublicRoutes in access_internal_test.go). What was missing was
// a page that reads the refusal as a question rather than as a failure.

// TestThePortalAsksWhoIsReadingBeforeItReadsAnythingElse.
//
// The gate has to come first, not somewhere in the middle: every other call on
// this page needs the session, so a load that reads the catalogue first spends a
// refusal before it has established there is nobody to refuse.
func TestThePortalAsksWhoIsReadingBeforeItReadsAnythingElse(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "async function load(", "\n}")
	gate := strings.Index(body, "readMe(")
	if gate < 0 {
		t.Fatal("load() no longer asks who is reading, so a refused call is the first " +
			"thing the page learns and it has no way to read it as a question")
	}
	for _, route := range []string{"/api/v1/portal/catalog", "/api/v1/orders"} {
		if at := strings.Index(body, route); at >= 0 && at < gate {
			t.Errorf("load() reads %s before it knows whether anybody is signed in", route)
		}
	}
	if !strings.Contains(body, "state.needSignIn") {
		t.Error("load() does not stop on a page that needs a sign-in, so every " +
			"remaining call is made only to be refused")
	}
}

// TestARefusalIsTheOnlyThingReadAsASignIn.
//
// A 401 says "not you"; a 500 says the server could not answer. The page must
// tell them apart, or an instance with a broken session store shows its customers
// a login form that cannot possibly work — and, the other way round, a reader
// whose session is fine is asked for a password because a lookup timed out.
// Unreadable is not forbidden, and the answer to not knowing is to carry on and
// offer less.
func TestARefusalIsTheOnlyThingReadAsASignIn(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "async function readMe(", "\n}")
	if !strings.Contains(body, "e.status === 401") {
		t.Error("the gate does not distinguish a refusal from an unreadable answer, " +
			"so anything that goes wrong reads as nobody being signed in")
	}
}

// TestTheStatusTravelsWithTheFailure.
//
// The page can only tell 401 from anything else if api() keeps the status. It
// used to throw a bare Error carrying the status inside a message string, which
// is a thing to parse rather than a thing to read.
func TestTheStatusTravelsWithTheFailure(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "async function api(", "\n}")
	if !strings.Contains(body, "err.status = res.status") {
		t.Error("a failed call throws without its status, so no caller can tell a " +
			"refusal from a server that broke")
	}
}

// TestTheSignInIsOfferedWithWhateverTheServerOffers.
//
// A password form alone is a dead end on an installation that federates its
// login: there is no password to type, and the one control on screen cannot work.
// The providers endpoint is public, answers an empty list where nothing is
// configured, and is what the Console's own sign-in reads.
func TestTheSignInIsOfferedWithWhateverTheServerOffers(t *testing.T) {
	js := readWeb(t, "portal.js")
	body := webRegion(t, js, "async function loadSignInOptions(", "\n}")
	if !strings.Contains(body, "/api/v1/auth/providers") {
		t.Error("the sign-in never asks for the identity providers, so an " +
			"installation that federates its login shows a password form nobody can use")
	}
	if !strings.Contains(body, "try {") {
		t.Error("the providers are read outside a try, so an instance that cannot " +
			"answer loses the password form as well")
	}
	if !strings.Contains(webRegion(t, js, "function renderSignIn(", "\n}"), "state.providers") {
		t.Error("the sign-in screen draws no provider, so reading them changes nothing")
	}
}

// TestTheThrottleIsNotReportedAsAWrongPassword.
//
// After five wrong guesses the server refuses the *attempt* for a quarter of an
// hour without looking at the password (ADR-0197). Reported as "wrong password"
// that is how somebody spends fifteen minutes hunting a password that is already
// correct — and a portal customer, unlike an operator, has no server log to
// consult and nobody to ask but the desk this portal exists to save.
func TestTheThrottleIsNotReportedAsAWrongPassword(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function signInFailureText(", "\n}")
	if !strings.Contains(body, "429") {
		t.Error("the throttle is reported as a credential failure, so the one wait " +
			"that cannot be shortened by typing looks like something to retype")
	}
	if !strings.Contains(body, "401") {
		t.Error("a wrong password is not named, so every refusal reads the same")
	}
}

// TestASessionThatRunsOutReturnsToTheSignIn.
//
// The portal is a page people leave open. A session that expires while it stands
// there turns the next load into the same 401 the gate exists for — and reporting
// that one as a failure is the original defect one step later.
func TestASessionThatRunsOutReturnsToTheSignIn(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "document.addEventListener('DOMContentLoaded'", "\n});")
	// The mechanism and not the number: 401 appears in the comment that explains
	// this, so a guard looking for the number passes over code that no longer does
	// anything with it. What has to be there is the page turning back to the gate.
	if !strings.Contains(body, "state.needSignIn = true") {
		t.Error("a load that fails on a refusal is shown as a broken portal rather " +
			"than as a session that ran out")
	}
	if !strings.Contains(body, "signin.expired") {
		t.Error("nothing tells the visitor their session ended, so the sign-in " +
			"appears for no stated reason on a page they were already using")
	}
	if !strings.Contains(body, "forgetTheReader()") {
		t.Error("the catalogue resolved for the session that ended is kept, so the " +
			"sign-in asks who you are under somebody's catalogue name and brand")
	}
}

// TestSigningInStaysOnThePortal.
//
// The Console's sign-in lands on the Console, which is the wrong product for
// somebody who followed a link to order a laptop and holds no Console role. A
// successful sign-in here re-reads the page they asked for and nothing else.
func TestSigningInStaysOnThePortal(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "async function signIn(", "\n}")
	if !strings.Contains(body, "/api/v1/auth/login") {
		t.Fatal("the sign-in posts nowhere")
	}
	if !strings.Contains(body, "load(") {
		t.Error("a successful sign-in does not reload the portal, so the visitor is " +
			"signed in and still looking at the sign-in screen")
	}
	if strings.Contains(body, "location.href") || strings.Contains(body, "index.html") {
		t.Error("a successful sign-in navigates away from the portal the visitor asked for")
	}
}

// TestTheSignInSaysWhichInstallationIsAsking.
//
// A page asking for a password while showing nothing about who is asking is the
// shape of a phishing page, and the mark is the one thing on this screen the
// visitor can recognise. No catalogue is resolved yet — that is what the sign-in
// is for — so it is the instance's mark, from the endpoint that is public exactly
// because a sign-in screen has to be able to read it.
func TestTheSignInSaysWhichInstallationIsAsking(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function renderMark(", "\n}")
	// Not merely that the constant is named — it was already named here, as the
	// fallback for a catalogue mark that fails to load. What has to be true is that
	// the mark survives having no catalogue at all, which is every sign-in screen.
	if !strings.Contains(body, "state.needSignIn") {
		t.Error("the sign-in screen carries no mark, so a visitor is asked for a " +
			"password by a page that says nothing about who is asking")
	}
}
