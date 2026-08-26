package api_test

import (
	"net/http"
	"strings"
	"testing"
)

// The login throttle and the audit trail, over the real HTTP path.
//
// Two gaps the ISDS concept records as R-12 and R-13: nothing stood in front of
// `/api/v1/auth/login`, and signing in, failing to, and changing an account were
// written down nowhere. Both matter more now that a login is required by default
// — the throttle guards the one door everybody comes through, and the trail is
// what makes "we require a login" demonstrable rather than merely claimed.

// TestLoginThrottleRefusesAFlood: repeated failures against one account stop
// being answered with a password check and start being refused outright.
func TestLoginThrottleRefusesAFlood(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	c := newClient(t)

	// The account budget is the tighter of the two, so it is what bites first.
	throttled := false
	for i := 0; i < 12; i++ {
		code, _ := cReq(t, c, ts, "POST", "/api/v1/auth/login",
			`{"username":"admin","password":"wrong-password"}`)
		switch code {
		case http.StatusUnauthorized:
			continue
		case http.StatusTooManyRequests:
			throttled = true
		default:
			t.Fatalf("attempt %d: status=%d, want 401 or 429", i, code)
		}
		break
	}
	if !throttled {
		t.Fatal("a dozen wrong passwords in a row were never throttled")
	}
}

// TestLoginThrottleDoesNotRevealWhetherAnAccountExists: the throttle must answer
// the same for a real account and an invented one, or it says out loud what the
// uniform "invalid credentials" message is careful not to.
func TestLoginThrottleDoesNotRevealWhetherAnAccountExists(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")

	attemptsUntilThrottled := func(username string) int {
		c := newClient(t)
		for i := 1; i <= 12; i++ {
			code, _ := cReq(t, c, ts, "POST", "/api/v1/auth/login",
				`{"username":"`+username+`","password":"wrong-password"}`)
			if code == http.StatusTooManyRequests {
				return i
			}
		}
		return 0
	}
	real := attemptsUntilThrottled("admin")
	invented := attemptsUntilThrottled("nobody-by-that-name")
	if real == 0 || invented == 0 {
		t.Fatalf("never throttled: real=%d invented=%d", real, invented)
	}
	if real != invented {
		t.Errorf("a real account throttled after %d attempts and an invented one after %d — the difference is an enumeration oracle", real, invented)
	}
}

// TestSuccessfulLoginClearsTheFailureBudget: someone who mistypes their password
// and then gets it right must not be left on the edge of a lockout.
func TestSuccessfulLoginClearsTheFailureBudget(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	c := newClient(t)

	for i := 0; i < 4; i++ {
		if code, _ := cReq(t, c, ts, "POST", "/api/v1/auth/login",
			`{"username":"admin","password":"wrong-password"}`); code != http.StatusUnauthorized {
			t.Fatalf("wrong password %d: status=%d, want 401", i, code)
		}
	}
	if code := login(t, c, ts, "admin", "password1"); code != http.StatusOK {
		t.Fatalf("correct password after four misses: got %d, want 200", code)
	}
	// The budget is back: four more misses are answered, not refused.
	for i := 0; i < 4; i++ {
		if code, _ := cReq(t, c, ts, "POST", "/api/v1/auth/login",
			`{"username":"admin","password":"wrong-password"}`); code != http.StatusUnauthorized {
			t.Fatalf("miss %d after a success: status=%d, want 401 — the success did not forgive", i, code)
		}
	}
}

// TestLoginRefusalStaysUniform: the audit line distinguishes an unknown account
// from a wrong password; the response must not. The log is for the operator, the
// wire is what an attacker gets to read.
func TestLoginRefusalStaysUniform(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")

	_, wrongPassword := cReq(t, newClient(t), ts, "POST", "/api/v1/auth/login",
		`{"username":"admin","password":"wrong-password"}`)
	_, unknownAccount := cReq(t, newClient(t), ts, "POST", "/api/v1/auth/login",
		`{"username":"nobody-by-that-name","password":"wrong-password"}`)

	if string(wrongPassword) != string(unknownAccount) {
		t.Errorf("responses differ:\n  wrong password:  %s\n  unknown account: %s", wrongPassword, unknownAccount)
	}
	if !strings.Contains(string(wrongPassword), "invalid credentials") {
		t.Errorf("refusal body = %s, want the uniform message", wrongPassword)
	}
}
