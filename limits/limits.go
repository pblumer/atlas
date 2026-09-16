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
	// (ADR-0294).
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

	// DirectorySync is one directory-synchronisation message: the accounts and
	// groups a scheduled delta read of Entra reports back for this server to write
	// (ADR-0332). It is its own budget rather than
	// Payload's because the two bound different risks — Payload bounds one answer a
	// process received, this bounds the one message that may create accounts.
	DirectorySync int64

	// DirectoryObjects is how many directory objects (accounts plus groups) one such
	// message may carry. It is the batch ceiling, and it is a count and not a size
	// because what it protects is not memory: the decision and the writes happen in a
	// single run-loop turn, so every object in a message is time the engine's single
	// writer spends on this instead of on process execution. A message above the
	// ceiling is refused whole rather than truncated — a short change set is a wrong
	// answer that would then be recorded as complete by advancing the cursor.
	DirectoryObjects int32

	// DirectoryReport is how many individually named lines a synchronisation report
	// may carry. The report exists to be read by a person before a first run is
	// applied, and a report nobody finishes reading is one nobody reads: the counts
	// are unbounded because they are numbers, and this bounds the lines. What does not
	// fit is counted, never silently dropped.
	DirectoryReport int32

	// InventoryLoad is one commissioning-load message: the rights a reading of one
	// target system found, reported here to be written down as pre-existing
	// (ADR-0333). Its own budget rather than Payload's
	// for the same reason DirectorySync is — Payload bounds one answer a process
	// received, this bounds the one message that may write evidence kept for years.
	InventoryLoad int64

	// InventoryObservations is how many rights one such message may carry. A count
	// and not a size, because what it protects is not memory: the decision, the
	// inventory reads it makes and the writes all happen in a single run-loop turn,
	// so every observation is time the engine's single writer spends on this instead
	// of on process execution. A message above the ceiling is refused whole rather
	// than truncated — and here truncation would be worse than elsewhere, since the
	// load's own record would then say a system was loaded when part of it was not.
	InventoryObservations int32

	// InventoryReport is how many individually named lines a load's report may carry.
	// Distinct from DirectoryReport because the two reports are read at different
	// lengths: a directory report's body is exceptions, while a load's body is the
	// grants themselves — that is what a person is being asked to authorise, and a
	// report showing only the exceptions would ask them to approve a number.
	InventoryReport int32

	// Reconcile is one reconciliation message: what a target system was found to
	// hold within a declared scope (ADR-0334).
	Reconcile int64

	// ReconcileObservations is how many rights one such reading may carry. A count,
	// like the load's, and refused whole for a sharper reason than the load's: a
	// truncated reading is a reading that is *not complete for its scope*, and this
	// endpoint reads absence as a finding. Half a group's members reported as the
	// whole group is a report that everybody in the other half has lost their
	// access.
	ReconcileObservations int32

	// ReconcileReport is how many individually named findings one run's answer may
	// carry, notes included. What a person can absorb is a number of lines, not a
	// number of lines per category, so the two share this one.
	ReconcileReport int32

	// ReconcileJournal is how many disagreements may stand open at once. It is a
	// ceiling on a *store* rather than on a message, which no other budget here is,
	// and it earns that: the journal grows with what the target systems disagree
	// about, which is external input by a longer road. A run that would open more
	// than this counts the rest and records none of them — a hundred thousand
	// findings mean the scope or the catalogue is wrong, and filling a disk with
	// them helps nobody read the first ten.
	ReconcileJournal int32

	// Recertify is one message opening a recertification campaign: its name, its
	// scope, and the map of who reviews whom (ADR-0341).
	// The reviewer map is what makes it large — one entry per person in scope — and
	// it is external input like any other.
	Recertify int64

	// RecertifyRows is how many questions one campaign may ask.
	//
	// A ceiling on what a *read of Atlas's own inventory* produced, which is unusual
	// and deliberate. The number that matters is not a message size, it is how many
	// judgements one campaign asks of people: a campaign of twenty thousand rows is
	// not answered, it is signed, and this is the one place where making that
	// impossible is cheaper than detecting it afterwards. A campaign above it is
	// refused whole rather than shortened, because a campaign missing its tail looks
	// exactly like a complete one to whoever closes it.
	RecertifyRows int32

	// RecertifyReport is how many rows one answer renders. Unlike RecertifyRows this
	// is only about reading: the campaign keeps every row and every one is decidable
	// through its own route, so a view that stops at five hundred costs nothing but
	// a second request.
	RecertifyReport int32

	// RecertifyNote is one decision's free text. Small on purpose — it is a
	// sentence explaining a judgement, not an attachment, and a justification field
	// that invites an essay gets an essay from the first reviewer and "ok" from
	// every one after.
	RecertifyNote int64

	// ExpiringWindow is how far ahead one read may look, in days
	// (ADR-0344). A ceiling on a *question* rather than
	// on a message, and it earns that: a window wide enough to cover every right
	// with an end turns "what ends soon" into a list of the whole inventory, which
	// is a different route's job and a different cost.
	ExpiringWindow int32

	// ExpiringReport is how many rights one answer renders. The counts are over
	// everything either way, so a cut list costs a second request and never a wrong
	// number — which is why this one is a truncation where a campaign's rows are a
	// refusal.
	ExpiringReport int32

	// ConflictReport is how many held incompatible pairs one answer lists
	// (ADR-0342). The counts are over everything, so a cut list
	// costs a second request and never a wrong number. A rule declared over a large
	// estate can produce a great many findings at once and none of them is cleared
	// automatically — which is correct, and is also why the list is bounded and the
	// number is not.
	ConflictReport int32

	// HistoryReport is how many ended holds one answer lists
	// (ADR-0346).
	//
	// Larger than the others in this group, and deliberately: every ceiling beside
	// it bounds a list of problems, and a problem list that needs a high ceiling
	// is telling you something. This one bounds a person's access history, which
	// grows with their tenure rather than with anything being wrong — somebody ten
	// years in a job that changes has a long and entirely healthy list, and a
	// ceiling that truncated it would omit the oldest rows, which are the ones an
	// audit reaches for.
	HistoryReport int32

	// Favourites is how many products one account may mark
	// (ADR-0348). The smallest ceiling in this file, and the one whose
	// number is a *product* judgement as much as a budget: a shortcut list nobody
	// can scan has stopped being a shortcut. It is a budget nonetheless, because
	// without it one account can grow a stored file without bound by pressing a
	// star.
	Favourites int32

	// OrderLineAnswers is how many configuration answers one order line may carry
	// (ADR-0358) — the fields of
	// the form its product declares. Small, because a form somebody fills in while
	// ordering a laptop is a handful of questions, and one with forty is a process
	// wearing a form's clothes.
	//
	// It bounds a map that arrives whole in a request body, so without it one
	// request can grow the order store without bound.
	OrderLineAnswers int32

	// PendingWorkItems is how many waiting items one person's answer lists
	// (ADR-0343). Small, because the consumer is a reminder and a
	// reminder listing two hundred lines is one nobody reads to the end. The counts
	// are over everything, so a message can say "and 190 more" truthfully.
	PendingWorkItems int32
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
		// Eight megabytes is a full enumeration of a few tens of thousands of objects
		// under a narrow $select; two thousand objects is the batch a run-loop turn can
		// write without the engine noticeably stalling, and five hundred lines is more
		// than anybody reads in one sitting.
		DirectorySync:    8 << 20,
		DirectoryObjects: 2_000,
		DirectoryReport:  500,
		// A commissioning load is read in pages, so the per-message numbers are the
		// directory's rather than larger: two thousand rights is a run-loop turn the
		// engine absorbs, and eight megabytes carries them with room to spare. The
		// report is allowed more lines than the directory's because its lines are the
		// grants, and a first load is read once, carefully, by somebody deciding.
		InventoryLoad:         8 << 20,
		InventoryObservations: 2_000,
		InventoryReport:       2_000,
		// A reconciliation reads in scopes rather than in whole systems, so its
		// message is the same size as a load's. The report is shorter than a load's
		// on purpose: a load's body is what somebody authorises and has to be read
		// whole, while a reconciliation is read repeatedly and what matters is the
		// first screen of it. Ten thousand open findings is already a broken
		// catalogue rather than a governance backlog.
		Reconcile:             8 << 20,
		ReconcileObservations: 2_000,
		ReconcileReport:       500,
		ReconcileJournal:      10_000,
		Recertify:             8 << 20,
		RecertifyRows:         5_000,
		RecertifyReport:       500,
		RecertifyNote:         4 << 10,
		ExpiringWindow:        365,
		ExpiringReport:        500,
		PendingWorkItems:      50,
		ConflictReport:        500,
		HistoryReport:         2000,
		Favourites:            100,
		OrderLineAnswers:      50,
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
