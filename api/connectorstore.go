package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// connectorKindTemis is the central DMN decision connector kind (ADR-0050);
// connectorKindClio is the clio event-store connector kind (ADR-0036). Both are
// wired into the runtime and configurable in the Console: a connector record of
// either kind resolves to a live client with its token from the vault. The
// http.rest kind is model-authored (its endpoint lives in the model, not a record),
// so it is not a managed kind here.
// connectorKindMail is the outbound mail connector kind (ADR-0079/0081): a managed
// record of this kind resolves to a live mail client whose credential is read from
// the vault. Like clio, its provider and secret are managed here, never in the model;
// only the message (recipients, subject, body) is model-authored. The provider is
// SMTP (the default), Gmail, Microsoft Graph, or preview — see mail.Provider* and
// mail.NewProviderClient, which own provider dispatch.
// connectorKindSharePoint is the SharePoint connector kind (ADR-0141): a managed
// record of this kind resolves to a live Microsoft Graph client whose OAuth
// credential is read from the vault. Like mail, its Graph base and secret are managed
// here, never in the model; only the target (site, list, item fields) is
// model-authored. See sharepoint.NewProviderClient, which owns provider dispatch.
// connectorKindRemedy is the BMC Remedy connector kind (ADR-0106): a managed record
// of this kind resolves to a live Remedy AR System client whose credential bundle
// (username/password JSON) is read from the vault. Like clio and mail, its base URL
// and credentials are managed here, never in the model; only the form and its field
// values are model-authored.
// connectorKindJira is the Atlassian Jira connector kind (ADR-0201): a
// managed record of this kind resolves to a live Jira REST client whose credential
// bundle — {email, apiToken} for Jira Cloud or {token} for a Data Center personal
// access token — is read from the vault. Like Remedy, its base URL and credential are
// managed here, never in the model; only the operation and its values are
// model-authored.
const (
	connectorKindTemis      = "temis"
	connectorKindClio       = "clio"
	connectorKindMail       = "mail"
	connectorKindSharePoint = "sharepoint"
	connectorKindRemedy     = "remedy"
	connectorKindJira       = "jira"
	connectorKindEntra      = "entra"
	// connectorKindAD is the Active Directory connector kind
	// (ADR-draft-ad-as-a-console-connector). A record holds the directory's LDAP URL
	// and a credentialsRef naming a vault {bindDN, password} bundle; the model names
	// the record and nothing else about the directory. Worker-only like Entra: the
	// engine never binds, so the service account never enters it.
	connectorKindAD = "ad"
)

// connector is a managed connector instance: an operator-configured, durable
// integration Atlas delegates to (ADR-0041). It holds a name a model refers to, an
// endpoint, and — per the secret model — only a *reference* to a credential
// (CredentialsRef), never the secret value; the token is resolved from the
// environment at runtime. Disabled instances are kept but not registered.
type connector struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Endpoint       string `json:"endpoint"`
	CredentialsRef string `json:"credentialsRef,omitempty"`
	Enabled        bool   `json:"enabled"`
	CreatedAt      int64  `json:"createdAt"`
	// Provider and Sender apply to a mail connector (Kind == connectorKindMail,
	// ADR-0079/0081). Provider selects the transport ("smtp", "gmail", or
	// "microsoft"; empty defaults to SMTP). Sender is the default From address a mail
	// task falls back to when it authors no sender — and, for SMTP, the auth username;
	// for a native provider, the mailbox to send as. Both are empty for the other
	// kinds. As with every kind, only a credential *reference* is stored here, never
	// the secret: for a native provider CredentialsRef names a vault auth bundle
	// (client secret, refresh token, or service-account key), never a value (I6).
	Provider string `json:"provider,omitempty"`
	Sender   string `json:"sender,omitempty"`
}

// connectorStore is a durable store for managed connector instances, one JSON file
// per id under a single directory — the same on-disk sidecar approach as the
// deployment/draft/dmn-ref stores (ADR-0019/0034/0041). Like them it is owned
// solely by the server's run-loop goroutine, so it needs no locking; and it holds
// no secret material, only references (I6).

// connectorStore is a durable store for connector records, one JSON file per id
// under a single directory (ADR-0019). Like every design-time store it is owned
// solely by the server's run-loop goroutine, so it needs no locking of its own.
type connectorStore = sidecar.Store[connector]

// newConnectorStore opens (creating if needed) the connector directory.
func newConnectorStore(dir string) (*connectorStore, error) {
	return sidecar.NewStore(dir, "connectorstore",
		func(rec connector) string { return rec.ID },
		sidecar.Order(func(a, b connector) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
}
