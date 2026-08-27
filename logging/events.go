package logging

import (
	"log/slog"
	"regexp"
	"sort"
)

// The event catalogue: every operational thing Atlas says about itself, named once.
//
// These names are an API. An operator writes an alert against `event=checkpoint.failed`
// and expects it to keep working across upgrades, exactly as they expect
// `atlas_checkpoint_position` to (ADR-0142). So renaming one is a breaking change and
// belongs in the changelog under _Changed_, while the sentence beside it can be reworded
// freely — that is the whole reason the name is a separate field.
//
// The shape is `subject.thing_that_happened`, lower_snake_case on both sides. Subjects
// match the subsystem an operator already knows from the flags and the metrics:
// checkpoint, wal_compaction, exporter, retention, vault, auth, server.

// Event is a registered log event name.
//
// It is a struct with an unexported field on purpose. A caller outside this package
// cannot write Event{name: "made.up"}, so the catalogue below is the complete set of
// names that can ever be logged — the contract is enforced by the compiler rather than
// by a review comment (invariant I5, compile don't interpret). The one value an outside
// caller can forge is the zero Event, and that logs as "unregistered" rather than as an
// empty field.
type Event struct{ name string }

// String returns the event name.
func (e Event) String() string {
	if e.name == "" {
		return unregisteredEvent
	}
	return e.name
}

// Attr returns the event as the attribute it is logged under.
func (e Event) Attr() slog.Attr { return slog.String(eventKey, e.String()) }

// registry holds every declared event, so a test can check the whole catalogue at once
// and newEvent can reject a duplicate.
var registry = map[string]bool{}

// eventShape is the naming rule, applied at init rather than in review.
var eventShape = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// newEvent declares an event, panicking on a malformed or duplicate name. A panic at
// package init is a boot failure in every binary and every test — which is the point:
// two subsystems quietly sharing one name would make an operator's alert fire for the
// wrong thing, and that is not a defect worth discovering in production.
func newEvent(name string) Event {
	if !eventShape.MatchString(name) {
		panic("logging: malformed event name " + name + " (want subject.thing_that_happened)")
	}
	if registry[name] {
		panic("logging: duplicate event name " + name)
	}
	registry[name] = true
	return Event{name: name}
}

// Events returns every registered event name, sorted.
func Events() []string {
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Process lifecycle.
var (
	// ServerListening is emitted once the HTTP listener is up. It comes *after*
	// recovery — the port stays closed until the log has been replayed (slice 7) — so
	// it doubles as the "this instance finished starting" signal.
	ServerListening    = newEvent("server.listening")
	ServerShuttingDown = newEvent("server.shutting_down")
	ServerDocsEnabled  = newEvent("server.docs_enabled")
	ServerMetrics      = newEvent("server.metrics_enabled")
	DataDirOpened      = newEvent("server.data_dir_opened")
	// AuthDisabled is a server started with --auth=false: no login is required for
	// anything. It is a WARN and it is loud because it is now the deliberate
	// exception rather than the default — the one line that says this instance is
	// open to whoever can reach the port (ADR-0195).
	AuthDisabled = newEvent("auth.disabled")

	// The security audit trail (ADR-0197). Atlas's
	// business trails were always strong — every state transition, every variable
	// override, every task claim, each with its actor — but who signed in, who
	// failed to, and who changed an account or a credential was written down
	// nowhere. That is the first thing an audit asks for and the last thing these
	// events leave unanswered.
	//
	// Each carries the acting principal and the client address, and none of them
	// carries a secret: no password, no token, not even a truncated one. What is an
	// attribute here is what a log shipper extracts, indexes and keeps.
	AuthLogin          = newEvent("auth.login")
	AuthLoginFailed    = newEvent("auth.login_failed")
	AuthLoginThrottled = newEvent("auth.login_throttled")
	AuthLogout         = newEvent("auth.logout")
	// AuthDenied is an authenticated caller refused for lacking a role — an
	// administration attempt by somebody who may not administer, which is a
	// different and much rarer signal than an anonymous request being asked to log
	// in. Only the authorization refusal is recorded; logging every 401 would bury
	// it under every unauthenticated probe on the internet.
	AuthDenied = newEvent("auth.denied")
	// The account lifecycle, and the credentials that are not accounts.
	AuthUserCreated  = newEvent("auth.user_created")
	AuthUserUpdated  = newEvent("auth.user_updated")
	AuthUserDeleted  = newEvent("auth.user_deleted")
	AuthPasswordSet  = newEvent("auth.password_set")
	AuthTokenMinted  = newEvent("auth.token_minted")
	AuthTokenRevoked = newEvent("auth.token_revoked")
	// The OAuth authorization server (ADR-0200). Registering a client is an admin
	// act like minting a token; the rest is one person deciding, which is the event
	// an audit of "who let that application in" is looking for. AuthOAuthDenied
	// covers both halves of a refusal — the person declining, and the server
	// refusing a request that never should have been made — because for an operator
	// reading a log they are the same question: why did this not connect.
	AuthOAuthClientRegistered = newEvent("auth.oauth_client_registered")
	AuthOAuthClientDeleted    = newEvent("auth.oauth_client_deleted")
	AuthOAuthGranted          = newEvent("auth.oauth_granted")
	AuthOAuthDenied           = newEvent("auth.oauth_denied")
	AuthOAuthTokenIssued      = newEvent("auth.oauth_token_issued")
	AuthOAuthGrantRevoked     = newEvent("auth.oauth_grant_revoked")
	// AuthWorkerTokenUnknown is an operator-set ATLAS_TOKEN that this server does
	// not accept. The supervisor honours the variable and stops injecting its own,
	// so the workers it starts would hold a credential refused at every poll — a
	// trap that used to be silent because no value could ever have worked
	// (ADR-0194).
	AuthWorkerTokenUnknown = newEvent("auth.worker_token_unknown")
	// CommandFailed is a top-level command exiting non-zero.
	CommandFailed = newEvent("command.failed")
	MCPProxying   = newEvent("mcp.proxying")
	// WorkerStarting is the out-of-process job worker announcing what it will serve
	// and for which server (ADR-0157).
	WorkerStarting = newEvent("worker.starting")
	// WorkerPollFailed is a worker reporting that a poll failed and will be retried.
	WorkerPollFailed = newEvent("worker.poll_failed")
	// WorkerSupervisorStarted and WorkerSupervisorFailed report the lifecycle of a
	// worker process Atlas launched itself (ADR-0157 step 7).
	WorkerSupervisorStarted = newEvent("worker.supervised_started")
	WorkerSupervisorFailed  = newEvent("worker.supervise_failed")
	// ADMockEnabled is an AD worker announcing that it serves the Active Directory
	// connector against a directory in its own memory rather than a real one
	// (ADR-0181). It is a warning rather than an info because
	// a mock worker is indistinguishable from a working one everywhere else: it
	// completes every job it leases.
	ADMockEnabled = newEvent("ad_mock.enabled")
	// ADMockPerformed is one operation that mock directory simulated. It is what a
	// mockup run leaves behind for the person who ran it, in the worker's log where
	// the Workers console shows it.
	ADMockPerformed = newEvent("ad_mock.performed")
	// ADMockSeedUnusable is a mock directory that could not read the seed it was
	// pointed at, and started empty instead. It is a warning and not a refusal
	// because a mock touches nothing real: an empty directory costs a joiner nothing
	// and costs a leaver one visible incident, whereas refusing takes the worker down
	// for every AD task at once — which is what an optional field with a typo in it
	// used to do (ADR-draft-atlas-manages-the-ad-mock-seed).
	ADMockSeedUnusable = newEvent("ad_mock.seed_unusable")
	// WorkerHistoryFailed is the job-history exporter reporting that an append did not
	// reach its clio connector, or that its buffer is dropping entries. Both are
	// warnings rather than errors on purpose: the history is telemetry, and the engine
	// deliberately does not wait for it, so a gap costs a run nothing.
	WorkerHistoryFailed = newEvent("worker.history_failed")
)

// Recovery checkpoints and WAL compaction (ADR-0131).
var (
	CheckpointEnabled     = newEvent("checkpoint.enabled")
	CheckpointPublished   = newEvent("checkpoint.published")
	CheckpointFailed      = newEvent("checkpoint.failed")
	CheckpointPruneFailed = newEvent("checkpoint.prune_failed")

	WALCompactionEnabled = newEvent("wal_compaction.enabled")
	// WALCompactionInert is compaction configured *without* checkpointing, which does
	// nothing at all: the cut is derived from a checkpoint. It warrants a warning
	// precisely because the flag makes it look enabled.
	WALCompactionInert           = newEvent("wal_compaction.inert")
	WALCompactionFailed          = newEvent("wal_compaction.failed")
	WALCompactionWatermarkFailed = newEvent("wal_compaction.watermark_unavailable")
	WALCompactionSegmentsDeleted = newEvent("wal_compaction.segments_deleted")
)

// Backup, restore, and streaming exports (ADR-0107/0108/0109).
var (
	RestoreApplied                = newEvent("restore.applied")
	BackupStreamFailed            = newEvent("backup.stream_failed")
	FullBackupStreamFailed        = newEvent("full_backup.stream_failed")
	ApplicationSourceStreamFailed = newEvent("application_source.stream_failed")
)

// History retention (ADR-0115/0144) and the OpenSearch exporter (ADR-0114).
var (
	RetentionEnabled   = newEvent("retention.enabled")
	RetentionPurged    = newEvent("retention.purged")
	ExporterEnabled    = newEvent("exporter.enabled")
	ExporterIndexed    = newEvent("exporter.indexed")
	ExporterTickFailed = newEvent("exporter.tick_failed")
)

// Identity, secrets, and provisioning (ADR-0044/0070/0123).
var (
	VaultKeyGenerated = newEvent("vault.key_generated")

	// JobTypeIndexCollision reports a stored job-type assignment whose index the
	// reserved range has since grown over. Warned at startup rather than swallowed:
	// jobs already on disk carry the old index, so the drop is not cosmetic.
	JobTypeIndexCollision        = newEvent("jobtype.index_collision")
	AuthAdminSeeded              = newEvent("auth.admin_seeded")
	AuthPasswordReset            = newEvent("auth.password_reset")
	UserProvisioningUserCreated  = newEvent("user_provisioning.user_created")
	UserProvisioningPasswordSet  = newEvent("user_provisioning.password_set")
	UserProvisioningUserDisabled = newEvent("user_provisioning.user_disabled")
)

// Distributed traces (ADR-0142 slice 8b).
var (
	TracingEnabled        = newEvent("tracing.enabled")
	TracingShutdownFailed = newEvent("tracing.shutdown_failed")
)

// Everything else the running server reports about itself.
var (
	// DeploymentReloadedWithProblems reports a stored definition — or a DMN model
	// bundled with one, told apart by the artifact attribute — that today's
	// deploy-time checks would refuse, brought back anyway because it passed the gate
	// of the day it was deployed and its instances are running under it
	// (ADR-0177). Warned rather than swallowed: the
	// model is drifting from what the compiler now asks for, and the next deploy of
	// it will be refused with the author watching.
	DeploymentReloadedWithProblems = newEvent("deployment.reloaded_with_problems")

	ScriptWorkerEnabled      = newEvent("script_worker.enabled")
	ScriptWorkerMissing      = newEvent("script_worker.binary_missing")
	CallOverrideSkipped      = newEvent("call_override.skipped")
	CollabParticipantsReaped = newEvent("collab.participants_reaped")
)
