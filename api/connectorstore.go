package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// connectorKindTemis is the central DMN decision Worker Type (ADR-0050);
// connectorKindClio is the clio event-store Worker Type (ADR-0036). Both are
// wired into the runtime and configurable in the Console: a worker record of
// either kind resolves to a live client with its token from the vault. The
// http.rest kind is model-authored (its endpoint lives in the model, not a record),
// so it is not a managed kind here.
// connectorKindMail is the outbound mail Worker Type (ADR-0079/0081): a managed
// record of this kind resolves to a live mail client whose credential is read from
// the vault. Like clio, its provider and secret are managed here, never in the model;
// only the message (recipients, subject, body) is model-authored. The provider is
// SMTP (the default), Gmail, Microsoft Graph, or preview — see mail.Provider* and
// mail.NewProviderClient, which own provider dispatch.
// connectorKindSharePoint is the SharePoint Worker Type (ADR-0141): a managed
// record of this kind resolves to a live Microsoft Graph client whose OAuth
// credential is read from the vault. Like mail, its Graph base and secret are managed
// here, never in the model; only the target (site, list, item fields) is
// model-authored. See sharepoint.NewProviderClient, which owns provider dispatch.
// connectorKindRemedy is the BMC Remedy Worker Type (ADR-0106): a managed record
// of this kind resolves to a live Remedy AR System client whose credential bundle
// (username/password JSON) is read from the vault. Like clio and mail, its base URL
// and credentials are managed here, never in the model; only the form and its field
// values are model-authored.
// connectorKindJira is the Atlassian Jira Worker Type (ADR-0201): a
// managed record of this kind resolves to a live Jira REST client whose credential
// bundle — {email, apiToken} for Jira Cloud or {token} for a Data Center personal
// access token — is read from the vault. Like Remedy, its base URL and credential are
// managed here, never in the model; only the operation and its values are
// model-authored.
// connectorKindGoogleSheets is the Google Sheets Worker Type
// (ADR-0235): a configured record of this kind resolves to a
// live client speaking Sheets v4 and Drive v3, whose OAuth credential bundle — a
// service account's {clientEmail, privateKey}, or a {clientId, clientSecret,
// refreshToken} for a consumer account — is read from the vault. Like Jira, only the
// operation and its values are model-authored; the credential is configured here and
// never in a model. (The identifier keeps the connectorKind* prefix its siblings in
// this block carry; renaming that family is its own step of the ADR-0203 migration.)
const (
	connectorKindTemis      = "temis"
	connectorKindClio       = "clio"
	connectorKindMail       = "mail"
	connectorKindSharePoint = "sharepoint"
	connectorKindRemedy     = "remedy"
	connectorKindJira       = "jira"
	// Documented above the block: it needs no endpoint, only a credentialsRef, because
	// Google's API bases are not a per-tenant address.
	connectorKindGoogleSheets = "googlesheets"
	// connectorKindDiscord is the Discord Worker Type
	// (ADR-0258): a configured record of this kind resolves to a live
	// Discord API client whose bot token — a {botToken} bundle — is read from the vault.
	// Like Google Sheets it needs no endpoint, because Discord's API base is not a
	// per-tenant address; the field stays an override for an operator behind a proxy.
	// Only the operation and its values are model-authored.
	connectorKindDiscord = "discord"
	connectorKindEntra   = "entra"
	// connectorKindAD is the Active Directory Worker Type
	// (ADR-0206). A record holds the directory's LDAP URL
	// and a credentialsRef naming a vault {bindDN, password} bundle; the model names
	// the record and nothing else about the directory. Worker-only like Entra: the
	// engine never binds, so the service account never enters it.
	connectorKindAD = "ad"

	// connectorKindAgent is an agent model an agent-driven ad-hoc subprocess asks
	// (ADR-0253/ADR-0254, ADR-0255). A record holds
	// the endpoint, the wire format in Provider, the model name in Model, and a
	// credentialsRef naming the vault key holding the API key. Worker-only for the
	// clearest reason ADR-0164 has: a round is one model call, minutes long and able to
	// hang, so it never runs in the engine process.
	connectorKindAgent = "agent"
)

// configuredWorker is an operator-managed Worker (ADR-0203): an instance of a
// Worker Type configured with the endpoint, provider and credential reference Atlas
// uses to execute job-backed work. The explicit name distinguishes this durable
// design-time configuration from runtime Worker Instances and from the worker package.
//
// Persisted JSON deliberately stays byte-compatible with the historical worker
// record. CredentialsRef is only a reference to secret material, never the secret
// value itself (I6); disabled Workers remain durable but are not registered.
type configuredWorker struct {
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

	// Model is which model an agent Worker asks (Kind == connectorKindAgent,
	// ADR-0255). It is the first piece of a Worker's
	// configuration that is neither an endpoint nor a credential nor derivable from
	// either, and it is deliberately here rather than in the vault bundle behind
	// CredentialsRef: it is not a secret, and it is the single most-changed setting an
	// agent has — an operator weighing cost against capability changes the model, and
	// must be able to see what it is set to without opening a secret store.
	//
	// Empty for every other kind, and empty on an agent record written before this,
	// which reads as "the protocol's default" — exactly what an unconfigured Messages
	// Worker already means. The Chat-Completions adapter has no default and says so at
	// startup, which is why validateAgentConnector insists on one for that protocol.
	Model string `json:"model,omitempty"`

	// Ownership and sharing (ADR-0205, measure M11). The three fields are ADR-0071's
	// for a project, reused verbatim rather than reinvented, so a group grant
	// (ADR-0180) works here with no further thought and there is one sharing
	// vocabulary in the product instead of two.
	//
	// A record written before this carries none of them, and that is a meaningful
	// state rather than a gap: an ownerless worker is an administrator's to manage
	// until one is assigned. See connectorRole for why this departs from ADR-0071's
	// treatment of legacy artifacts.
	//
	// Nothing the runtime does consults these. A service task resolving a worker
	// by name, the registries and the inbound bridge read the store directly:
	// execution is not authoring.
	OwnerID    string          `json:"ownerId,omitempty"`
	Visibility string          `json:"visibility,omitempty"` // "private" | "shared"
	Members    []projectMember `json:"members,omitempty"`
	UpdatedAt  int64           `json:"updatedAt,omitempty"`
}

// connector is the compatibility name used by the existing connector-oriented API
// and implementation while ADR-0203 is migrated incrementally. It is an alias, not a
// second record type, so worker and configured-Worker code share one JSON
// representation and one durable store.
type connector = configuredWorker

// configuredWorkerStore is the durable store for configured Workers. The directory
// and JSON shape intentionally remain the historical connector-store format: this
// migration changes terminology only and must not fork or rewrite persisted
// design-time state. Like every design-time store it is owned solely by the server
// run loop and needs no locking of its own (I3).
type configuredWorkerStore = sidecar.Store[configuredWorker]

// connectorStore is the compatibility name retained for existing callers until the
// connector-management API migration in ADR-0203 is complete.
type connectorStore = configuredWorkerStore

// newConfiguredWorkerStore opens (creating if needed) the configured-Worker
// directory. The on-disk store name remains "connectorstore" so existing error text
// and operational diagnostics do not change during the compatibility window.
func newConfiguredWorkerStore(dir string) (*configuredWorkerStore, error) {
	return sidecar.NewStore(dir, "connectorstore",
		func(rec configuredWorker) string { return rec.ID },
		sidecar.Order(func(a, b configuredWorker) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
}

// newConnectorStore is the compatibility constructor used by the existing Server
// wiring. It delegates to the canonical configured-Worker store and therefore cannot
// create a second source of truth.
func newConnectorStore(dir string) (*connectorStore, error) {
	return newConfiguredWorkerStore(dir)
}
