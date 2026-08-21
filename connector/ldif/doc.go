// Package ldif reads and writes directory entries held in a file — LDIF (RFC 2849)
// and DSML v1 (ADR-0171).
//
// # Why this is not the text-file connector
//
// ADR-0139's connector covers the *table* formats: delimited, fixed-width, and
// attribute-value pairs all describe rows of fields, and a process gets back a list
// of row objects whichever one the file arrived in. LDIF and DSML do not describe
// rows. They describe **directory entries** — a distinguished name plus multi-valued
// attributes — and they produce
//
//	{"dn": "...", "attributes": {"cn": ["Ada"], "objectClass": ["top", "person"]}}
//
// which is the same shape [connector/ldap]'s search and [connector/ad]'s DirSync
// delta return. That is the point of keeping them apart: an LDIF read feeds exactly
// the downstream handling a live directory read does, and a connector whose result
// shape depended on a dropdown would have made that impossible to rely on.
//
// # Both directions
//
// A file is as often produced as consumed. Writing LDIF is how a process hands a
// change set to a directory it cannot reach itself, which is the usual shape of a
// migration or an offline provisioning run.
package ldif
