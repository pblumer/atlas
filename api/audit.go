package api

import (
	"log/slog"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// The security audit trail (ADR-draft-login-throttle-and-audit-log).
//
// Atlas's business trails were always strong: every state transition is an event,
// every external variable change names who made it (ADR-0098), every manual task
// completion the same (ADR-0159). What was written down nowhere was the security
// half — who signed in, who failed to, who changed an account or minted a
// credential. The ISDS concept records that gap as R-13, and the answer it had to
// give was "the reverse proxy must supply it", which is an answer about somebody
// else's software.
//
// These lines close it, and they are also what makes the rest of this work
// demonstrable rather than merely true: a login gate nobody can see working is a
// claim, not a control.
//
// Three rules hold for everything here.
//
//   - It goes through logging, so it is one stream with stable event= names,
//     rendered as JSON with --log-format=json and shipped wherever the operator
//     ships logs. A second sink would be a second thing to configure, back up and
//     forget.
//   - Every line says who acted and from where, in the same attributes, so a
//     query for one actor finds all of them.
//   - No line carries a secret. Not a password, not a token, not a prefix of one.
//     An attribute is precisely what a log shipper extracts, indexes and keeps.

// auditActor returns the attributes every audit line carries about who is acting:
// the client address always, and the principal when the request has one.
//
// A login attempt has no principal yet — that is what it is trying to become — so
// the address is what identifies it, and the username it named is passed by the
// caller as a separate attribute rather than folded in here. Confusing "the
// account somebody asked for" with "the account somebody is" is how an audit trail
// starts crediting actions to the victim.
func auditActor(r *http.Request) []slog.Attr {
	attrs := []slog.Attr{slog.String("client_ip", httpapi.ClientIP(r))}
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		attrs = append(attrs,
			slog.String("actor", p.Username),
			slog.String("actor_id", p.UserID))
	}
	return attrs
}

// audit records one security-relevant act at INFO: something happened that an
// auditor will want to see, and that nothing is wrong with.
func audit(r *http.Request, e logging.Event, msg string, attrs ...slog.Attr) {
	logging.Info(e, msg, append(auditActor(r), attrs...)...)
}

// auditRefusal records one security-relevant refusal at WARN: a failed login, a
// throttled one, an operation somebody was not allowed. These are the lines an
// alert is built on, so they are levelled apart from the ordinary ones rather
// than left for a query to separate.
func auditRefusal(r *http.Request, e logging.Event, msg string, attrs ...slog.Attr) {
	logging.Warn(e, msg, append(auditActor(r), attrs...)...)
}
