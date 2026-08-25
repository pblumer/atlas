package api

import (
	"net/http"
	"sort"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
)

// This file answers one question for the Modeler: for the implementation an author is
// about to choose, which process runs the work on THIS server?
//
// Three panels choose one — the service-task connector picker (ADR-0067), a script
// task's language (ADR-0047), and a business rule task's decision binding (ADR-0050) —
// and all three author work that `--offload-connectors` can move.
//
// The picker used to answer it from a constant in the browser, written when the
// answer was the same everywhere — every kind but the plain job worker ran inside the
// engine. Two changes made that constant false, and neither is visible from a
// browser. Kinds are moved onto a worker by the server's own command line, by default
// (ADR-0168) or by --offload-connectors, so the answer differs per install; and kinds
// were then born on a worker with no in-process form at all (ADR-0173), so for those
// the answer can never be "the engine" no matter how the server is configured.
//
// The engine already holds the only authority on the first half — s.jobRunner is what
// an offload *removes a handler from*, and it is the same registry the type-keyed pull
// consults to decide whether a job may be leased at all. So the picker asks rather
// than guesses, which also means a kind that moves later needs no second edit here
// (ADR-0183).

// The placements a connector kind can have. The pair of "-only" values is not
// pedantry: each is the case where the *advice* attached to the plain value would be
// wrong. Telling the author of an in-engine kind to prefer a job worker (ADR-0164) is
// useless when the kind has no out-of-process form, and saying a kind was moved onto
// a worker misdescribes one that was never anywhere else.
const (
	// placementEngine: an in-process handler is registered here, and the kind can be
	// moved onto a worker with --offload-connectors.
	placementEngine = "engine"
	// placementEngineOnly: it runs here and has no out-of-process form at all.
	placementEngineOnly = "engine-only"
	// placementWorker: this server does not run the kind itself; its jobs wait for a
	// worker (a supervised child, for the kinds offloaded by default).
	placementWorker = "worker"
	// placementWorkerOnly: the kind never had an in-process handler — it was born on
	// a worker, so no configuration brings it back into the engine.
	placementWorkerOnly = "worker-only"
)

// authoredKindJobTypes maps an id the Modeler authors by to the reserved job types a
// task of that kind compiles to. Those indices are stable identifiers baked into
// compiled processes; this table names them and moves none of them.
//
// The ids are what the *author* picks, which is not always what the *operator* moves.
// For connectors and the decision bindings the two words coincide, and
// TestPlacementJobTypesAgreeWithOffloadableKinds holds them to the same job types. For
// scripts they deliberately do not: `--offload-connectors script` is one word over three
// languages, while the panel offers a language at a time and each can also be turned off
// on its own. Keying the languages separately is what lets the panel say "Python waits
// for a worker" on a server where PowerShell does not — a per-kind answer would have
// called both in-engine. TestScriptLanguageIDsMatchTheLanguageRegistry ties those ids to
// script.Langs, whose names the Modeler's own select is built from.
//
// TestEveryCatalogKindHasAPlacement fails when the connector picker gains a kind this
// table does not name, and TestEveryOffloadableJobTypeHasAPlacement fails when an
// operator can move work nothing here reports — silence being the exact thing this
// replaced.
var authoredKindJobTypes = map[string][]int32{
	"rest":                  {compiler.RestJobTypeIndex},
	"scim":                  {compiler.ScimJobTypeIndex},
	"ldap":                  {compiler.LdapJobTypeIndex},
	"soap":                  {compiler.SoapJobTypeIndex},
	"ad":                    {compiler.AdJobTypeIndex},
	"ldif":                  {compiler.LdifJobTypeIndex},
	"entra":                 {compiler.EntraJobTypeIndex},
	"mssql":                 {compiler.MsSqlJobTypeIndex},
	"mariadb":               {compiler.MariaDBJobTypeIndex},
	"postgres":              {compiler.PostgresJobTypeIndex},
	connectorKindClio:       {compiler.ClioWriteJobTypeIndex, compiler.ClioQueryJobTypeIndex, compiler.ClioReadJobTypeIndex},
	connectorKindMail:       {compiler.MailJobTypeIndex},
	"csv":                   {compiler.CsvImportJobTypeIndex},
	connectorKindSharePoint: {compiler.SharePointJobTypeIndex},
	connectorKindRemedy:     {compiler.RemedyJobTypeIndex},
	"webscrape":             {compiler.WebScrapeJobTypeIndex},
	"userconnector":         {compiler.UserConnectorJobTypeIndex},

	// A script task's language (ADR-0047). One entry per language, not one for
	// "script": see above.
	"powershell": {compiler.PwshJobTypeIndex},
	"python":     {compiler.PythonJobTypeIndex},
	"javascript": {compiler.JsJobTypeIndex},

	// A business rule task's decision binding (ADR-0050): the embedded DMN engine, or
	// a central temis service. Two kinds to the operator, so they move separately.
	"dmn":              {compiler.DMNJobTypeIndex},
	connectorKindTemis: {compiler.TemisDecisionJobTypeIndex},
}

// catalogKindsWithoutJobType are the picker entries that compile to no job at all,
// with the reason. The server reports no placement for them and the picker shows no
// badge, which is the honest answer in both cases rather than an omission.
var catalogKindsWithoutJobType = map[string]string{
	"worker": "the plain job worker: its job type is model-authored rather than reserved, and being out of process is what the kind IS — its name already says so",
	"mockup": "the engine simulates the task with a timer and creates no job at all (ADR-0120), so there is nothing to lease and nothing that could move",
}

// engineOnlyJobTypes are the reserved job types whose work has no out-of-process form
// at all, with the reason. It is the one honest exception to ADR-0164's rule that a
// side-effecting service task belongs on a worker: what these do is mutate state the
// run loop owns, so there is nothing for a worker to hold and no endpoint to reach.
//
// Both directions read this: the placement above, so the picker does not advise a move
// that cannot be made, and TestEveryInProcessHandlerIsOffloadable, so a handler is not
// quietly excused from being movable.
var engineOnlyJobTypes = map[int32]string{
	compiler.UserConnectorJobTypeIndex: "mutates the run-loop-owned user store (ADR-0123), so it has no out-of-process form",
}

// offloadableJobTypes is offloadableKinds inverted: the job types an operator can move
// onto a worker, whatever the word they name them by. The placement asks per job type
// rather than per id because the vocabularies do not line up — three script languages
// share the single flag word "script" — so an id lookup would report the languages as
// having no out-of-process form at all.
// Derived once at startup rather than per request: it is a pure function of a table
// that never changes, and the placement query reads it once per job type.
var offloadableJobTypes = func() map[int32]bool {
	set := make(map[int32]bool, len(offloadableKinds))
	for _, types := range offloadableKinds {
		for _, jt := range types {
			set[jt] = true
		}
	}
	return set
}()

// connectorPlacement is one authored kind and where this server runs it.
type connectorPlacement struct {
	ID        string `json:"id"`
	Placement string `json:"placement"`
}

// connectorPlacements reports every catalog kind's placement, ordered by id so the
// reply is stable across calls.
func (s *Server) connectorPlacements() []connectorPlacement {
	ids := make([]string, 0, len(authoredKindJobTypes))
	for id := range authoredKindJobTypes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]connectorPlacement, 0, len(ids))
	// One hop onto the run loop for the whole set: s.jobRunner is owned there, and a
	// reply assembled across several hops could describe two different configurations.
	s.do(func() {
		for _, id := range ids {
			out = append(out, connectorPlacement{ID: id, Placement: s.placementOfCatalogKind(id)})
		}
	})
	return out
}

// placementOfCatalogKind decides one kind's placement from the two authorities: the
// job runner says what this server runs, and offloadableKinds says whether an
// out-of-process form exists to run it instead.
//
// It answers about the *kind*, not about whether a feature is switched on. A server
// built without user provisioning registers no handler for it, and the kind is still
// engine-only — its work has no worker form either way, and "worker" would be a
// worse answer than "engine" there.
//
// Runs on the run-loop goroutine (invariant I3): s.jobRunner is owned there, and
// applyOffloadedKinds mutates it at startup.
func (s *Server) placementOfCatalogKind(id string) string {
	types := authoredKindJobTypes[id]
	for _, jt := range types {
		if _, only := engineOnlyJobTypes[jt]; only {
			return placementEngineOnly
		}
	}
	inProcess := false
	for _, jt := range types {
		if s.jobRunner.Handles(jt) {
			inProcess = true
			break
		}
	}
	hasWorkerForm := false
	for _, jt := range types {
		if offloadableJobTypes[jt] {
			hasWorkerForm = true
			break
		}
	}
	switch {
	case inProcess && hasWorkerForm:
		return placementEngine
	case inProcess:
		// A handler an operator cannot name in --offload-connectors, and not one of
		// the recorded exceptions above. TestEveryInProcessHandlerIsOffloadable fails
		// on exactly this, so it is a state the repository does not ship; saying
		// "engine-only" is the reading that at least withholds advice that would not
		// work rather than offering it.
		return placementEngineOnly
	case hasWorkerForm:
		return placementWorker
	default:
		return placementWorkerOnly
	}
}

// handleConnectorKinds tells the Modeler where this server runs each connector kind,
// so the picker's badge describes this install rather than the arrangement that held
// when it was written.
func (s *Server) handleConnectorKinds(w http.ResponseWriter, _ *http.Request) {
	httpapi.JSON(w, http.StatusOK, map[string]any{"kinds": s.connectorPlacements()})
}
