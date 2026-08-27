package api

import (
	"strings"
	"time"
)

// The login throttle (ADR-0197).
//
// `/api/v1/auth/login` had nothing in front of it. The token bucket in
// ratelimit.go existed, but only the public form routes used it, so password
// guessing was bounded by nothing but how fast bcrypt would answer — and bcrypt
// answering slowly is itself the problem, because each attempt costs the server
// far more than it costs the caller. That mattered less while a login was
// optional; it is the front door now that one is required by default
// (ADR-0195).
//
// Two buckets, because one of them alone is bypassable:
//
//   - By address, because an attacker pays nothing for a different username but
//     something for a different network. This is also what bounds the CPU an
//     unauthenticated caller can make the server spend on bcrypt.
//   - By account, because an attacker with a botnet pays nothing for a different
//     address either, and the address bucket would then never bound how many
//     guesses one account takes.
//
// Both are the same token bucket with different keys, so there is one mechanism
// to reason about and one already covered by tests.

const (
	// loginIPBurst is how many attempts one address may make back to back.
	// Generous on purpose: a whole office behind one NAT address is a normal
	// deployment, and a bucket that punishes that gets turned off.
	loginIPBurst = 20

	// loginIPRefill is how fast that budget comes back: 30 an hour short of an
	// attempt every two seconds, which no person notices and no script can use.
	loginIPRefill = 0.5

	// loginAccountBurst is how many attempts one account absorbs before it is
	// throttled — enough for a person who cannot remember which password this
	// system has, short enough that guessing is pointless.
	loginAccountBurst = 5

	// loginAccountRecovery is how long a fully spent account bucket takes to
	// refill. It is the lockout's whole duration, and it is deliberately short:
	// the cost of this design is that somebody guessing at your name can lock you
	// out, so the lockout has to heal on its own rather than need an operator.
	loginAccountRecovery = 15 * time.Minute
)

// loginAccountRefill is the per-second rate that spends loginAccountRecovery
// refilling loginAccountBurst tokens.
var loginAccountRefill = float64(loginAccountBurst) / loginAccountRecovery.Seconds()

// loginGuard decides whether an authentication attempt may proceed at all.
type loginGuard struct {
	byIP      *rateLimiter
	byAccount *rateLimiter
}

func newLoginGuard() *loginGuard {
	return &loginGuard{
		byIP:      newRateLimiter(loginIPBurst, loginIPRefill),
		byAccount: newRateLimiter(loginAccountBurst, loginAccountRefill),
	}
}

// allow reports whether an attempt from ip against account may proceed, spending
// from both budgets.
//
// It is called before anything has looked the account up, and it charges the
// account bucket whether or not that account exists. That is what keeps the
// throttle from answering a question the login itself is careful not to answer:
// if only real accounts could be throttled, six refused attempts would tell an
// attacker the name is real.
func (g *loginGuard) allow(ip, account string) bool {
	// Both are spent, not short-circuited: an attempt refused by one still counts
	// against the other, so cycling addresses does not preserve an account's
	// budget and cycling accounts does not preserve an address's.
	okIP := g.byIP.allow(ip)
	okAccount := g.byAccount.allow(accountKey(account))
	return okIP && okAccount
}

// forgive restores an account's budget after a successful login. Somebody who
// mistypes twice and then gets it right should not carry those failures around
// for the next quarter of an hour.
//
// Only the account budget: a successful login says something about who is at that
// account, not about how much traffic an address should be allowed to make.
func (g *loginGuard) forgive(account string) { g.byAccount.clear(accountKey(account)) }

// accountKey normalizes a username to the key its bucket lives under. Case-folded,
// because a limit that Alice, ALICE and aLice each get their own copy of is not a
// limit.
func accountKey(account string) string { return strings.ToLower(strings.TrimSpace(account)) }
