package api

import (
	"testing"
	"time"
)

// The login throttle. Before it, `/api/v1/auth/login` had nothing in front of it
// at all: the token bucket existed but guarded only the public form routes, so a
// script could guess passwords as fast as bcrypt would answer, and the only
// record of it was that the server was busy.

// guardAt builds a guard whose clock the test drives, so the refill is asserted
// rather than waited for.
func guardAt(now *time.Time) *loginGuard {
	g := newLoginGuard()
	clock := func() time.Time { return *now }
	g.byIP.now = clock
	g.byAccount.now = clock
	return g
}

// TestLoginGuardStopsAFloodFromOneAddress: the per-address bucket is the primary
// defence, because it costs an attacker nothing to try a different username but
// something to try a different network.
func TestLoginGuardStopsAFloodFromOneAddress(t *testing.T) {
	now := time.Unix(0, 0)
	g := guardAt(&now)

	// Each attempt names a different account, so only the address bucket can be
	// what stops this.
	allowed := 0
	for i := 0; i < loginIPBurst*3; i++ {
		if g.allow("10.0.0.1", string(rune('a'+i%26))+"ccount") {
			allowed++
		}
	}
	if allowed != loginIPBurst {
		t.Errorf("allowed %d attempts from one address, want the burst of %d", allowed, loginIPBurst)
	}

	// A different address is unaffected: one client's flood must not lock out
	// everybody else, which is the failure mode of throttling too coarsely.
	if !g.allow("10.0.0.2", "someone") {
		t.Error("a second address was refused because the first one flooded")
	}
}

// TestLoginGuardStopsGuessingOneAccountAcrossAddresses: an attacker with a botnet
// pays nothing for a new address, so the address bucket alone would not bound how
// many guesses one account takes.
func TestLoginGuardStopsGuessingOneAccountAcrossAddresses(t *testing.T) {
	now := time.Unix(0, 0)
	g := guardAt(&now)

	allowed := 0
	for i := 0; i < loginAccountBurst*3; i++ {
		// A fresh address every time.
		if g.allow("10.0."+string(rune('0'+i/10))+"."+string(rune('0'+i%10)), "alice") {
			allowed++
		}
	}
	if allowed != loginAccountBurst {
		t.Errorf("allowed %d guesses at one account, want the burst of %d", allowed, loginAccountBurst)
	}
}

// TestLoginGuardIsBlindToWhetherAnAccountExists is what keeps the throttle from
// becoming a user-enumeration oracle. It is charged for every attempt, before
// anything has looked the name up, so "alice" and "nonesuch" behave alike.
func TestLoginGuardIsBlindToWhetherAnAccountExists(t *testing.T) {
	now := time.Unix(0, 0)
	g := guardAt(&now)

	// A distinct address per account keeps the address bucket out of it, so what
	// each count measures is the account bucket alone.
	spend := func(ip, account string) int {
		allowed := 0
		for i := 0; i < loginAccountBurst*2; i++ {
			if g.allow(ip, account) {
				allowed++
			}
		}
		return allowed
	}
	real := spend("10.0.0.1", "alice")
	fake := spend("10.0.0.2", "nonesuch")
	if real != fake {
		t.Errorf("a real account took %d attempts and an unknown one %d — the difference is an enumeration oracle", real, fake)
	}
}

// TestLoginGuardKeysAccountsCaseInsensitively: usernames that differ only in case
// must share a bucket, or the limit is bypassed by typing Alice, ALICE, aLice.
func TestLoginGuardKeysAccountsCaseInsensitively(t *testing.T) {
	now := time.Unix(0, 0)
	g := guardAt(&now)

	for i := 0; i < loginAccountBurst; i++ {
		if !g.allow("10.0.0.1", "alice") {
			t.Fatalf("attempt %d refused before the burst was spent", i)
		}
	}
	if g.allow("10.0.0.1", "ALICE") {
		t.Error("ALICE drew from a different bucket than alice")
	}
}

// TestLoginGuardForgivesOnSuccess: a person who mistypes their password twice and
// then gets it right should not be carrying those two failures around for the next
// quarter of an hour.
func TestLoginGuardForgivesOnSuccess(t *testing.T) {
	now := time.Unix(0, 0)
	g := guardAt(&now)

	for i := 0; i < loginAccountBurst-1; i++ {
		if !g.allow("10.0.0.1", "alice") {
			t.Fatalf("attempt %d refused before the burst was spent", i)
		}
	}
	g.forgive("alice")

	for i := 0; i < loginAccountBurst; i++ {
		if !g.allow("10.0.0.1", "alice") {
			t.Fatalf("attempt %d after a success was refused; the burst was not restored", i)
		}
	}
}

// TestLoginGuardRefills: the lockout is temporary by construction. A person locked
// out by somebody else guessing at their name recovers on their own.
func TestLoginGuardRefills(t *testing.T) {
	now := time.Unix(0, 0)
	g := guardAt(&now)

	for i := 0; i < loginAccountBurst; i++ {
		g.allow("10.0.0.1", "alice")
	}
	if g.allow("10.0.0.1", "alice") {
		t.Fatal("the account burst did not run out")
	}
	now = now.Add(loginAccountRecovery)
	if !g.allow("10.0.0.1", "alice") {
		t.Errorf("still locked out after %s; the lockout must heal without an operator", loginAccountRecovery)
	}
}

// TestRateLimiterClear covers the forgiveness primitive on its own: clearing an
// unknown key is a no-op, and clearing a spent one restores it to full.
func TestRateLimiterClear(t *testing.T) {
	l := newRateLimiter(2, 0)
	l.clear("never-seen") // must not panic

	if !l.allow("k") || !l.allow("k") {
		t.Fatal("a fresh key must start full")
	}
	if l.allow("k") {
		t.Fatal("the bucket did not run out")
	}
	l.clear("k")
	if !l.allow("k") {
		t.Error("clear did not restore the bucket")
	}
}
