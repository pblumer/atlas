package api

import (
	"strings"

	"github.com/pblumer/atlas/clio"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/mail"
	"github.com/pblumer/atlas/remedy"
	"github.com/pblumer/atlas/sharepoint"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/temis"
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
			s.jobRunner.HandleCompleting(compiler.TemisDecisionJobTypeIndex, temis.Handler(store, s.processLookup, s.temisRegistry, nil))
		},
		rebuild: func(s *Server) error {
			clients, err := s.buildTemisClients()
			if err != nil {
				return err
			}
			s.temisRegistry.Replace(clients)
			return nil
		},
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
			s.jobRunner.Handle(compiler.ClioWriteJobTypeIndex, clio.Handler(store, s.processLookup, s.clioRegistry))
			s.jobRunner.HandleWithOutput(compiler.ClioQueryJobTypeIndex, clio.QueryHandler(store, s.processLookup, s.clioRegistry))
			s.jobRunner.HandleWithOutput(compiler.ClioReadJobTypeIndex, clio.ReadHandler(store, s.processLookup, s.clioRegistry))
		},
		rebuild: func(s *Server) error {
			clients, err := s.buildClioClients()
			if err != nil {
				return err
			}
			s.clioRegistry.Replace(clients)
			return nil
		},
	},
	{
		// An outbound mail connector task sends a model-authored message through a
		// server-registered provider (ADR-0079). The provider host and credential live
		// in the managed connector store; the credential is resolved from the vault at
		// build time (ADR-0041), so a secret never lives in a model.
		name:           connectorKindMail,
		validateCreate: validateMailConnector,
		newRegistry:    func(s *Server) { s.mailRegistry = mail.NewRegistry() },
		registerHandlers: func(s *Server, store *state.Store) {
			s.jobRunner.Handle(compiler.MailJobTypeIndex, mail.Handler(store, s.processLookup, s.mailRegistry))
		},
		rebuild: func(s *Server) error {
			clients, err := s.buildMailClients()
			if err != nil {
				return err
			}
			s.mailRegistry.Replace(clients)
			return nil
		},
	},
	{
		// A SharePoint connector task creates a list item through a server-registered
		// Microsoft Graph provider (ADR-0105) and writes the created item's JSON into the
		// task's result variable (HandleWithOutput). The Graph base and OAuth credential
		// live in the managed connector store; the credential is resolved from the vault
		// at build time (ADR-0041).
		name:           connectorKindSharePoint,
		validateCreate: validateSharePointConnector,
		newRegistry:    func(s *Server) { s.sharePointRegistry = sharepoint.NewRegistry() },
		registerHandlers: func(s *Server, store *state.Store) {
			s.jobRunner.HandleWithOutput(compiler.SharePointJobTypeIndex, sharepoint.Handler(store, s.processLookup, s.sharePointRegistry))
		},
		rebuild: func(s *Server) error {
			clients, err := s.buildSharePointClients()
			if err != nil {
				return err
			}
			s.sharePointRegistry.Replace(clients)
			return nil
		},
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
			s.jobRunner.HandleWithOutput(compiler.RemedyJobTypeIndex, remedy.Handler(store, s.processLookup, s.remedyRegistry))
		},
		rebuild: func(s *Server) error {
			clients, err := s.buildRemedyClients()
			if err != nil {
				return err
			}
			s.remedyRegistry.Replace(clients)
			return nil
		},
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
		k.newRegistry(s)
		if err := k.rebuild(s); err != nil {
			return err
		}
		k.registerHandlers(s, store)
	}
	return nil
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
// naming a vault OAuth auth bundle (ADR-0105). Provider/Sender are mail-only.
func validateSharePointConnector(p *createConnectorParams) string {
	p.Provider, p.Sender = "", ""
	if p.CredentialsRef == "" {
		return "a sharepoint connector requires a credentialsRef naming a vault auth bundle"
	}
	return ""
}

// validateMailConnector validates a mail create request (ADR-0079/0081): the provider
// is SMTP (the default), Gmail, or Microsoft Graph; a sender (default From address) is
// always required; SMTP needs a submission endpoint (host:port) while a native
// provider needs a credentialsRef pointing at a vault auth bundle instead.
func validateMailConnector(p *createConnectorParams) string {
	if p.Provider == "" {
		p.Provider = mail.ProviderSMTP
	}
	switch p.Provider {
	case mail.ProviderSMTP, mail.ProviderGmail, mail.ProviderMicrosoft:
	default:
		return "mail connector provider must be \"smtp\", \"gmail\", or \"microsoft\""
	}
	if p.Sender == "" {
		return "mail connector sender (default From address) is required"
	}
	if p.Provider == mail.ProviderSMTP {
		if p.Endpoint == "" {
			return "smtp mail connector endpoint (host:port) is required"
		}
	} else if p.CredentialsRef == "" {
		return "a " + p.Provider + " mail connector requires a credentialsRef naming a vault auth bundle"
	}
	return ""
}
