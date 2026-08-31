package ad

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/envname"
	"github.com/pblumer/atlas/model"
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
	// Connector is the Console-configured directory this job talks to. When it is set
	// the worker holds that directory's URL and bind credentials under this name and
	// URL/BindDN/BindSecret below are empty — the shape every other credential-bearing
	// kind already uses (ADR-0206). When it is empty the job
	// carries the directory itself, which is how models written before that read.
	Connector  string `json:"connector,omitempty"`
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
	// The sync operation's own fields. Cookie is base64 of the server's opaque resume
	// token, because a process variable holds text and the token is binary.
	// CookieVariable is where the *new* cookie is written back, which is what lets a
	// reconciliation loop carry itself forward without any state in the connector.
	BaseDN         string `json:"baseDN,omitempty"`
	Filter         string `json:"filter,omitempty"`
	Cookie         string `json:"cookie,omitempty"`
	CookieVariable string `json:"cookieVariable,omitempty"`
	MaxEntries     int    `json:"maxEntries,omitempty"`
	ObjectSecurity bool   `json:"objectSecurity,omitempty"`
	ResultVariable string `json:"resultVariable,omitempty"`
}

// Resolve turns a compiled AD connector task into a [Job]: the authored values
// evaluated against the instance's variables, and the attribute object read out of
// the named variable. It is engine work by necessity — FEEL is compiled at deploy
// (ADR-0008/0015) and only the engine has the scope.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("ad: connector task has no detail")
	}
	// The variables the task sees, up its scope chain, so its own input-mapped locals
	// shadow what it inherits (ADR-0068).
	scopeVars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("ad: read variables for element %d: %w", elementInstanceKey, err)
	}
	piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
	op := cp.Intern(detail.AdOp)
	j := Job{
		Connector:   cp.Intern(detail.Connector),
		URL:         resolveValue(detail.AdURL, piKey, scopeVars),
		BindDN:      resolveValue(detail.AdBindDN, piKey, scopeVars),
		BindSecret:  cp.Intern(detail.AdBindSecret),
		StartTLS:    detail.AdStartTLS,
		Operation:   op,
		DN:          resolveValue(detail.AdDN, piKey, scopeVars),
		MemberDN:    resolveValue(detail.AdMemberDN, piKey, scopeVars),
		NewDN:       resolveValue(detail.AdNewDN, piKey, scopeVars),
		NewPassword: resolveValue(detail.AdNewPassword, piKey, scopeVars),
	}
	if needsEntry(op) {
		attrs, err := attrsFromVar(cp.Intern(detail.AdEntryVar), scopeVars)
		if err != nil {
			return Job{}, err
		}
		j.Attributes = attrs
	}
	if op == "sync" {
		j.BaseDN = resolveValue(detail.AdBaseDN, piKey, scopeVars)
		j.Filter = resolveValue(detail.AdFilter, piKey, scopeVars)
		j.MaxEntries = int(detail.AdMaxEntries)
		j.ObjectSecurity = detail.AdObjectSecurity
		j.ResultVariable = cp.Intern(detail.ResultVar)
		j.CookieVariable = cp.Intern(detail.AdCookieVar)
		// An unset cookie variable is the first pass, not an error: a reconciliation
		// starts by reading everything, and the variable exists from then on.
		if v, ok := scopeVars[j.CookieVariable]; ok && v.Kind == model.VarString {
			j.Cookie = v.Text
		}
	}
	return j, nil
}

// needsEntry reports whether an operation reads the authored attribute object.
func needsEntry(op string) bool {
	switch op {
	case "create-user", "create-group", "create-contact", "update-attributes":
		return true
	default:
		return false
	}
}

// Run performs a resolved job: it resolves the bind password from the caller's own
// secret store, dials and binds, performs the operation, and closes. It is the whole
// of the worker's half, and the in-process path calls it too, so there is one
// definition of what a resolved AD task means rather than two that drift.
func Run(_ context.Context, j Job, dialer Dialer, secret SecretResolver, dirs *Registry) (map[string]any, error) {
	url, bindDN, bindPassword, startTLS, err := target(j, secret, dirs)
	if err != nil {
		return nil, err
	}
	conn, err := dialer.Dial(url, bindDN, bindPassword, startTLS)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if j.Operation == "sync" {
		return runSync(j, conn)
	}
	return nil, dispatch(j, conn)
}

// target resolves which directory this job talks to and how it binds, from whichever
// of the two shapes the task carries.
//
// A named connector is looked up in the registry the worker was built with; a task
// that carries its own url resolves its bind password out of the worker's secrets, as
// it always did. The two are deliberately not blended — a task compiles as one or the
// other (the compiler refuses both), so there is no precedence rule here for a reader
// to get wrong.
func target(j Job, secret SecretResolver, dirs *Registry) (url, bindDN, password string, startTLS bool, err error) {
	if name := strings.TrimSpace(j.Connector); name != "" {
		if dirs == nil {
			return "", "", "", false, fmt.Errorf("ad: this job names the directory %q, but no directories are configured where it runs; is this server offloading the ad kind to a worker that holds them?", name)
		}
		d, ok := dirs.Client(name)
		if !ok {
			return "", "", "", false, dirs.Unresolved("ad", name)
		}
		if d.URL == "" {
			return "", "", "", false, fmt.Errorf("ad: directory %q has no url", name)
		}
		return d.URL, d.BindDN, d.Password, d.StartTLS, nil
	}
	if j.URL == "" {
		return "", "", "", false, fmt.Errorf("ad: %s has an empty url", j.Operation)
	}
	if j.BindSecret != "" {
		password = resolveSecret(secret, j.BindSecret)
		if password == "" {
			// The variable is named as it is spelled, not as a pattern to apply in your
			// head. An operator meeting this has one question — *what do I set* — and
			// ATLAS_CONNECTOR_<REF>_TOKEN answers it only if they perform the fold
			// correctly on a reference that may carry punctuation. Both ways out are
			// named because both are real: the Console vault reaches a worker Atlas
			// supervises, and the environment is what a worker you run yourself reads.
			return "", "", "", false, fmt.Errorf("ad: bind secret %q is not configured where this job runs: store it under that name in Console > Connectors > Secrets, or set %s in the environment of the worker that leases ad jobs",
				j.BindSecret, envname.ConnectorToken(j.BindSecret))
		}
	}
	return j.URL, j.BindDN, password, j.StartTLS, nil
}

// runSync performs one DirSync pass and returns the variables it completes with: the
// changes, and the cookie the *next* pass must present.
//
// The cookie is written back into the same variable it was read from, so a
// reconciliation modelled as a loop — sync, handle the changes, wait on a timer,
// sync again — carries its own position forward with no state anywhere in the
// connector or the engine.
func runSync(j Job, conn Conn) (map[string]any, error) {
	cookie, err := base64.StdEncoding.DecodeString(j.Cookie)
	if err != nil {
		return nil, fmt.Errorf("ad: the sync cookie in %q is not the value a previous pass wrote: %w", j.CookieVariable, err)
	}
	var flags int64
	if j.ObjectSecurity {
		flags |= goldap.DirSyncObjectSecurity
	}
	res, err := conn.DirSync(DirSyncRequest{
		BaseDN:     j.BaseDN,
		Filter:     j.Filter,
		Cookie:     cookie,
		Flags:      flags,
		MaxEntries: int32(j.MaxEntries),
	})
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		j.ResultVariable: map[string]any{
			"entries": entriesToJSON(res.Entries),
			// more says the server has further changes waiting right now, so a loop
			// can go straight round again instead of waiting for its timer.
			"more": res.More,
		},
	}
	if j.CookieVariable != "" {
		out[j.CookieVariable] = base64.StdEncoding.EncodeToString(res.Cookie)
	}
	return out, nil
}

// entriesToJSON turns delta entries into a JSON-ready slice: each entry is
// {"dn": …, "attributes": {name: [values]}}, the same shape the LDAP connector's
// search writes, so a process reads both the same way.
//
// A deleted object arrives here like any other entry, carrying isDeleted=TRUE — AD
// reports a deletion as a change rather than as an absence, and flattening that away
// would lose the only signal a leaver has.
func entriesToJSON(entries []Entry) any {
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		attrs := make(map[string]any, len(e.Attributes))
		for name, vals := range e.Attributes {
			attrs[name] = vals
		}
		out = append(out, map[string]any{"dn": e.DN, "attributes": attrs})
	}
	return out
}
