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
	// (ADR-0206). A record holds the directory's LDAP URL
	// and a credentialsRef naming a vault {bindDN, password} bundle; the model names
	// the record and nothing else about the directory. Worker-only like Entra: the
	// engine never binds, so the service account never enters it.
	connectorKindAD = "ad"
)

// worker is an operator-managed Worker (ADR-0203): an instance of a Worker Type
// configured with the endpoint, provider and credential reference Atlas uses to
// execute job-backed work. It is the canonical in-process name for what the legacy
// HTTP/API surface still calls a connector instance.
//
// Persisted JSON deliberately stays byte-compatible with the historical connector
// record. CredentialsRef is only a reference to secret material, never the secret
// value itself (I6); disabled Workers remain durable but are not registered.
type worker struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Endpoint       string `json:"endpoint"`
	CredentialsRef string `json:"credentialsRef,omitempty"`
	Enabled        bool   `json:"enabled"`
	CreatedAt      int64  `json:"createdAt"`
	// Provider and Sender apply to a mail Worker (Kind == connectorKindMail,
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

// connector is the compatibility name used by the existing connector-oriented API
// and implementation while ADR-0203 is migrated incrementally. It is an alias, not a
// second record type, so connector and Worker code share one JSON representation and
// one durable store.
type connector = worker

// workerStore is the durable store for configured Workers. The directory and JSON
// shape intentionally remain the historical connector-store format: this migration
// changes terminology only and must not fork or rewrite persisted design-time state.
// Like every design-time store it is owned solely by the server run loop and needs no
// locking of its own (I3).
type workerStore = sidecar.Store[worker]

// connectorStore is the compatibility name retained for existing callers until the
// connector-management API migration in ADR-0203 is complete.
type connectorStore = workerStore

// newWorkerStore opens (creating if needed) the configured-Worker directory. The
// on-disk store name remains "connectorstore" so existing error text and operational
// diagnostics do not change during the compatibility window.
func newWorkerStore(dir string) (*workerStore, error) {
	return sidecar.NewStore(dir, "connectorstore",
		func(rec worker) string { return rec.ID },
		sidecar.Order(func(a, b worker) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
}

// newConnectorStore is the compatibility constructor used by the existing Server
// wiring. It delegates to the canonical Worker store and therefore cannot create a
// second source of truth.
func newConnectorStore(dir string) (*connectorStore, error) {
	return newWorkerStore(dir)
}
