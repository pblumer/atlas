package ad

import (
	"context"
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/state"
)

// A resolved AD task, and the function that performs one.
//
// This is [ADR-0168]'s split applied to a kind that was built in process, and AD's
// shape makes the line fall in a different place than mail's did.
//
// A mail task names a connector and nothing else: the endpoint and the credential
// both move to the worker. An AD task authors its own server URL and bind DN, because
// ADR-0166 decided the directory is model data — so those keep travelling, and the
// only thing that moves is *where the bind password behind the reference is read*.
// The engine's vault stops being consulted for an offloaded AD task; the worker
// resolves the same `ATLAS_CONNECTOR_<REF>_TOKEN` from its own environment.
//
// What that buys is the thing worth buying: a compromised engine no longer yields a
// bind credential with write access to the directory. [Job] carries the reference,
// never the value, which is a property of the type rather than of the code filling
// it in.
//
// [ADR-0168]: https://github.com/pblumer/atlas/blob/main/docs/adr/0168-connector-work-on-a-worker.md

// Job is an AD task with everything the engine can evaluate already evaluated: the
// server, the operation, and its operands.
//
// BindSecret is a *reference*, not a password — the same reference the model authored
// (ADR-0041). Whoever runs the job resolves it against whatever secret store it has.
type Job struct {
	URL        string `json:"url"`
	BindDN     string `json:"bindDN,omitempty"`
	BindSecret string `json:"bindSecretRef,omitempty"`
	StartTLS   bool   `json:"startTLS,omitempty"`
	Operation  string `json:"operation"`
	DN         string `json:"dn"`
	MemberDN   string `json:"memberDN,omitempty"`
	NewDN      string `json:"newDN,omitempty"`
	// NewPassword is the password a set-password writes. It is the operation's own
	// data and always was a process variable (ADR-0166), so it travels — and a worker
	// leasing the job already receives the task's variables regardless, which is why
	// carrying it here adds no exposure that the lease did not already have.
	NewPassword string `json:"newPassword,omitempty"`
	// Attributes is the resolved entry for create-user, create-group and
	// update-attributes.
	Attributes map[string][]string `json:"attributes,omitempty"`
}

// Resolve turns a compiled AD connector task into a [Job]: the authored values
// evaluated against the instance's variables, and the attribute object read out of
// the named variable. It is engine work by necessity — FEEL is compiled at deploy
// (ADR-0008/0015) and only the engine has the scope.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, scope uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("ad: connector task has no detail")
	}
	scopeVars, err := readScopeVars(store, scope)
	if err != nil {
		return Job{}, fmt.Errorf("ad: read variables for scope %d: %w", scope, err)
	}
	op := cp.Intern(detail.AdOp)
	j := Job{
		URL:         resolveValue(detail.AdURL, scope, scopeVars),
		BindDN:      resolveValue(detail.AdBindDN, scope, scopeVars),
		BindSecret:  cp.Intern(detail.AdBindSecret),
		StartTLS:    detail.AdStartTLS,
		Operation:   op,
		DN:          resolveValue(detail.AdDN, scope, scopeVars),
		MemberDN:    resolveValue(detail.AdMemberDN, scope, scopeVars),
		NewDN:       resolveValue(detail.AdNewDN, scope, scopeVars),
		NewPassword: resolveValue(detail.AdNewPassword, scope, scopeVars),
	}
	if needsEntry(op) {
		attrs, err := attrsFromVar(cp.Intern(detail.AdEntryVar), scopeVars)
		if err != nil {
			return Job{}, err
		}
		j.Attributes = attrs
	}
	return j, nil
}

// needsEntry reports whether an operation reads the authored attribute object.
func needsEntry(op string) bool {
	switch op {
	case "create-user", "create-group", "update-attributes":
		return true
	default:
		return false
	}
}

// Run performs a resolved job: it resolves the bind password from the caller's own
// secret store, dials and binds, performs the operation, and closes. It is the whole
// of the worker's half, and the in-process path calls it too, so there is one
// definition of what a resolved AD task means rather than two that drift.
func Run(_ context.Context, j Job, dialer Dialer, secret SecretResolver) error {
	if j.URL == "" {
		return fmt.Errorf("ad: %s has an empty url", j.Operation)
	}
	bindPassword := ""
	if j.BindSecret != "" {
		bindPassword = resolveSecret(secret, j.BindSecret)
		if bindPassword == "" {
			return fmt.Errorf("ad: bind secret %q is not configured (set ATLAS_CONNECTOR_<REF>_TOKEN where this job runs)", j.BindSecret)
		}
	}
	conn, err := dialer.Dial(j.URL, j.BindDN, bindPassword, j.StartTLS)
	if err != nil {
		return err
	}
	defer conn.Close()
	return dispatch(j, conn)
}
