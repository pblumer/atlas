package api

// This file is the inventory of everything Atlas keeps on disk, and what a backup
// owes each of it (ADR-draft-store-registry).
//
// It exists because the previous arrangement could not be kept correct. The
// backup's list of directories lived in one file and the code creating those
// directories lived in twenty others, with nothing connecting them. Adding a store
// did not fail any test, so twelve of them drifted out of the archive — including
// the vault, whose key was backed up while the encrypted secrets it opens were
// not, and the job-type table, whose numbers are what already-stored jobs mean by
// their type. The archive reported success throughout.
//
// The list is still a list. What changed is that it is now the *only* one, that
// backup and restore are derived from it rather than kept alongside it, and that a
// test boots a server and refuses any directory this file does not classify. A
// store added tomorrow cannot be merged without someone deciding what happens to
// it when the disk is gone.

// storeClass says what a backup owes a store. The classes differ in more than
// whether they are copied: they decide which archive carries them, and moving one
// between installations is a different act from restoring one after a disk failure.
type storeClass string

const (
	// classDesignTime is authoring: models, forms, projects, documentation. It is
	// what the design-time export carries, and it is meant to be moved between
	// installations, so it must never contain a secret or a credential.
	classDesignTime storeClass = "design-time"
	// classInstance is authoring and configuration that belongs to *this*
	// installation rather than travelling with an export: process documentation,
	// information models, saved playground scenarios, and the per-server call
	// resolution overrides. A whole-instance snapshot carries it; the portable
	// design-time export does not.
	//
	// The line between this and classDesignTime is a judgement, and for three of the
	// four it is arguably the wrong one — documentation and information models are
	// as much a part of a model as its forms are, and an author moving work between
	// installations would probably expect them. They are here because the reported
	// finding was that the *snapshot* omitted them, and widening the portable export
	// as a side effect of fixing that would be a change nobody asked for. The
	// registry now makes it a visible question rather than an invisible omission.
	classInstance storeClass = "instance"
	// classRuntime is the engine's own durable state — the log and what it needs to
	// come back. Losing it loses running work.
	classRuntime storeClass = "runtime"
	// classIdentity is who may do what: accounts, groups, and the settings that
	// decide access. Restoring without it leaves an installation nobody can enter.
	classIdentity storeClass = "identity"
	// classCredential is a standing permission held by a machine — API tokens,
	// deploy tokens, OAuth clients and grants. Whether a restore should carry these
	// or deliberately revoke them is a decision an operator makes; leaving them out
	// silently is not that decision.
	classCredential storeClass = "credential"
	// classSecret is material that opens other material: the vault and its key.
	// Only the protected full snapshot carries it.
	classSecret storeClass = "secret"
	// classEphemeral is derivable from what the other classes hold, so an archive
	// carrying it would only carry a stale copy. The state store is the example: it
	// is a materialization of the log.
	classEphemeral storeClass = "ephemeral"
)

// storeEntry is one thing on disk and what is owed it.
type storeEntry struct {
	// name is the directory (or file) directly under the data directory.
	name string
	// class decides which archives carry it.
	class storeClass
	// isFile marks the entries that are single files rather than directories.
	isFile bool
	// onDemand marks the ones a booted server does not create until the feature
	// behind them is used. They still have to be backed up when they are there;
	// this only says that their absence is not a missing store.
	onDemand bool
	// ownMechanism marks a store the archive carries through a path of its own
	// rather than by copying the directory whole. The checkpoint root is the case:
	// a snapshot takes the newest checkpoint that *verifies* and only that one,
	// because shipping a checkpoint nobody can check would trade a loud failure for
	// a silent one, and shipping all of them would multiply the archive by their
	// number. Classified, backed up, and not walked.
	ownMechanism bool
	// why records what is lost with it, for the person reading this list while
	// deciding whether their restore is complete.
	why string
}

// persistentStores is the inventory. Adding a store means adding a line here; the
// completeness test will not let it be skipped.
var persistentStores = []storeEntry{
	// --- runtime -----------------------------------------------------------
	{name: "wal", class: classRuntime, why: "the log: every event that ever happened"},
	{name: "checkpoints", class: classRuntime, onDemand: true, ownMechanism: true, why: "the prefix a compacted log no longer carries"},
	{name: "jobtypes", class: classRuntime, why: "the numbers already-stored jobs mean by their type; re-interning assigns different ones"},
	{name: "exporter", class: classRuntime, onDemand: true, why: "how far the event export has read, so a restore does not re-export history"},

	// --- design time -------------------------------------------------------
	{name: "deployments", class: classDesignTime, why: "the deployed process definitions"},
	{name: "drafts", class: classDesignTime, why: "work in progress in the Modeler"},
	{name: "forms", class: classDesignTime, why: "task forms"},
	{name: "projects", class: classDesignTime, why: "applications and their sharing scopes"},
	{name: "panorama-models", class: classDesignTime, why: "landscape models"},
	{name: "releases", class: classDesignTime, why: "published application versions"},
	{name: "dmnrefs", class: classDesignTime, why: "decision references"},
	{name: "dmn-models", class: classDesignTime, onDemand: true, why: "decision models"},
	{name: "public-links", class: classDesignTime, why: "shared links to forms"},
	{name: "connectors", class: classDesignTime, why: "worker definitions"},
	{name: "repository", class: classDesignTime, why: "the shared artifact repository"},
	{name: "inbound-subscriptions", class: classDesignTime, why: "which worker receives which message"},
	{name: "settings", class: classDesignTime, why: "installation settings, including the OIDC claim mapping"},
	{name: "process-docs", class: classInstance, why: "process documentation"},
	{name: "information-models", class: classInstance, why: "the vocabulary data objects are typed against"},
	{name: "playground-scenarios", class: classInstance, why: "saved playground scenarios"},
	{name: "call-overrides", class: classInstance, why: "which process a call activity resolves to on this server"},
	{name: "task-folders", class: classInstance, why: "the saved task views people work out of, and who each is shared with"},

	// --- identity ----------------------------------------------------------
	{name: "users", class: classIdentity, why: "accounts and their roles"},
	{name: "groups", class: classIdentity, why: "group membership, which grants project access"},

	// --- credentials -------------------------------------------------------
	{name: "api-tokens", class: classCredential, why: "machine access to the API"},
	{name: "deploy-tokens", class: classCredential, why: "what a peer Atlas publishes with"},
	{name: "oauth-clients", class: classCredential, why: "registered OAuth clients"},
	{name: "oauth-grants", class: classCredential, why: "standing OAuth authorizations"},
	{name: "grant-audit", class: classCredential, why: "the trail of who was granted what"},
	{name: "targets", class: classCredential, why: "where workers send, and what they authenticate with"},

	// --- secrets -----------------------------------------------------------
	{name: "vault", class: classSecret, why: "the encrypted secrets themselves"},
	{name: "vault.key", class: classSecret, isFile: true, why: "the key that opens the vault; useless without it, and it without the vault"},

	// --- rebuildable -------------------------------------------------------
	{name: "state", class: classEphemeral, why: "a materialization of the log; recovery rebuilds it"},
}

// storeClassOf reports how an entry directly under the data directory is
// classified, and whether it is classified at all.
func storeClassOf(name string) (storeClass, bool) {
	for _, e := range persistentStores {
		if e.name == name {
			return e.class, true
		}
	}
	return "", false
}

// backupDirs is the design-time export: what an author moves between
// installations. Derived, so it cannot drift from the inventory — and by
// construction it carries no secret and no credential.
func backupDirs() []string {
	return namesOfClass(func(e storeEntry) bool { return e.class == classDesignTime && !e.isFile })
}

// fullBackupDirs is every directory a whole-instance snapshot carries: everything
// that cannot be rebuilt from the rest.
func fullBackupDirs() []string {
	return namesOfClass(func(e storeEntry) bool {
		return e.class != classEphemeral && !e.isFile && !e.ownMechanism
	})
}

// fullBackupFiles is the same for the entries that are single files.
func fullBackupFiles() []string {
	return namesOfClass(func(e storeEntry) bool { return e.class != classEphemeral && e.isFile })
}

func namesOfClass(keep func(storeEntry) bool) []string {
	out := make([]string, 0, len(persistentStores))
	for _, e := range persistentStores {
		if keep(e) {
			out = append(out, e.name)
		}
	}
	return out
}
