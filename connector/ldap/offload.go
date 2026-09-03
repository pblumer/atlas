package ldap

import (
	"context"
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/envname"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A resolved LDAP task, and the function that performs one.
//
// This is [ADR-0168]'s split applied to the generic directory kind, and it lands
// where Active Directory's did (ADR-0182) because the two share their shape: an LDAP
// task authors its own server URL and bind DN — they are model data (ADR-0154) — so
// those keep travelling with the job. What moves is *where the secrets behind the
// references are read*. The engine's vault stops being consulted for an offloaded
// LDAP task; the worker resolves the same `ATLAS_CONNECTOR_<REF>_TOKEN` names from
// its own environment.
//
// [Job] therefore carries the bind-password and client-certificate *references* and
// never their values — a property of the type rather than of the code filling it in,
// which is what makes "a compromised engine yields no directory credential" a claim
// a reader can check.
//
// [ADR-0168]: https://github.com/pblumer/atlas/blob/main/docs/adr/0168-connector-work-on-a-worker.md

// Job is an LDAP task with everything the engine can evaluate already evaluated: the
// server, the operation, and its operands.
type Job struct {
	URL      string `json:"url"`
	StartTLS bool   `json:"startTLS,omitempty"`
	BindDN   string `json:"bindDN,omitempty"`
	// BindSecret and ClientCertSecret are *references* (ADR-0041), not credentials.
	// Whoever runs the job resolves them against whatever secret store it has; an
	// anonymous bind names neither.
	BindSecret       string `json:"bindSecretRef,omitempty"`
	ClientCertSecret string `json:"clientCertSecretRef,omitempty"`
	Operation        string `json:"operation"`
	// DN is the entry the five mutating operations act on.
	DN string `json:"dn,omitempty"`
	// Attributes is the resolved entry object for add and the three modify flavours,
	// read out of the task's entryVariable. It is engine state — a worker has no
	// scope chain to read a process variable from — so it travels resolved.
	Attributes map[string][]string `json:"attributes,omitempty"`
	// NewPassword is what modify-password writes. It was always a process variable
	// (ADR-0154), and a worker leasing the job already receives the task's variables,
	// so carrying it here adds no exposure the lease did not already have.
	NewPassword string `json:"newPassword,omitempty"`
	// The search operands: where to read, how deep, what narrows it, what bounds the
	// pages and the total, and which variable receives the entries.
	BaseDN         string `json:"baseDN,omitempty"`
	Scope          string `json:"scope,omitempty"`
	Filter         string `json:"filter,omitempty"`
	PageSize       int32  `json:"pageSize,omitempty"`
	MaxEntries     int32  `json:"maxEntries,omitempty"`
	ResultVariable string `json:"resultVariable,omitempty"`
}

// Result is what running a Job produces: the five mutating operations write to the
// directory and have nothing to write back, a search has the entries its result
// variable takes.
type Result struct {
	ResultVariable string
	Value          any
	// HasValue separates "the model discards these entries" from "the operation
	// returns nothing": only a search with a result variable writes anything at all.
	HasValue bool
}

// Variables renders a run's result as the process variables the job completes with.
// Both halves call it, so an offloaded search and an in-engine one cannot disagree
// about what an LDAP task returns.
func (r Result) Variables() []model.VariableValue {
	if !r.HasValue || r.ResultVariable == "" {
		return nil
	}
	return []model.VariableValue{responseVariable(r.ResultVariable, r.Value)}
}

// Resolve turns a compiled LDAP task into a [Job]: the authored values evaluated
// against the instance's variables, and the attribute object read out of the named
// variable. It is engine work by necessity — FEEL is compiled at deploy
// (ADR-0008/0015) and only the engine has the scope.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("ldap: task has no detail")
	}
	// The variables the task sees, up its scope chain, so its own input-mapped locals
	// shadow what it inherits (ADR-0068).
	scopeVars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("ldap: read variables for element %d: %w", elementInstanceKey, err)
	}
	piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
	op := cp.Intern(detail.LdapOp)
	j := Job{
		URL:              resolveValue(detail.LdapURL, piKey, scopeVars),
		StartTLS:         detail.LdapStartTLS,
		BindDN:           resolveValue(detail.LdapBindDN, piKey, scopeVars),
		BindSecret:       cp.Intern(detail.LdapBindSecret),
		ClientCertSecret: cp.Intern(detail.LdapClientCertSecret),
		Operation:        op,
	}
	switch op {
	case "search":
		j.BaseDN = resolveValue(detail.LdapBaseDN, piKey, scopeVars)
		j.Scope = cp.Intern(detail.LdapScope)
		j.Filter = resolveValue(detail.LdapFilter, piKey, scopeVars)
		j.PageSize = detail.LdapPageSize
		j.MaxEntries = detail.LdapMaxEntries
		j.ResultVariable = cp.Intern(detail.ResultVar)
	default:
		// Every other operation names the entry it acts on.
		j.DN = resolveValue(detail.LdapDN, piKey, scopeVars)
		if op == "modify-password" {
			j.NewPassword = resolveValue(detail.LdapNewPassword, piKey, scopeVars)
		}
		if needsEntry(op) {
			attrs, err := attrsFromVar(cp.Intern(detail.LdapEntryVar), scopeVars)
			if err != nil {
				return Job{}, err
			}
			j.Attributes = attrs
		}
	}
	return j, nil
}

// needsEntry reports whether an operation reads the authored attribute object.
func needsEntry(op string) bool {
	switch op {
	case "add", "modify", "add-values", "delete-values":
		return true
	default:
		return false
	}
}

// Run performs a resolved job: it resolves the secrets from the caller's own store,
// dials and binds, performs the operation, and closes. It is the whole of the
// worker's half, and the in-process path calls it too, so there is one definition of
// what a resolved LDAP task means rather than two that drift.
func Run(_ context.Context, j Job, dialer Dialer, secret SecretResolver) (Result, error) {
	if j.URL == "" {
		return Result{}, fmt.Errorf("ldap: %s has an empty url", j.Operation)
	}
	bindPassword, err := credential(j.BindSecret, "bind secret", secret)
	if err != nil {
		return Result{}, err
	}
	clientCert, err := credential(j.ClientCertSecret, "client certificate secret", secret)
	if err != nil {
		return Result{}, err
	}
	conn, err := dialer.Dial(DialOptions{
		URL:          j.URL,
		StartTLS:     j.StartTLS,
		BindDN:       j.BindDN,
		BindPassword: bindPassword,
		ClientCert:   clientCert,
	})
	if err != nil {
		return Result{}, err
	}
	defer conn.Close()
	return dispatch(j, conn)
}

// credential resolves one secret reference where the job runs. An empty reference is
// no credential rather than a missing one — an anonymous bind names none.
func credential(ref, what string, secret SecretResolver) (string, error) {
	if ref == "" {
		return "", nil
	}
	value := resolveSecret(secret, ref)
	if value == "" {
		// The variable is named as it is spelled, not as a pattern for an operator to
		// apply in their head: the fold is not obvious for a reference carrying
		// punctuation. Both ways out are named because both are real — the Console
		// vault reaches a worker Atlas supervises, and the environment is what a
		// worker you run yourself reads.
		return "", fmt.Errorf("ldap: %s %q is not configured where this job runs: store it under that name in Console > Workers > Secrets, or set %s in the environment of the worker that leases ldap jobs",
			what, ref, envname.ConnectorToken(ref))
	}
	return value, nil
}

// dispatch performs the resolved operation over the bound connection.
func dispatch(j Job, conn Conn) (Result, error) {
	switch j.Operation {
	case "search":
		entries, err := conn.Search(SearchRequest{
			BaseDN:     j.BaseDN,
			Scope:      j.Scope,
			Filter:     j.Filter,
			PageSize:   j.PageSize,
			MaxEntries: j.MaxEntries,
		})
		if err != nil {
			return Result{}, err
		}
		return Result{ResultVariable: j.ResultVariable, Value: entriesToJSON(entries), HasValue: true}, nil
	case "add":
		return Result{}, conn.Add(j.DN, j.Attributes)
	case "modify", "add-values", "delete-values":
		if len(j.Attributes) == 0 {
			return Result{}, fmt.Errorf("ldap: %s resolved no attributes to change", j.Operation)
		}
		return Result{}, conn.Modify(j.DN, modsFor(j.Operation, j.Attributes))
	case "delete":
		return Result{}, conn.Delete(j.DN)
	case "modify-password":
		if j.NewPassword == "" {
			return Result{}, fmt.Errorf("ldap: modify-password resolved an empty newPassword")
		}
		return Result{}, conn.SetPassword(j.DN, j.NewPassword)
	default:
		return Result{}, fmt.Errorf("ldap: unknown operation %q", j.Operation)
	}
}
