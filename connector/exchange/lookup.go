package exchange

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/pblumer/atlas/connector/exchange/catalog"
	"github.com/pblumer/atlas/connector/lookup"
)

// A recipient lookup answers the one question a name field asks of Exchange: does a
// recipient already hold this alias (ADR-draft-name-availability-check)? A Microsoft
// 365 group, a shared mailbox and a distribution list all take their address from their
// alias, so a name that Entra reports free can still collide with a mailbox an
// administrator created by hand — Exchange is the third place to ask.
//
// Like the Entra and SharePoint lookups it rides the form-lookup protocol of ADR-0473 —
// an ephemeral query answered with the Worker's own credential, never a job — and it is
// a value lookup and nothing else. The filter is written here, from an alias this file
// has already held to a shape with no quote in it; a form and a person never hand this
// Worker OPATH.

// LookupRecipients is the one resource a recipient lookup reads.
const LookupRecipients = "recipients"

// LookupReadable are the properties a recipient lookup may return.
var LookupReadable = []string{"Alias", "RecipientTypeDetails"}

// alias is what a recipient's alias may be in a lookup: letters, digits, '.', '-' and
// '_', starting with a letter or a digit. Exchange allows a few more characters in an
// alias; a name an order form offers does not need them, and refusing them keeps every
// value safe to place between the quotes of an OPATH literal.
var alias = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidAlias reports whether v can be looked up as an alias.
func ValidAlias(v string) bool { return alias.MatchString(v) }

// CheckLookup holds a recipient lookup to what this Worker Type answers: a value lookup
// on recipients, with nothing else asked.
func CheckLookup(q lookup.Query) error {
	if q.Resource != LookupRecipients || !ValidAlias(q.Value) {
		return lookup.Fail(lookup.CodeInvalid)
	}
	if q.Search != "" || len(q.SearchFields) > 0 || q.Next != "" || q.ActiveOnly || len(q.Filters) > 0 {
		return lookup.Fail(lookup.CodeInvalid)
	}
	if q.PageSize < 0 || q.PageSize > lookup.MaxPageSize {
		return lookup.Fail(lookup.CodeInvalid)
	}
	for _, p := range q.Properties {
		if !slices.Contains(LookupReadable, p) {
			return lookup.Fail(lookup.CodeInvalid)
		}
	}
	return nil
}

// recipientFilter is the OPATH filter a lookup sends: the alias itself, or any SMTP
// address whose local part is the alias. The second clause is what catches a mailbox
// whose alias differs from its address — "fp-fin" at kreditoren@…, or the reverse.
// Whether Exchange Online evaluates -like on EmailAddresses with a trailing wildcard in
// every tenant is Microsoft's documentation of filterable properties, not checked
// against one (the record's open question).
func recipientFilter(value string) string {
	v := strings.ReplaceAll(value, "'", "''") // ValidAlias admits no quote; kept for safety
	return "(Alias -eq '" + v + "') -or (EmailAddresses -like 'smtp:" + v + "@*')"
}

// RunLookup answers a recipient lookup against the organization connector names: one
// item when a recipient holds the alias, none when it is free. It runs Get-Recipient,
// which both Exchange Online and Exchange Server have under that name.
func RunLookup(ctx context.Context, connector string, q lookup.Query, reg *Registry) (lookup.Page, error) {
	backend, ok := reg.Client(connector)
	if !ok {
		return lookup.Page{}, lookup.Fail(lookup.CodeUnconfigured)
	}
	if err := CheckLookup(q); err != nil {
		return lookup.Page{}, err
	}
	inv := catalog.Invocation{Cmdlet: "Get-Recipient", Parameters: map[string]any{
		"Filter":                    recipientFilter(q.Value),
		catalog.ResultSizeParameter: 1,
	}}
	out, err := backend.Invoke(ctx, inv, 1)
	if err != nil {
		// The error carries the filter, which carries what the person typed; only the
		// code leaves this function.
		return lookup.Page{}, recipientLookupFailure(ctx, err)
	}
	items := []map[string]any{}
	if len(out) > 0 {
		rec, _ := out[0].(map[string]any)
		item := map[string]any{}
		for _, p := range q.Properties {
			item[p] = rec[p]
		}
		items = append(items, item)
	}
	return lookup.Page{Items: items}, nil
}

// recipientLookupFailure turns a failed Get-Recipient into a lookup code. The backends'
// errors are text written for an incident; the status is read from the phrase the
// online backend always writes, and anything else — Remote PowerShell included — is the
// provider being unavailable.
func recipientLookupFailure(ctx context.Context, err error) error {
	msg := err.Error()
	switch {
	case ctx.Err() != nil:
		return lookup.Fail(lookup.CodeUnavailable)
	case strings.Contains(msg, "returned HTTP 401"), strings.Contains(msg, "returned HTTP 403"):
		return lookup.Fail(lookup.CodeRefused)
	case strings.Contains(msg, "returned HTTP 429"):
		return lookup.Fail(lookup.CodeThrottled)
	default:
		return lookup.Fail(lookup.CodeUnavailable)
	}
}
