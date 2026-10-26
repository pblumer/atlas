package compiler

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/connector/googlecontacts/catalog"
)

// compileGoogleContactsConnectorTask compiles an <atlas:googleContactsConnector> task:
// one operation on an address book through a Worker an operator configured
// (ADR-0476).
//
// The operation table is connector/googlecontacts/catalog's, read here directly rather
// than restated, for the reason Exchange's is: the catalog imports nothing of Atlas's,
// so the rules a deploy enforces are the very rows the worker runs.
func compileGoogleContactsConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.GoogleContacts
	fail := func(format string, args ...any) (int32, error) {
		return 0, fmt.Errorf("compiler: google contacts task %q "+format, append([]any{st.Id}, args...)...)
	}
	if strings.TrimSpace(cn.Connector) == "" {
		return fail("needs a connector attribute naming the Google Contacts Worker whose address book it uses")
	}
	// The Worker is normally a static name, and may be FEEL so one process can reach
	// more than one address book — the one of the account manager a case belongs to —
	// choosing from its own variables at call time.
	workerExpr, err := connectorValue(b.gate(), st.Id, "google contacts worker", "connector", cn.Connector)
	if err != nil {
		return 0, err
	}
	name := strings.ToLower(strings.TrimSpace(cn.Operation))
	if name == "" {
		return fail("needs an operation (%s)", strings.Join(catalog.Names(), ", "))
	}
	op, ok := catalog.Lookup(name)
	if !ok {
		return fail("has an unknown operation %q (want %s)", cn.Operation, strings.Join(catalog.Names(), ", "))
	}

	inline := strings.TrimSpace(cn.Contact) != ""
	fromVar := strings.TrimSpace(cn.ContactVariable) != ""
	// One pass over every authored value: required where the operation needs it,
	// refused where it does not use it, so a value is never silently ignored.
	for _, v := range []struct {
		attr     string
		set      bool
		required bool
		allowed  bool
		why      string
	}{
		{"contactId", strings.TrimSpace(cn.ContactID) != "", op.NeedsContactID, op.NeedsContactID, "the contact it acts on, as its id reads (people/c…)"},
		{"query", strings.TrimSpace(cn.Query) != "", op.NeedsQuery, op.NeedsQuery, "what it looks for"},
		{"field", strings.TrimSpace(cn.Field) != "", false, op.TakesField, "the custom field a find compares the query with"},
		{"group", strings.TrimSpace(cn.Group) != "", op.NeedsGroup, op.NeedsGroup || op.TakesGroup, "the label, by its name"},
		{"contact", inline || fromVar, op.NeedsContact, op.NeedsContact, "the contact's values, inline or in contactVariable"},
		{"maxResults", strings.TrimSpace(cn.MaxResults) != "", false, op.MaxResults.Max > 0, "how many contacts the result may hold"},
		{"resultVariable", strings.TrimSpace(cn.ResultVariable) != "", op.NeedsResult, op.TakesResult(), "the process variable receiving the answer"},
	} {
		switch {
		case v.required && !v.set:
			if v.attr == "resultVariable" {
				return fail("operation %q needs a resultVariable (a read that discards its result is one nothing asked for)", name)
			}
			return fail("operation %q needs %s (%s)", name, v.attr, v.why)
		case v.set && !v.allowed:
			if v.attr == "resultVariable" {
				return fail("operation %q returns nothing, so a resultVariable would name a value that is never written; remove it", name)
			}
			return fail("operation %q does not use %s (%s); remove it rather than leaving a value the Worker ignores", name, v.attr, v.why)
		}
	}
	if inline && fromVar {
		return fail("sets both an inline contact and a contactVariable; use one, not both")
	}

	var contact RestExpr
	if inline {
		template, err := googleContactsTemplate(st.Id, cn.Contact)
		if err != nil {
			return 0, err
		}
		if err := catalog.CheckContact(name, template); err != nil {
			return fail("%s", strings.TrimPrefix(err.Error(), "googlecontacts: "))
		}
		feel, err := jsonTemplateToFeel(template)
		if err != nil {
			return fail("contact: %v", err)
		}
		e, err := b.gate().compileFEEL(feel)
		if err != nil {
			return fail("contact did not compile (a value's =expression may be malformed): %v", err)
		}
		contact = RestExpr{Expr: e}
	}
	maxResults, err := googleContactsMaxResults(st.Id, name, op, cn.MaxResults)
	if err != nil {
		return 0, err
	}
	cfg := GoogleContactsConfig{
		Worker:     strings.TrimSpace(cn.Connector),
		WorkerExpr: workerExpr,
		Op:         name,
		Contact:    contact,
		ContactVar: strings.TrimSpace(cn.ContactVariable),
		MaxResults: maxResults,
		ResultVar:  strings.TrimSpace(cn.ResultVariable),
		Retries:    retries,
	}
	// A contact id that is written out literally is checked here, where it is still
	// the author reading the message; one read from a variable is checked by the worker.
	if id := strings.TrimSpace(cn.ContactID); id != "" && !strings.HasPrefix(id, "=") {
		if _, err := catalog.ContactResourceName(id); err != nil {
			return fail("%s", strings.TrimPrefix(err.Error(), "googlecontacts: "))
		}
	}
	for _, v := range []struct {
		what string
		raw  string
		into *RestExpr
	}{
		{"contactId", cn.ContactID, &cfg.ContactID},
		{"query", cn.Query, &cfg.Query},
		{"field", cn.Field, &cfg.Field},
		{"group", cn.Group, &cfg.Group},
	} {
		if strings.TrimSpace(v.raw) == "" {
			continue
		}
		val, err := connectorValue(b.gate(), st.Id, "google contacts", v.what, strings.TrimSpace(v.raw))
		if err != nil {
			return 0, err
		}
		*v.into = val
	}
	return b.AddGoogleContactsConnectorTask(cfg), nil
}

// googleContactsTemplate decodes the authored contact: a JSON object, its numbers kept
// as numbers.
func googleContactsTemplate(taskID, raw string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("compiler: google contacts task %q has an invalid contact JSON: %w", taskID, err)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("compiler: google contacts task %q contact must be a JSON object of contact values, not a %T", taskID, v)
	}
	return obj, nil
}

// googleContactsMaxResults reads the listing bound: the operation's default when
// absent, the authored number otherwise, within the operation's own ceiling — for a
// search that ceiling is Google's (30 contacts per search), for a listing it is what a
// process variable is meant to hold.
func googleContactsMaxResults(taskID, name string, op catalog.Op, raw string) (int32, error) {
	raw = strings.TrimSpace(raw)
	if op.MaxResults.Max == 0 {
		return 0, nil
	}
	if raw == "" {
		return op.MaxResults.Default, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("compiler: google contacts task %q has a non-numeric maxResults %q", taskID, raw)
	}
	if n < 1 {
		return 0, fmt.Errorf("compiler: google contacts task %q has a maxResults of %d; it must be at least 1", taskID, n)
	}
	if n > int(op.MaxResults.Max) {
		return 0, fmt.Errorf("compiler: google contacts task %q has a maxResults of %d on %s, above its ceiling of %d", taskID, n, name, op.MaxResults.Max)
	}
	return int32(n), nil
}
