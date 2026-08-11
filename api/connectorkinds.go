package api

import (
	"strings"

	"github.com/pblumer/atlas/mail"
)

// managedConnectorKind describes one operator-managed connector kind: how a create
// request for it is validated and how its live client registry is rebuilt from the
// connector store. The ordered slice below is the *single* list every
// connector-management path consults — the create whitelist, the create validation,
// and the registry rebuild all derive from it — so adding a kind is one entry here
// instead of a new arm in the create switch, a new clause in the kind whitelist, and
// a new step in rebuildConnectorRegistries. (Runtime job-worker wiring in server.go is
// still per-kind; consolidating that is a separate step.)
type managedConnectorKind struct {
	// name is the connector.Kind value (e.g. "mail"); see the connectorKind* constants.
	name string
	// validateCreate checks and normalizes a decoded create request for this kind. It
	// may mutate p (default a mail provider, clear the mail-only Provider/Sender fields
	// for kinds that don't use them) and returns a human-readable message when the
	// request is invalid; the empty string means the request is valid.
	validateCreate func(p *createConnectorParams) string
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
// preserved everywhere it is iterated (the rebuild sequence, the whitelist error
// message), so it stays stable across releases.
var managedConnectorKinds = []managedConnectorKind{
	{
		name:           connectorKindTemis,
		validateCreate: validateEndpointOnlyConnector,
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
		name:           connectorKindClio,
		validateCreate: validateEndpointOnlyConnector,
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
		name:           connectorKindMail,
		validateCreate: validateMailConnector,
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
		name:           connectorKindSharePoint,
		validateCreate: validateSharePointConnector,
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
		name:           connectorKindRemedy,
		validateCreate: validateRemedyConnector,
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
