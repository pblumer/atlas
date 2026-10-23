// Package lookup is the wire shape of a form lookup: what the server asks a Worker Type
// when a person searches a remote data source from a task form, and what the Worker
// Type answers (ADR-0473).
//
// A lookup is not a job. It is never written to the log, it has no element instance,
// no retries and no incident; it lives as long as the HTTP request that asked for it.
// What it shares with a job is the boundary: it is answered by a Worker Instance with
// the Worker's own credential, so the engine still holds none.
//
// The shape is structured on purpose. The server never hands a Worker Type a URL or a
// query string: it names a resource, the properties to search, the term, the equality
// filters and the properties to return, and the Worker Type builds its own request from
// its own allowlist. That is what keeps a form — and a person typing into one — from
// choosing what a privileged credential reads.
package lookup

import (
	"fmt"
	"strings"
	"unicode"
)

// Query is one lookup as a Worker Type receives it.
type Query struct {
	// Resource names what is searched, in the Worker Type's own vocabulary: "users" for
	// Entra.
	Resource string `json:"resource"`
	// Value asks for the one record whose value property equals it, instead of a
	// search: the check a submission makes before a task completes, and what a reopened
	// task asks to show the label of a value it already holds. Search, SearchFields and
	// Next are ignored when it is set; Filters still apply, so a record outside them is
	// not found.
	Value string `json:"value,omitempty"`
	// Search is the person's term, matched as a prefix of each of SearchFields. Empty
	// lists the first page of what Filters admit.
	Search       string   `json:"search,omitempty"`
	SearchFields []string `json:"searchFields,omitempty"`
	// Filters are equality constraints, all of which must hold.
	Filters []Filter `json:"filters,omitempty"`
	// ActiveOnly leaves out records the provider marks as disabled.
	ActiveOnly bool `json:"activeOnly,omitempty"`
	// Properties are what each returned record may carry, and nothing else.
	Properties []string `json:"properties"`
	// PageSize bounds one answer.
	PageSize int `json:"pageSize,omitempty"`
	// Next continues the listing a previous answer to this same query left off at. It
	// is the provider's own continuation, passed back verbatim; the server seals it so
	// a person cannot alter it, and the Worker Type still refuses one that does not
	// continue this resource on its own endpoint.
	Next string `json:"next,omitempty"`
}

// Filter is one equality constraint.
type Filter struct {
	Property string `json:"property"`
	Value    string `json:"value"`
}

// Page is one answer: at most PageSize records, each restricted to the properties the
// query named, and the continuation of the listing when there is more.
type Page struct {
	Items []map[string]any `json:"items"`
	Next  string           `json:"next,omitempty"`
}

// The codes a Worker Type answers a lookup with when it has no page. They are the
// whole of what crosses the wire about a failure: a provider's own error carries its
// request — and the request carries the person's search term — so no message travels
// with them. The server words what the person reads.
const (
	// CodeInvalid: the query breaks the Worker Type's own rules — a property it does
	// not allow, a page too large, a malformed value.
	CodeInvalid = "invalid_query"
	// CodeUnconfigured: this Worker Instance holds no Worker by the name asked for.
	CodeUnconfigured = "worker_unconfigured"
	// CodeThrottled: the provider asked to be left alone for a while; RetryAfter says
	// how long when it said.
	CodeThrottled = "provider_throttled"
	// CodeUnavailable: the provider failed or could not be reached.
	CodeUnavailable = "provider_unavailable"
	// CodeRefused: the provider refused the request or the Worker's credential — a
	// permission the app registration lacks, a query it does not run.
	CodeRefused = "provider_refused"
)

// Error is a failed lookup as a Worker Type reports it.
type Error struct {
	Code string `json:"code"`
	// RetryAfter is in seconds; zero when the provider gave no advice.
	RetryAfter int `json:"retryAfter,omitempty"`
}

func (e *Error) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("lookup: %s (retry after %ds)", e.Code, e.RetryAfter)
	}
	return "lookup: " + e.Code
}

// Fail is an [Error] with a code and nothing else.
func Fail(code string) *Error { return &Error{Code: code} }

// The bounds every lookup is held to, whoever builds it. They are product rules rather
// than installation budgets: a page a person reads, a term a person types.
const (
	// MaxPageSize is the most records one answer may carry.
	MaxPageSize = 50
	// DefaultPageSize is what a field that names none gets.
	DefaultPageSize = 25
	// MaxSearchLength bounds a term in characters. A name or an address is far
	// shorter; a longer term is not a search.
	MaxSearchLength = 64
	// MaxValueLength bounds a filter value or a looked-up value in characters.
	MaxValueLength = 128
)

// CleanText reports whether s may travel as a term or a value: no control characters,
// which no name or address contains and which a request builder should never have to
// think about.
func CleanText(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// NormalizeSearch trims a term and collapses its inner whitespace, so "  Anna   Mü "
// and "Anna Mü" are one query — for the provider, and for the cursor that continues it.
func NormalizeSearch(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
