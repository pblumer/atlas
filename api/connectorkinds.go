package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/clio"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/connector/remedy"
	"github.com/pblumer/atlas/connector/sharepoint"
	"github.com/pblumer/atlas/connector/temis"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/state"
)

// managedConnectorKind describes one operator-managed connector kind end to end: how
// a create request for it is validated, how its runtime registry is created and its
// job worker(s) registered at startup, and how that registry is rebuilt from the
// connector store on every change. The ordered slice below is the *single* list every
// connector path consults — the create whitelist, the create validation, the startup
// wiring, and the registry rebuild all derive from it — so adding a kind is one entry
// here instead of edits scattered across the create handler, the server constructor,
// and rebuildConnectorRegistries.
//
// (The reserved compiler.*JobTypeIndex values are stable identifiers baked into
// compiled processes; each entry references its own and none of them move.)
type managedConnectorKind struct {
	// name is the connector.Kind value (e.g. "mail"); see the connectorKind* constants.
	name string
	// validateCreate checks and normalizes a decoded create request for this kind. It
	// may mutate p (default a mail provider, clear the mail-only Provider/Sender fields
	// for kinds that don't use them) and returns a human-readable message when the
	// request is invalid; the empty string means the request is valid.
	validateCreate func(p *createConnectorParams) string
	// newRegistry creates this kind's empty client registry and assigns it to its
	// Server field, before rebuild populates it. Runs once at startup.
	newRegistry func(s *Server)
	// registerHandlers subscribes this kind's in-process job worker(s) under their
	// reserved job type(s), resolving each job's connector from the compiled process
	// (processLookup) and its client from the registry. Runs once at startup, after
	// newRegistry. The worker runs off the run loop and after fsync (I2/I3).
	registerHandlers func(s *Server, store *state.Store)
	// rebuild rebuilds this kind's live client registry from the current connector
	// store and swaps it atomically. It reads the store, so it runs on the run-loop
	// goroutine (the store's owner), like the build*Clients helpers it wraps.
	rebuild func(s *Server) error
	// problem reports why a *configured* connector of this kind is not in the live
	// registry — disabled, missing an endpoint or credential, of another kind — and
	// false when it is usable or unknown. It is what lets the connector list say a
	// record is stored but not working, instead of leaving that to be discovered by a
	// token parking on it (ADR-0158).
	problem func(s *Server, name string) (string, bool)
	// jobTypes are the reserved compiler job-type indices whose tasks resolve a
	// connector of this kind. They are what turns a model's connector reference back
	// into "a connector of kind X is required here", so a deploy can check the name
	// against the store instead of leaving the mismatch to the first token (ADR-0158).
	jobTypes []int32
	// workerOnly marks a kind the engine never runs itself: it builds no client and
	// subscribes no in-process handler, so the tenant credential never enters the
	// engine (ADR-0172). The store entry exists only so an operator can configure it in
	// the Console and the supervised worker is provisioned from it (superviseEnv). Its
	// newRegistry/rebuild/registerHandlers are therefore nil and skipped at setup.
	workerOnly bool
}

// createConnectorParams is the decoded body of a create-connector request. The
// per-kind validators normalize it in place (see managedConnectorKind.validateCreate)
// before handleCreateConnector persists it as a connector record.
type createConnectorParams struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Endpoint       string `json:"endpoint"`
	CredentialsRef string `json:"credentialsRef"`
	Provider       string `json:"provider"`
	Sender         string `json:"sender"`
	Enabled        *bool  `json:"enabled"`
	// ConnectionString is a SQL connector's whole configuration, sealed into the vault
	// by the create handler which then stores only the reference — so the record still
	// holds no secret (I6). It exists because for these kinds the credential *is* the
	// configuration: a dialog that asked only for a vault key would be a form with one
	// field that is not the thing the operator has in their hand. Naming an existing
	// key with credentialsRef instead still works and is what an operator who already
	// keeps their DSNs in the vault does. Never echoed back.
	ConnectionString string `json:"connectionString"`
}

// managedConnectorKinds is the ordered registry of managed connector kinds. Order is
// preserved everywhere it is iterated (the startup wiring, the rebuild sequence, the
// whitelist error message), so it stays stable across releases.
var managedConnectorKinds = []managedConnectorKind{
	{
		// A *central* business rule task delegates its decision to a remote temis
		// service instead of the embedded library (ADR-0050). It registers via
		// HandleCompleting because a decision's completion both writes its result back
		// and retains the evaluation for debugging (ADR-0066). Connectors come from the
		// environment plus operator-managed instances in the Console (ADR-0041).
		name:           connectorKindTemis,
		validateCreate: validateEndpointOnlyConnector,
		newRegistry:    func(s *Server) { s.temisRegistry = temis.NewRegistry() },
		registerHandlers: func(s *Server, store *state.Store) {
			s.jobRunner.HandleCompleting(compiler.TemisDecisionJobTypeIndex, func(rd state.Reader) job.CompletingHandler {
				return temis.Handler(rd, s.processLookup, s.temisRegistry, nil)
			})
		},
		rebuild: func(s *Server) error {
			clients, problems, err := s.buildTemisClients()
			if err != nil {
				return err
			}
			s.temisRegistry.ReplaceWith(clients, problems)
			return nil
		},
		problem: func(s *Server, name string) (string, bool) {
			if s.temisRegistry == nil {
				return "", false
			}
			return s.temisRegistry.Problem(name)
		},
		jobTypes: []int32{compiler.TemisDecisionJobTypeIndex},
	},
	{
		// A clio connector task appends, reads, or queries a server-registered clio
		// event store (ADR-0036) — one worker per operation under its own reserved job
		// type. The endpoint and token live in the managed connector store; the token is
		// resolved from the vault at build time (ADR-0041).
		name:           connectorKindClio,
		validateCreate: validateEndpointOnlyConnector,
		newRegistry:    func(s *Server) { s.clioRegistry = clio.NewRegistry() },
		registerHandlers: func(s *Server, store *state.Store) {
			s.jobRunner.Handle(compiler.ClioWriteJobTypeIndex, func(rd state.Reader) job.Handler { return clio.Handler(rd, s.processLookup, s.clioRegistry) })
			s.jobRunner.HandleWithOutput(compiler.ClioQueryJobTypeIndex, func(rd state.Reader) job.OutputHandler { return clio.QueryHandler(rd, s.processLookup, s.clioRegistry) })
			s.jobRunner.HandleWithOutput(compiler.ClioReadJobTypeIndex, func(rd state.Reader) job.OutputHandler { return clio.ReadHandler(rd, s.processLookup, s.clioRegistry) })
		},
		rebuild: func(s *Server) error {
			clients, problems, err := s.buildClioClients()
			if err != nil {
				return err
			}
			s.clioRegistry.ReplaceWith(clients, problems)
			return nil
		},
		problem: func(s *Server, name string) (string, bool) {
			if s.clioRegistry == nil {
				return "", false
			}
			return s.clioRegistry.Problem(name)
		},
		jobTypes: []int32{compiler.ClioWriteJobTypeIndex, compiler.ClioQueryJobTypeIndex, compiler.ClioReadJobTypeIndex},
	},
	{
		// An outbound mail connector task sends a model-authored message through a
		// server-registered provider (ADR-0079). The provider host and credential live
		// in the managed connector store; the credential is resolved from the vault at
		// build time (ADR-0041), so a secret never lives in a model. The preview
		// provider (ADR-0150) is the exception that needs neither: it frames the message
		// identically and delivers it to the server's outbox.
		name:           connectorKindMail,
		validateCreate: validateMailConnector,
		newRegistry: func(s *Server) {
			s.mailRegistry = mail.NewRegistry()
			// The outbox is created with the registry but never swapped with it: a
			// rebuild re-binds the preview clients and must not throw away what they
			// already delivered (ADR-0150). 0 takes the default capacity.
			s.mailOutbox = mail.NewOutbox(0)
		},
		registerHandlers: func(s *Server, store *state.Store) {
			s.jobRunner.Handle(compiler.MailJobTypeIndex, func(rd state.Reader) job.Handler { return mail.Handler(rd, s.processLookup, s.mailRegistry) })
		},
		rebuild: func(s *Server) error {
			clients, problems, err := s.buildMailClients()
			if err != nil {
				return err
			}
			s.mailRegistry.ReplaceWith(clients, problems)
			return nil
		},
		problem: func(s *Server, name string) (string, bool) {
			if s.mailRegistry == nil {
				return "", false
			}
			return s.mailRegistry.Problem(name)
		},
		jobTypes: []int32{compiler.MailJobTypeIndex},
	},
	{
		// A SharePoint connector task creates a list item through a server-registered
		// Microsoft Graph provider (ADR-0141) and writes the created item's JSON into the
		// task's result variable (HandleWithOutput). The Graph base and OAuth credential
		// live in the managed connector store; the credential is resolved from the vault
		// at build time (ADR-0041).
		name:           connectorKindSharePoint,
		validateCreate: validateSharePointConnector,
		newRegistry:    func(s *Server) { s.sharePointRegistry = sharepoint.NewRegistry() },
		registerHandlers: func(s *Server, store *state.Store) {
			s.jobRunner.HandleWithOutput(compiler.SharePointJobTypeIndex, func(rd state.Reader) job.OutputHandler {
				return sharepoint.Handler(rd, s.processLookup, s.sharePointRegistry)
			})
		},
		rebuild: func(s *Server) error {
			clients, problems, err := s.buildSharePointClients()
			if err != nil {
				return err
			}
			s.sharePointRegistry.ReplaceWith(clients, problems)
			return nil
		},
		problem: func(s *Server, name string) (string, bool) {
			if s.sharePointRegistry == nil {
				return "", false
			}
			return s.sharePointRegistry.Problem(name)
		},
		jobTypes: []int32{compiler.SharePointJobTypeIndex},
	},
	{
		// A BMC Remedy connector task creates an entry (e.g. an incident) in a Remedy
		// form through the AR System REST API (ADR-0106) and writes the new entry id into
		// the task's result variable (HandleWithOutput). The base URL and credential
		// bundle live in the managed connector store; the bundle is resolved from the
		// vault at build time (ADR-0041).
		name:           connectorKindRemedy,
		validateCreate: validateRemedyConnector,
		newRegistry:    func(s *Server) { s.remedyRegistry = remedy.NewRegistry() },
		registerHandlers: func(s *Server, store *state.Store) {
			s.jobRunner.HandleWithOutput(compiler.RemedyJobTypeIndex, func(rd state.Reader) job.OutputHandler { return remedy.Handler(rd, s.processLookup, s.remedyRegistry) })
		},
		rebuild: func(s *Server) error {
			clients, problems, err := s.buildRemedyClients()
			if err != nil {
				return err
			}
			s.remedyRegistry.ReplaceWith(clients, problems)
			return nil
		},
		problem: func(s *Server, name string) (string, bool) {
			if s.remedyRegistry == nil {
				return "", false
			}
			return s.remedyRegistry.Problem(name)
		},
		jobTypes: []int32{compiler.RemedyJobTypeIndex},
	},
	{
		// A Microsoft Entra ID connector task manages the cloud directory over Graph
		// (ADR-0172). It is worker-only: the engine builds no client and holds no tenant
		// credential — the store entry exists only so an operator can add a tenant in the
		// Console and the supervised entra worker is provisioned from it (superviseEnv),
		// the credential resolved from the vault as an OAuth bundle. So there is no
		// registry, no rebuild and no in-process handler here; the compiler's connector
		// name check (jobTypes) still uses the store so a deploy catches a name typo.
		name:           connectorKindEntra,
		workerOnly:     true,
		validateCreate: validateEntraConnector,
		jobTypes:       []int32{compiler.EntraJobTypeIndex},
	},
	// The three SQL products (ADR-0173, ADR-draft-console-managed-sql-connectors). Each
	// is worker-only for the same reason Entra is, and more strongly: a DSN *is* a
	// credential, so the engine builds no client and registers no handler. The store
	// entry exists so an operator can add a database in the Console instead of on a
	// command line, and superviseEnv renders its connection string into the supervised
	// worker's environment. An external worker is handed nothing and still reads its
	// own environment, which is what keeps ADR-0173's promise intact for every worker
	// Atlas does not start itself.
	{
		name:           connectorKindPostgres,
		workerOnly:     true,
		validateCreate: validateSQLConnector,
		problem:        func(s *Server, name string) (string, bool) { return s.sqlConnectorProblem(connectorKindPostgres, name) },
		jobTypes:       []int32{compiler.PostgresJobTypeIndex},
	},
	{
		name:           connectorKindMariaDB,
		workerOnly:     true,
		validateCreate: validateSQLConnector,
		problem:        func(s *Server, name string) (string, bool) { return s.sqlConnectorProblem(connectorKindMariaDB, name) },
		jobTypes:       []int32{compiler.MariaDBJobTypeIndex},
	},
	{
		name:           connectorKindMSSQL,
		workerOnly:     true,
		validateCreate: validateSQLConnector,
		problem:        func(s *Server, name string) (string, bool) { return s.sqlConnectorProblem(connectorKindMSSQL, name) },
		jobTypes:       []int32{compiler.MsSqlJobTypeIndex},
	},
}

// setupManagedConnectors wires every managed connector kind at startup: it creates each
// kind's registry, populates it from the connector store, and subscribes its job
// worker(s). Each registry is built here — before the loop serves traffic — and rebuilt
// on every connector change; a task whose connector is not configured parks until it
// is. It runs on the run-loop goroutine (the connector store's owner), after
// s.jobRunner and s.connectors are set.
func (s *Server) setupManagedConnectors(store *state.Store) error {
	for _, k := range managedConnectorKinds {
		// A worker-only kind runs entirely on a supervised worker: the engine builds no
		// client and subscribes no handler for it, so the tenant credential never enters
		// the engine (ADR-0172). Its store entry is read only by superviseEnv.
		if k.workerOnly {
			continue
		}
		k.newRegistry(s)
		if err := k.rebuild(s); err != nil {
			return err
		}
		k.registerHandlers(s, store)
	}
	return nil
}

// offloadableKinds maps an operator-facing kind name to the reserved job types its
// in-process handler serves. It is the vocabulary of --offload-connectors, and it
// deliberately spans more than the *managed* kinds: a kind is offloadable if Atlas
// runs it itself, whether or not it also has a server-side registry. CSV import is
// the clearest example — it has no registry precisely because it needs no credential
// (ADR-0168), and it is the first kind meant to move.
//
// The user task is absent on purpose: it waits for a person, and the pull refuses it
// for that reason rather than because a handler serves it. Every *other* in-process
// handler must appear here, and TestEveryInProcessHandlerIsOffloadable fails when one
// does not: a connector Atlas can run but an operator cannot move is a kind that can
// only ever run on the loop, which is the thing ADR-0164 rules out.
var offloadableKinds = map[string][]int32{
	connectorKindTemis:      {compiler.TemisDecisionJobTypeIndex},
	connectorKindClio:       {compiler.ClioWriteJobTypeIndex, compiler.ClioQueryJobTypeIndex, compiler.ClioReadJobTypeIndex},
	connectorKindMail:       {compiler.MailJobTypeIndex},
	connectorKindSharePoint: {compiler.SharePointJobTypeIndex},
	connectorKindRemedy:     {compiler.RemedyJobTypeIndex},
	"csv":                   {compiler.CsvImportJobTypeIndex},
	"ldif":                  {compiler.LdifJobTypeIndex},
	"rest":                  {compiler.RestJobTypeIndex},
	"scim":                  {compiler.ScimJobTypeIndex},
	"ldap":                  {compiler.LdapJobTypeIndex},
	"soap":                  {compiler.SoapJobTypeIndex},
	"ad":                    {compiler.AdJobTypeIndex},
	"webscrape":             {compiler.WebScrapeJobTypeIndex},
	"dmn":                   {compiler.DMNJobTypeIndex},
	"script":                {compiler.PwshJobTypeIndex, compiler.PythonJobTypeIndex, compiler.JsJobTypeIndex},
}

// DefaultOffloadedKinds are the connector kinds Atlas moves onto a worker of its
// own accord, and supervises a worker for. It is the opt-out half of ADR-0164:
// somebody trying Atlas gets the out-of-process architecture without configuring
// anything, because the engine starts the worker itself.
//
// A kind belongs here when a supervised worker can actually serve it, which is true
// two ways. Most of these need **no credential** at all: what each of them needs is
// something a worker has by being a separate process — a CPU for a script, network
// reach for a scrape, neither for a CSV parse.
//
// Mail is the other way, and the reason the set is not simply "the unmanaged kinds".
// Its endpoint and password live in the connector store rather than the environment
// (ADR-0036/0041), so for as long as a child only inherited the environment, moving
// mail here would have handed every mail task to a worker with no mailbox. The engine
// now writes that configuration into the child's environment at spawn (see
// superviseEnv), which is the operator setting the worker up, done by the program —
// so mail is served, and it is served by the process that should be waiting on an
// SMTP handshake. Every managed kind here must be one superviseEnv provisions;
// TestEveryDefaultOffloadedKindCanBeServedByItsWorker holds that.
//
// Active Directory is the third way in, and the one that made the set worth
// re-deciding (ADR-0182). It is not managed — it holds no
// connector record, and an AD task authors its own server — but its bind password is
// a per-task *reference* that resolves out of the vault, which a supervised worker
// can read no more than it can read the connector store. So it is defaulted on the
// same condition mail is: superviseEnv hands the child exactly the references the
// deployed models name (adWorkerEnv). It belongs here because a directory write is
// the plainest case of work that should never sit on the engine's loop — a bind, a
// modify and a close against a domain controller somebody else operates — and
// because the credential is worth less to an attacker in a worker than in the engine
// (ADR-0166's own argument for offloading it at all).
//
// The credential-bearing kinds the engine cannot yet hand over stay in the engine
// until an operator moves their secrets themselves, with --offload-connectors.
func DefaultOffloadedKinds() []string {
	return []string{"ad", "csv", connectorKindMail, "script", "webscrape"}
}

// DefaultSupervisedWorkerOnlyKinds are the worker-only connector kinds Atlas supervises
// by default (ADR-0172). Unlike the offloaded kinds these have no in-engine form at all,
// so --in-process-connectors cannot apply to them: the worker is the only way to run
// them. It starts with nothing to serve and parks (exitNothingToServe); the moment an
// operator adds a tenant in the Console, refreshSupervisedWorkers brings it up — no
// flag, no restart of Atlas. That is what makes the tenant a Console entry rather than a
// deployment change.
func DefaultSupervisedWorkerOnlyKinds() []string {
	return []string{connectorKindEntra, connectorKindPostgres, connectorKindMariaDB, connectorKindMSSQL}
}

// applyOffloadedKinds removes the in-process handlers for the kinds an operator
// moved to a worker, after every registration path has run. Doing it as a removal
// rather than a condition at each site is what makes it impossible to miss one.
//
// An unknown name is an error rather than a no-op: an operator who misspells one
// would otherwise believe they had relocated a kind while it kept running in the
// engine, which is the single outcome this exists to prevent.
func (s *Server) applyOffloadedKinds() error {
	for _, name := range s.offloadedKinds {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		types, ok := offloadableKinds[name]
		if !ok {
			return fmt.Errorf("api: cannot offload connector kind %q: no such kind (have %s)",
				name, strings.Join(offloadableKindNames(), ", "))
		}
		for _, jt := range types {
			s.jobRunner.Unhandle(jt)
		}
	}
	return nil
}

// offloadableKindNames lists every kind that can be named, for the error above.
// IsOffloadableKind reports whether a connector kind has in-process handlers at all,
// which is what makes it nameable in --offload-connectors: offloading is the removal
// of those handlers, so a kind that has none (entra, which only ever runs on a
// worker) is refused there rather than silently accepted. A caller that wants a
// worker for a kind asks this first, so it can supervise a worker-only kind without
// walking into that refusal (ADR-0164/0168).
func IsOffloadableKind(name string) bool {
	_, ok := offloadableKinds[name]
	return ok
}

func offloadableKindNames() []string {
	names := make([]string, 0, len(offloadableKinds))
	for name := range offloadableKinds {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// lookupManagedConnectorKind returns the descriptor for a kind name, or false if the
// kind is not a managed one (e.g. the model-authored http.rest kind).
func lookupManagedConnectorKind(name string) (managedConnectorKind, bool) {
	for _, k := range managedConnectorKinds {
		if k.name == name {
			return k, true
		}
	}
	return managedConnectorKind{}, false
}

// managedConnectorKindsError is the 400 message for an unsupported create kind. It is
// built from the registry so it always lists exactly the supported kinds, in order
// (e.g. connector kind must be "temis", "clio", "mail", "sharepoint", or "remedy").
func managedConnectorKindsError() string {
	quoted := make([]string, len(managedConnectorKinds))
	for i, k := range managedConnectorKinds {
		quoted[i] = "\"" + k.name + "\""
	}
	if len(quoted) == 1 {
		return "connector kind must be " + quoted[0]
	}
	return "connector kind must be " + strings.Join(quoted[:len(quoted)-1], ", ") + ", or " + quoted[len(quoted)-1]
}

// validateEndpointOnlyConnector validates a temis or clio create request: the
// mail-only Provider/Sender fields do not apply, and an endpoint is required.
func validateEndpointOnlyConnector(p *createConnectorParams) string {
	p.Provider, p.Sender = "", ""
	if p.Endpoint == "" {
		return "connector endpoint is required"
	}
	return ""
}

// validateRemedyConnector validates a Remedy create request: like temis/clio it needs
// an endpoint, and it also needs a credentialsRef naming a vault {username,password}
// bundle to authenticate against the AR System (ADR-0106); the secret itself never
// lives in the record (I6).
func validateRemedyConnector(p *createConnectorParams) string {
	p.Provider, p.Sender = "", ""
	if p.Endpoint == "" {
		return "connector endpoint is required"
	}
	if p.CredentialsRef == "" {
		return "a remedy connector requires a credentialsRef naming a vault {username,password} bundle"
	}
	return ""
}

// validateSharePointConnector validates a SharePoint create request: it defaults its
// Graph API base (endpoint is an optional override) and needs a credentialsRef
// naming a vault OAuth auth bundle (ADR-0141). Provider/Sender are mail-only.
func validateSharePointConnector(p *createConnectorParams) string {
	p.Provider, p.Sender = "", ""
	if p.CredentialsRef == "" {
		return "a sharepoint connector requires a credentialsRef naming a vault auth bundle"
	}
	return ""
}

// validateEntraConnector checks an Entra tenant a Console operator is adding. The
// tenant id, client id and client secret live together in a vault bundle the
// credentialsRef names — never in the record, and never in a model (ADR-0172). An
// optional endpoint overrides the Graph base for a national cloud; the mail-only
// fields do not apply.
func validateEntraConnector(p *createConnectorParams) string {
	p.Provider, p.Sender = "", ""
	if p.CredentialsRef == "" {
		return "an entra connector requires a credentialsRef naming a vault bundle {tenantId, clientId, clientSecret}"
	}
	return ""
}

// normalizeConnectorUpdate re-applies the per-kind create validation to a record a
// PATCH has just changed, returning a message when the result is unusable. A partial
// body carries no kind, so it cannot route through validateCreate — which is exactly
// how an SMTP endpoint could be edited into a shape that only fails much later, at
// send time (ADR-0150). The *record* does carry its kind, so the check a create got is
// re-run against the record the update produced, which is what lets an operator switch
// a mail connector's provider from an incident and be told immediately that the new
// one needs a credential (ADR-0160).
//
// Only mail validates anything today; every other kind passes through untouched, so
// emptying a temis endpoint is still allowed and reported as a problem on the
// connector rather than refused — the shape an operator uses to park a connector they
// are in the middle of moving.
func normalizeConnectorUpdate(rec *connector) string {
	if rec.Kind != connectorKindMail {
		return ""
	}
	p := createConnectorParams{
		Name:           rec.Name,
		Kind:           rec.Kind,
		Endpoint:       strings.TrimSpace(rec.Endpoint),
		CredentialsRef: strings.TrimSpace(rec.CredentialsRef),
		Provider:       strings.TrimSpace(rec.Provider),
		Sender:         strings.TrimSpace(rec.Sender),
	}
	if msg := validateMailConnector(&p); msg != "" {
		return msg
	}
	rec.Endpoint, rec.CredentialsRef = p.Endpoint, p.CredentialsRef
	rec.Provider, rec.Sender = p.Provider, p.Sender
	return ""
}

// validateMailConnector validates a mail create request (ADR-0079/0081/0150): the
// provider is SMTP (the default), Gmail, Microsoft Graph, or preview; a sender
// (default From address) is always required; SMTP needs a submission endpoint, which
// is normalized to "host:port" here, so an endpoint written without a port is fixed
// (or explained) at the moment someone types it rather than at the moment a token
// parks behind an incident; a native provider needs a credentialsRef pointing at a
// vault auth bundle instead; and preview needs neither, which is the point of it.
func validateMailConnector(p *createConnectorParams) string {
	if p.Provider == "" {
		p.Provider = mail.ProviderSMTP
	}
	switch p.Provider {
	case mail.ProviderSMTP, mail.ProviderGmail, mail.ProviderMicrosoft, mail.ProviderPreview:
	default:
		return "mail connector provider must be \"smtp\", \"gmail\", \"microsoft\", or \"preview\""
	}
	if p.Sender == "" {
		return "mail connector sender (default From address) is required"
	}
	switch p.Provider {
	case mail.ProviderSMTP:
		endpoint, err := mail.NormalizeSMTPEndpoint(p.Endpoint)
		if err != nil {
			return err.Error()
		}
		p.Endpoint = endpoint
	case mail.ProviderPreview:
		// A preview connector dials nothing and authenticates against nothing, so an
		// endpoint or credential written into the form would be dead configuration
		// that reads as if it were in use.
		p.Endpoint, p.CredentialsRef = "", ""
	default:
		if p.CredentialsRef == "" {
			return "a " + p.Provider + " mail connector requires a credentialsRef naming a vault auth bundle"
		}
	}
	return ""
}
