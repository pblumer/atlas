// Package limits is the one place that names every resource budget in an Atlas
// installation, and the one way to change them.
//
// Before this package the budgets existed but nowhere in particular: thirty-odd
// `max…Bytes` constants spread across as many files, a handful of bare literals at
// the call site, and two engine budgets reachable only through a setter no
// deployment could call. Each one was defensible where it stood. Together they were
// not a policy, because nothing said what the set was — and a set nobody can
// enumerate is a set nobody notices a hole in. The audit of 2026-09-07 found the
// hole: an unbounded allocation driven by an instance variable, sitting between
// twenty neighbours that were bounded.
//
// So the shape here is the one the store registry uses for durable directories
// (ADR-0219): a single value that names every budget, defaults that are exactly the
// numbers the code used before it existed, and a test that fails when a new ceiling
// appears anywhere without a name in this file. The test is the part that lasts.
// A list is only as good as what stops it from going stale.
//
// # What is a budget and what is not
//
// A budget here bounds *externally supplied* input against memory: how much a
// caller, a called service, or a running process may make this server hold at once.
// Ceilings that express a domain rule — how many search results a page returns, how
// long an API token may live, how many rows a preview shows — are not budgets and
// do not belong here; they are answers about the product, and moving them into an
// operator-facing knob would invite tuning the product from the environment.
//
// # Configuring
//
// [FromEnv] reads one variable per field, `ATLAS_LIMIT_` plus the field name in
// upper snake case (`ATLAS_LIMIT_MODEL_UPLOAD`). A variable that is unset, empty,
// or unparseable leaves the default standing rather than zeroing the budget: a typo
// in a deployment's environment must not silently remove a ceiling, which is the
// failure mode the whole package exists to prevent.
package limits

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
)

// Limits is a complete set of resource budgets. The zero value is not usable —
// every field is a ceiling, and a ceiling of zero admits nothing — so build one with
// [Default] or [FromEnv] and override individual fields from there.
type Limits struct {
	// ErrorBody is how much of a failed HTTP response is kept to put in the error
	// message. It is small on purpose: the value is a diagnostic quotation, not the
	// answer, and the server it came from is already misbehaving.
	ErrorBody int64

	// Theme is a UI theme document — a small set of colours and names. Kept apart
	// from Settings because it is served to every page load.
	Theme int64

	// Registration is a self-service handshake body: an OAuth client registering
	// itself (RFC 7591), a node announcing itself. Deliberately the tightest budget
	// on an authenticated-optional path.
	Registration int64

	// Request is one ordinary JSON request or the answer of a peer Atlas: a user,
	// a FEEL evaluation, a folder, an OIDC claim mapping. The great majority of the
	// API lives here.
	Request int64

	// Settings is a configuration document a person edits: mock configuration, a
	// script's source with its sample variables, a public start form's payload.
	Settings int64

	// Asset is a binary a person uploads for display — a logo — or a generated form
	// answer holding one.
	Asset int64

	// Definition is a modelled artefact in JSON: a form, a collaboration session's
	// payload, a playground request, the answer of a service a process called.
	Definition int64

	// Generated is a document this server produced or is asked to render back:
	// a generated form request, a mail outbox post.
	Generated int64

	// ModelUpload is a BPMN, DMN or XOML document. Four megabytes is a diagram far
	// past what a person maintains by hand.
	ModelUpload int64

	// Payload is the largest thing that legitimately travels in one piece: a model
	// provider's answer, a playground model, a mock's journal, a script's output.
	Payload int64

	// DataUpload is tabular or scenario data: a CSV, a playground scenario.
	DataUpload int64

	// Import is a document imported from another tool — an information model, a
	// process documentation bundle.
	Import int64

	// AppBundle is an application's whole source or export.
	AppBundle int64

	// Archive is a backup or snapshot being restored, compressed *and* decompressed:
	// the same ceiling bounds both, so a small archive cannot expand past it.
	Archive int64

	// TokenSteps is how many element activations one token may drive in a single run
	// before the engine parks it with an incident — a *rate* budget, protecting the
	// single writer from a cycle of automatic elements (ADR-0272).
	TokenSteps int32

	// Iterations is how many iterations one multi-instance activity may ask for
	// before the engine refuses it — a *size* budget, protecting the allocation from
	// a number that came out of an instance variable (ADR-0276). Neither substitutes
	// for the other: a hundred thousand iterations are a hundred thousand tokens
	// taking one step each, which TokenSteps is deliberately built not to stop.
	Iterations int32

	// Variable is how large one process variable's value may be. A variable is a
	// business record — a customer, an order, the inputs of a decision. Anything
	// larger than this is a document, and a document does not belong in a token's
	// scope, where every touch rewrites the whole of it into the log
	// (ADR-draft-a-variable-is-a-record).
	Variable int64

	// Collection is how large a multi-instance activity's assembled output collection
	// may be. It is a *separate* budget from Variable, and larger, because the two
	// bound different things: Variable asks what one record may weigh, Collection what
	// a legitimate loop at the iteration ceiling may accumulate. One number cannot do
	// both — the collection would have to be small enough to be no ceiling for a
	// record, or the record large enough to be no ceiling at all.
	//
	// It bounds the peak held in memory. It is deliberately *not* a bound on what the
	// loop writes: the collection is re-serialised once per iteration, so the bytes
	// written grow with the square of the iteration count, and a budget small enough
	// to make that safe would be smaller than Variable. That is a defect with its own
	// fix, not a number to hide in.
	Collection int64
}

// Default returns the budgets an installation runs with when it says nothing. Each
// number is the one the code carried before this package existed; moving them here
// changed where they are written down, not what they are.
func Default() Limits {
	return Limits{
		ErrorBody:    4 << 10,
		Theme:        4 << 10,
		Registration: 16 << 10,
		Request:      64 << 10,
		Settings:     256 << 10,
		Asset:        512 << 10,
		Definition:   1 << 20,
		Generated:    2 << 20,
		ModelUpload:  4 << 20,
		Payload:      8 << 20,
		DataUpload:   16 << 20,
		Import:       24 << 20,
		AppBundle:    32 << 20,
		Archive:      1 << 30,
		TokenSteps:   10_000,
		Iterations:   100_000,
		Variable:     1 << 20,
		Collection:   16 << 20,
	}
}

// EnvPrefix is what every budget's environment variable starts with.
const EnvPrefix = "ATLAS_LIMIT_"

// Names lists every budget, in the order [Limits] declares them. It is derived from
// the struct rather than written out, so a budget added to Limits is configurable
// and documented the moment it exists — there is no second list to keep in step,
// which is the failure this package was built to end rather than to repeat.
func Names() []string {
	t := reflect.TypeOf(Limits{})
	out := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		out = append(out, t.Field(i).Name)
	}
	return out
}

// EnvVar is the environment variable that sets the named budget:
// "ModelUpload" becomes "ATLAS_LIMIT_MODEL_UPLOAD".
func EnvVar(field string) string { return EnvPrefix + upperSnake(field) }

// FromEnv returns [Default] with every budget the environment names applied on top.
// A variable that is unset, empty, unparseable, not positive, or too large for the
// field leaves that budget at its default and is reported in the returned slice —
// the caller logs those, so a typo is visible at startup rather than at the first
// request that needed the ceiling. It is deliberately not an error: a malformed knob
// must not stop a server from coming up, and it must never be the reason a ceiling
// is missing.
func FromEnv() (Limits, []string) {
	return fromEnviron(os.Getenv)
}

// fromEnviron is FromEnv with the lookup injected, so the tests can exercise every
// rejection without touching the process environment.
func fromEnviron(get func(string) string) (Limits, []string) {
	l := Default()
	v := reflect.ValueOf(&l).Elem()
	t := v.Type()
	var bad []string
	for i := range t.NumField() {
		key := EnvVar(t.Field(i).Name)
		raw := strings.TrimSpace(get(key))
		if raw == "" {
			continue
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		switch {
		case err != nil || n <= 0:
			bad = append(bad, fmt.Sprintf("%s=%q is not a positive number; keeping the default", key, raw))
		case v.Field(i).OverflowInt(n):
			bad = append(bad, fmt.Sprintf("%s=%q is larger than this budget can hold; keeping the default", key, raw))
		default:
			v.Field(i).SetInt(n)
		}
	}
	return l, bad
}

// upperSnake turns a Go field name into the environment's spelling:
// "ModelUpload" -> "MODEL_UPLOAD".
func upperSnake(field string) string {
	var b strings.Builder
	for i, r := range field {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToUpper(b.String())
}
