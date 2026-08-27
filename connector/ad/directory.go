package ad

import "github.com/pblumer/atlas/connector/clientreg"

// Directory is one Active Directory an operator configured in the Console: where it
// is, and who binds to it.
//
// It exists because AD sat on the wrong side of a line this repository otherwise draws
// cleanly (ADR-draft-ad-as-a-console-connector). A connector whose target carries
// credentials — mail, Entra, Remedy, SharePoint, the three SQL products — is a *record*
// an operator creates, referenced from a model by name. A connector addressed per call
// with nothing secret about the address — REST, web scrape — carries its target in the
// model. AD is the first kind: a domain controller, a service account and a password.
// It got the model-authored shape by inheritance from the LDAP connector rather than
// from an argument, and paid for it twice — an operator could not create one like any
// other connector, and the mockup switch had nowhere per-directory to live.
//
// The password is a *value* here, not a reference. That is the point of the boundary:
// the engine resolves references out of its vault and renders what a worker needs into
// the worker's environment (ADR-0157/0168), so by the time a directory reaches this
// struct the resolution has already happened, in the process that can do it.
type Directory struct {
	// URL is the LDAP endpoint, ldap:// or ldaps://. A password set needs ldaps:// or
	// StartTLS, and the mock refuses an unencrypted one exactly as AD does.
	URL string
	// BindDN and Password authenticate the bind. Both come out of one vault bundle, so
	// the record itself holds neither — the Entra and Remedy shape (ADR-0172/0106).
	BindDN   string
	Password string
	// StartTLS upgrades a plain connection, for a directory that does not serve ldaps.
	StartTLS bool
}

// Registry is the set of directories a worker holds, by name. Like every other
// connector registry it is a clientreg.Registry, so an unconfigured name produces the
// same "not configured" refusal across kinds rather than one message per connector.
type Registry = clientreg.Registry[Directory]

// NewRegistry returns an empty directory registry.
func NewRegistry() *Registry { return clientreg.New[Directory]() }
