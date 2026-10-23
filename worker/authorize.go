package worker

import (
	"context"
	"errors"
	"net/http"
	"strconv"
)

// Asking before acting (ADR-0475 §5).
//
// A handler whose Worker Type the server can govern asks, before it touches the
// target system, whether it may — through [Authorize], which the worker puts within
// reach of every job it runs. The server answers with a decision it has already
// recorded, the parameters that decision covers, and, for a delegated Worker, the
// short-lived credential to act with. A Worker Type the server does not govern never
// asks, and one whose Worker is not governed is answered "enforcement off" and goes on
// exactly as before.

// Authorization is the server's answer.
type Authorization struct {
	// Enforcement is "off" for a Worker without an identity policy, else "shadow" or
	// "enforce".
	Enforcement string `json:"enforcement"`
	Permitted   bool   `json:"permitted"`
	Enforced    bool   `json:"enforced"`
	ReasonCode  string `json:"reasonCode"`
	DecisionKey uint64 `json:"decisionKey"`
	// Job is the parameters the decision covers. A handler acts on these and not on
	// what it was handed at lease time.
	Job        map[string]any `json:"job"`
	Credential *struct {
		Type        string `json:"type"`
		AccessToken string `json:"accessToken"`
		ExpiresAt   int64  `json:"expiresAt"`
	} `json:"credential"`
}

// Governed reports whether the answer came from a decision at all.
func (a *Authorization) Governed() bool {
	return a != nil && a.Enforcement != "" && a.Enforcement != "off"
}

// errPolicyRefused is what a handler returns when the server refused its action. The server
// has already failed the job or thrown its error, in the same batch as the decision,
// so the worker reports nothing: a report would only be fenced out, or — worse —
// read as a second outcome.
var errPolicyRefused = errors.New("worker: the action was refused by policy; the server has settled the job")

// Refused is the error a handler returns for a refused action.
func Refused() error { return errPolicyRefused }

type authorizerKey struct{}

// Authorize asks the server whether the job in ctx may act. It returns nil, nil when
// there is nobody to ask — a handler run outside a worker, or against a server built
// before this step existed — which a handler treats as the legacy answer.
func Authorize(ctx context.Context) (*Authorization, error) {
	ask, ok := ctx.Value(authorizerKey{}).(func(context.Context) (*Authorization, error))
	if !ok {
		return nil, nil
	}
	return ask(ctx)
}

// withAuthorizer puts the question for j within reach of its handler.
func (w *Worker) withAuthorizer(ctx context.Context, j Job) context.Context {
	return context.WithValue(ctx, authorizerKey{}, func(ctx context.Context) (*Authorization, error) {
		var out Authorization
		var header http.Header
		if w.opts.BrokerKey != "" {
			header = http.Header{"X-Atlas-Broker-Key": []string{w.opts.BrokerKey}}
		}
		err := w.callWith(ctx, "/api/v1/jobs/"+strconv.FormatUint(j.JobKey, 10)+"/authorize", header,
			map[string]any{"worker": w.opts.ID, "leaseToken": j.LeaseToken}, &out)
		var se *statusError
		if errors.As(err, &se) && se.status == 404 && se.noRoute {
			// A server without the step governs nothing.
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return &out, nil
	})
}
