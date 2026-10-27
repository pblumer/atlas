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
	// ServerCatalogueDisabled is a server started with --catalogue=false: the shop,
	// the catalogue, the orders and the inventory are not served
	// (ADR-0434). One line at start, so the
	// question "why is there no shop" has an answer in the log.
	ServerCatalogueDisabled = newEvent("server.catalogue_disabled")
	// ServerCatalogueInFlight is that start finding processes still working orders —
	// the shop's fulfilment and approval processes, or a product's provisioning — that
	// will fail at their next call to the order routes (ADR-0434). A WARN, written only
	// when there are any: it names how many and which, so the operator meets the
	// consequence before the incidents do.
	ServerCatalogueInFlight = newEvent("server.catalogue_disabled_in_flight")
	// ServerCataloguePurgeEnabled is a server that lets an administrator delete a
	// catalogue or a product for good, with its orders and held rights
	// (ADR-0469). The purge is an opt-out, so this is every server
	// started without --catalogue-purge=false — which a production installation must
	// be. A WARN at every start, so an installation upgraded without the flag says so
	// in the log, and an alert can match it.
	ServerCataloguePurgeEnabled = newEvent("server.catalogue_purge_enabled")
	// The operator-supplied certificate, where this server terminates TLS itself
	// (ADR-0191). ServerTLSReloaded is one line per renewal picked up without a
	// restart; ServerTLSReloadFailed is the pair that changed on disk and could not
	// be loaded, which leaves the previous certificate in service rather than
	// refusing handshakes — so it is a WARN an operator must act on before the
	// certificate that is still being served expires.
	ServerTLSReloaded     = newEvent("server.tls_reloaded")
	ServerTLSReloadFailed = newEvent("server.tls_reload_failed")
	// ServerTLSHandshakeAborted is a connection to the TLS listener that closed before
	// its handshake began — net/http's "TLS handshake error … EOF". A load balancer's
	// TCP health check produces one per interval, so it is DEBUG: true, countable, and
	// not news (ADR-0448). A handshake that failed for a reason keeps
	// net/http's own wording at INFO.
	ServerTLSHandshakeAborted = newEvent("server.tls_handshake_aborted")
	// ServerTrustedProxies is said once at startup where --trusted-proxies names any:
	// the list as the server understood it, so whoever reads an audit line's client_ip
	// later can see whose word it was taken on (ADR-0448).
	ServerTrustedProxies = newEvent("server.trusted_proxies")
	DataDirOpened        = newEvent("server.data_dir_opened")
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
	// AuthLoginDateUnrecorded is a login day that could not be written, read or
	// removed (ADR-0477). The login itself had succeeded;
	// what is missing or stale is the roster's "last login", which the account's next
	// login writes again.
	AuthLoginDateUnrecorded = newEvent("auth.login_date_unrecorded")
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
	// AuthOIDCConfigured is said once at startup where an operator named an identity
	// provider. It is the line that tells whoever reads the log after a failed login
	// which issuer this server was asking, which is the first thing to check.
	AuthOIDCConfigured = newEvent("auth.oidc_configured")
	// AuthOIDCMappingSet records a change to the mapping from a provider's claims
	// onto Atlas roles and groups. It is the change that explains every role change
	// after it: from the moment the mapping is on, whoever administers the provider's
	// groups administers this instance's roles.
	AuthOIDCMappingSet = newEvent("auth.oidc_mapping_set")
	// ConstantsChanged records a change to the constants this installation holds — an
	// application's, or its own for every application (ADR-0456):
	// what every new instance reading them starts with. The names changed are
	// attributes; the values are in the Console's audit log, which keeps the history,
	// rather than in a stream shipped elsewhere.
	ConstantsChanged = newEvent("constants.changed")
	// The directory mirror (ADR-0332). AuthDirectorySync
	// is one line per run, written whether or not the run wrote anything: a report-only
	// run still read a whole tenant out of a directory, which is an event even though
	// nothing changed here. Its attributes are counts and the mode; the Graph cursor is
	// deliberately absent, because a URL naming a tenant does not belong in a log that
	// is shipped elsewhere. The accounts it creates and changes are recorded under the
	// existing auth.user_created and auth.user_updated, so an alert written against
	// those keeps working whoever made the change.
	AuthDirectorySync = newEvent("auth.directory_sync")
	// AuthDirectoryGroupSynced is one mirrored group written. It is its own name rather
	// than a general group event, because it says exactly what it covers: nothing here
	// claims to record the group edits an administrator makes by hand.
	AuthDirectoryGroupSynced = newEvent("auth.directory_group_synced")
	// AuthRolesUpgraded is said once, on the first start after roles per endpoint
	// group shipped, naming how many accounts kept what they could already do. It is
	// the line an operator needs to see: nothing is narrower than it was yesterday
	// until somebody narrows it.
	AuthRolesUpgraded = newEvent("auth.roles_upgraded")
	// The OAuth authorization server (ADR-0200). Registering a client is an admin
	// act like minting a token; the rest is one person deciding, which is the event
	// an audit of "who let that application in" is looking for. AuthOAuthDenied
	// covers both halves of a refusal — the person declining, and the server
	// refusing a request that never should have been made — because for an operator
	// reading a log they are the same question: why did this not connect.
	AuthOAuthClientRegistered = newEvent("auth.oauth_client_registered")
	// AuthOAuthClientSelfRegistered is a client that registered *itself* (RFC 7591),
	// which only happens where an operator opened that. Kept apart from the admin
	// act above rather than folded into it: "an administrator added an application"
	// and "a stranger added one" are the same sentence with entirely different
	// consequences, and an audit that cannot tell them apart is not one.
	AuthOAuthClientSelfRegistered = newEvent("auth.oauth_client_self_registered")
	AuthOAuthClientDeleted        = newEvent("auth.oauth_client_deleted")
	AuthOAuthGranted              = newEvent("auth.oauth_granted")
	AuthOAuthDenied               = newEvent("auth.oauth_denied")
	AuthOAuthTokenIssued          = newEvent("auth.oauth_token_issued")
	AuthOAuthGrantRevoked         = newEvent("auth.oauth_grant_revoked")
	// AuthOAuthRegistrationOpen is said once at startup where an operator opened
	// self-registration, the way AuthDisabled is said for a server without a login.
	// A setting that widens what an unauthenticated caller may do is one nobody
	// should be able to inherit without being told (ADR-0200).
	AuthOAuthRegistrationOpen = newEvent("auth.oauth_registration_open")
	// Worker ownership (ADR-0205). Who may reach a worker's configuration —
	// its endpoint, its credential reference and its inbound subscriptions — is now
	// somebody's decision rather than everybody's, so the decision is recorded.
	// Sharing and withdrawing are separate events because "who was let in" and "who
	// was shut out" are separate questions in an incident.
	AuthConnectorShared   = newEvent("auth.connector_shared")
	AuthConnectorUnshared = newEvent("auth.connector_unshared")
	// AuthWorkerTokenUnknown is an operator-set ATLAS_TOKEN that this server does
	// not accept. The supervisor honours the variable and stops injecting its own,
	// so the workers it starts would hold a credential refused at every poll — a
	// trap that used to be silent because no value could ever have worked
	// (ADR-0194).
	AuthWorkerTokenUnknown = newEvent("auth.worker_token_unknown")
	// CommandFailed is a top-level command exiting non-zero.
	CommandFailed = newEvent("command.failed")
	MCPProxying   = newEvent("mcp.proxying")
	// MCPCatalogueUnknown is the stdio adapter unable to ask its server whether it
	// serves the catalogue (ADR-0434). The
	// adapter then offers every tool and the server refuses what it does not serve,
	// so this is a WARN about a tool list that may be too long, never a failure.
	MCPCatalogueUnknown = newEvent("mcp.catalogue_unknown")
	// WorkerStarting is the out-of-process job worker announcing what it will serve
	// and for which server (ADR-0157).
	WorkerStarting = newEvent("worker.starting")
	// OutboundProxySelected says, once at startup, where a server's or a worker's
	// outbound HTTP takes its proxy from — the proxy variables, the Windows settings,
	// or neither (ADR-0464). Info, not a warning: it is a choice
	// rather than a fault, and the line to read when a Worker's requests time out on
	// one host and not on another.
	OutboundProxySelected = newEvent("outbound_proxy.selected")
	// WorkerPollFailed is a worker reporting that a poll failed and will be retried.
	WorkerPollFailed = newEvent("worker.poll_failed")
	// WorkerSupervisorStarted and WorkerSupervisorFailed report the lifecycle of a
	// worker process Atlas launched itself (ADR-0157 step 7).
	WorkerSupervisorStarted = newEvent("worker.supervised_started")
	WorkerSupervisorFailed  = newEvent("worker.supervise_failed")
	// WorkerBreakerOpen and WorkerBreakerClosed report a circuit breaker holding a
	// failing target's jobs back, and letting them go again (ADR-0340).
	//
	// These are the line an operator alerts on. A breaker turns an outage from a flood
	// of incidents into *silence* — work that simply does not happen — and silence is
	// the one failure mode nothing else surfaces, which is why the record makes
	// visibility part of the decision rather than a follow-up. Open is a warning: an
	// integration has stopped and somebody should know why.
	WorkerBreakerOpen   = newEvent("worker.breaker_open")
	WorkerBreakerClosed = newEvent("worker.breaker_closed")
	// ADMockEnabled is an AD worker announcing that it serves the Active Directory
	// worker against a directory in its own memory rather than a real one
	// (ADR-0181). It is a warning rather than an info because
	// a mock worker is indistinguishable from a working one everywhere else: it
	// completes every job it leases.
	ADMockEnabled = newEvent("ad_mock.enabled")
	// ADMockPerformed is one operation that mock directory simulated. It is what a
	// mockup run leaves behind for the person who ran it, in the worker's log where
	// the Workers console shows it.
	ADMockPerformed = newEvent("ad_mock.performed")
	// ADMockReportFailed is a mock worker that could not deliver its directory to the
	// Atlas whose Console shows it (ADR-0213). A
	// warning, never a job failure: the operation it describes has already happened,
	// so what is lost is one refresh of a view that the next operation sends again.
	ADMockReportFailed = newEvent("ad_mock.report_failed")
	// ADMockSeedUnusable is a mock directory that could not read the seed it was
	// pointed at, and started empty instead. It is a warning and not a refusal
	// because a mock touches nothing real: an empty directory costs a joiner nothing
	// and costs a leaver one visible incident, whereas refusing takes the worker down
	// for every AD task at once — which is what an optional field with a typo in it
	// used to do (ADR-0202).
	ADMockSeedUnusable = newEvent("ad_mock.seed_unusable")
	// SQLMockEnabled is a SQL worker announcing that it answers database tasks from
	// seeded answers in its own memory rather than from a database (ADR-0221).
	// A warning for the same reason the AD one is: a mock worker is indistinguishable
	// from a working one everywhere else, because it completes the jobs it answers.
	SQLMockEnabled = newEvent("sql_mock.enabled")
	// SQLMockSeedUnusable is a mock database that could not read the seed file it was
	// pointed at, and started with no answers instead. A warning and not a refusal for
	// the reason ADMockSeedUnusable is one: the supervisor restarts a child that
	// exits, so failing on an optional file is a restart loop rather than a message.
	// Every statement then fails naming itself, which points at the missing seed.
	SQLMockSeedUnusable = newEvent("sql_mock.seed_unusable")
	// SQLMockReportFailed is a mock SQL worker that could not deliver its journal to
	// the Atlas whose Console shows it. A warning, never a job failure, for the reason
	// ADMockReportFailed is one: the statement it describes has already been answered
	// and the job has already settled, so what is lost is one refresh of a view that
	// the next statement sends again.
	SQLMockReportFailed = newEvent("sql_mock.report_failed")
	// TeamsPhoneMockMode is a Teams Phone worker announcing that it serves its tenants
	// from memory rather than through PowerShell (ADR-0463). A
	// warning for the reason the AD and SQL ones are: a mock worker completes every job
	// it leases and is indistinguishable from a working one everywhere else.
	TeamsPhoneMockMode = newEvent("teamsphone_mock.enabled")
	// WorkerHistoryFailed is the job-history exporter reporting that an append did not
	// reach its clio worker, or that its buffer is dropping entries. Both are
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

// The storage monitor and the engine halt (ADR-0481).
var (
	// StorageLow is the disk crossing into the warning band: less room above the
	// reserve than the warning margin, or a forecast that reaches the reserve within a
	// week. Logged once per crossing, not on every reading.
	StorageLow = newEvent("storage.low")
	// StorageFrozen is the disk falling below the reserve: from here the server refuses
	// new writes until there is room again. An error, because work is being turned away.
	StorageFrozen = newEvent("storage.frozen")
	// StorageResumed is the disk regaining the reserve and its margin, and the server
	// taking writes again.
	StorageResumed = newEvent("storage.resumed")
	// StorageUnreadable is a reading of the file system failing. Nothing changes on it —
	// a frozen server stays frozen, an open one stays open — so it is a warning.
	StorageUnreadable = newEvent("storage.unreadable")
	// EngineHalted is the engine stopping after a batch could not be made durable. It
	// processes nothing more until the server is restarted.
	EngineHalted = newEvent("engine.halted")
)

// Backup, restore, and streaming exports (ADR-0107/0108/0109).
var (
	RestoreApplied                = newEvent("restore.applied")
	BackupStreamFailed            = newEvent("backup.stream_failed")
	FullBackupStreamFailed        = newEvent("full_backup.stream_failed")
	ApplicationSourceStreamFailed = newEvent("application_source.stream_failed")
)

// The inbound event bridge (ADR-0075/0214).
var (
	// InboundWatchMinuteOverflowed reports a jira watch whose page filled the batch
	// limit with issues that all share one minute of its cursor field. JQL compares
	// timestamps to the minute, so no cursor can separate them: the watch steps past
	// the minute and skips what it could not read. Warned because that is a delivery
	// gap — and because standing still instead would silently stop the watch for good.
	InboundWatchMinuteOverflowed = newEvent("inbound_watch.minute_overflowed")
	// InboundWatchGap reports a watch that resumed past what it could no longer read:
	// an IMAP folder the server renumbered, Gmail history older than Gmail keeps
	// (ADR-0438), a wiki restored from a backup whose change feed ends
	// before the watch's cursor (ADR-draft-wikiwikibangbang-task-watch). Warned
	// because it is a delivery gap that otherwise looks exactly like a quiet source.
	InboundWatchGap = newEvent("inbound_watch.gap")
	// InboundWatchFailing reports a watch that cannot read its source — its Worker has
	// no mailbox or no client, is switched off, or the source refused or did not answer
	// the read. Logged once when the failure begins, not on every tick it repeats; the
	// watch's listing carries the newest reason as lastError for as long as it lasts.
	InboundWatchFailing = newEvent("inbound_watch.failing")
	// InboundWatchRecovered reports a failing watch that read its source again.
	InboundWatchRecovered = newEvent("inbound_watch.recovered")
)

// Push delivery of the event feed
// (ADR-0433).
var (
	// FeedSubscriptionChanged: an administrator created, changed or deleted a push
	// subscription — what decides which system beyond Atlas is sent who holds what, so
	// it belongs in the audit trail beside the tokens that read the same feed.
	FeedSubscriptionChanged = newEvent("feed.subscription_changed")
	// FeedPushFailing: a subscription's endpoint refused a batch or did not answer, said
	// once when the failures begin rather than at every retry; the subscription is held
	// on the backoff ladder and its cursor stays put.
	FeedPushFailing = newEvent("feed.push_failing")
	// FeedPushRecovered: a held subscription's endpoint accepted a batch again.
	FeedPushRecovered = newEvent("feed.push_recovered")
	// FeedSubscriptionDisabled: the feed's retention dropped rows a subscription had not
	// delivered, so delivery switched it off rather than go on from the oldest row held
	// and hide the gap.
	FeedSubscriptionDisabled = newEvent("feed.subscription_disabled")
)

// Orders (ADR-0416).
var (
	// OrderInstanceUnrecorded: an instance started to work an order position could not
	// be noted on the order. The instance runs; what is lost is the shop's view of its
	// open tasks, so this is a warning and not a failed start — failing the start would
	// make the caller retry and start the work twice.
	OrderInstanceUnrecorded = newEvent("order.instance_unrecorded")
	// OrderOutcomeConflict: one of the order's own acts — a provision or a return —
	// reached an ending different from the outcome already recorded for the same
	// attempt (ADR-0429 §3). The first ending stands; the second is reported here
	// rather than written, because an action ends once.
	OrderOutcomeConflict = newEvent("order.outcome_conflict")
)

// Wizards (ADR-0449).
var (
	// WizardContinuationFailed: a step of a wizard was completed, and reading where the
	// sitting stands afterwards — or claiming the next step for the person — failed. The
	// completion stands; what is lost is the hand-over to the next screen, which the
	// person then finds in their inbox. A warning and not a failed completion, because
	// failing it would make the caller submit the same answer twice.
	WizardContinuationFailed = newEvent("wizard.continuation_failed")
)

// History retention (ADR-0115/0144) and the OpenSearch exporter (ADR-0114).
var (
	RetentionEnabled   = newEvent("retention.enabled")
	RetentionPurged    = newEvent("retention.purged")
	ExporterEnabled    = newEvent("exporter.enabled")
	ExporterIndexed    = newEvent("exporter.indexed")
	ExporterTickFailed = newEvent("exporter.tick_failed")
)

// Deleting finished instances and compacting the WAL on an administrator's request
// (ADR-0486).
var (
	// HistoryPurged is one batch of finished instances an administrator deleted for
	// good, with who asked, the version and how many went.
	HistoryPurged = newEvent("history.purged")
	// ServerInstancePurgeEnabled is a server on which an administrator may delete
	// finished instances for good: said at every start, like the catalogue purge,
	// because the switch is an opt-out a production installation has to set.
	ServerInstancePurgeEnabled = newEvent("server.instance_purge_enabled")
	// WALCompactionRequested is a compaction pass an administrator asked for on a
	// server not started with --compact-wal.
	WALCompactionRequested = newEvent("wal_compaction.requested")
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

// Resource budgets (ADR-0291). A budget the environment could
// not set is the one configuration mistake that must never be silent: what is left
// standing is a ceiling, and a ceiling nobody knows about is how an installation
// discovers it at the request that ran out of memory.
var LimitIgnored = newEvent("limit.ignored")

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

	// DeploymentDiagramUpdated reports a layout-only adjustment to a deployed
	// definition's diagram (ADR-0251): the picture the
	// Operations views draw changed while the process behind it did not. It is on
	// the audit trail rather than merely in the record's stamp because the change
	// is visible to everyone looking at that definition — running instances and
	// finished ones alike — and "why does this diagram look different from last
	// week" is exactly the question an audit line answers.
	DeploymentDiagramUpdated = newEvent("deployment.diagram_updated")

	// DeploymentForceDeleted reports a deployed version removed with force=true
	// (ADR-0468): its running instances
	// were cancelled and the guards that protect a process still needed by an order or
	// a catalogue release were overridden. Audited because it ends work other people
	// may be waiting on, and "who stopped my instance" must have an answer.
	DeploymentForceDeleted = newEvent("deployment.force_deleted")

	// ApplicationPurged reports an application deleted together with everything it
	// owned on this server: deployed versions and their instances, decision
	// deployments, drafts, forms and models (same record).
	ApplicationPurged = newEvent("application.purged")

	ScriptWorkerEnabled      = newEvent("script_worker.enabled")
	ScriptWorkerMissing      = newEvent("script_worker.binary_missing")
	CallOverrideSkipped      = newEvent("call_override.skipped")
	CollabParticipantsReaped = newEvent("collab.participants_reaped")
	PlaygroundSessionsReaped = newEvent("playground.sessions_reaped")

	// PersonalValueUnreadable says a job could not be handed to a worker because one
	// of the variables it would carry is a personal value whose data subject has been
	// erased (ADR-0314). The record predicts this — "erasing a subject with a live
	// instance leaves that instance unable to provision" — and predicts that it will
	// read as an incident rather than a clear message. This is the line that at least
	// makes it diagnosable: without it the job is simply never handed out, and a worker
	// polls forever with nothing anywhere saying why.
	PersonalValueUnreadable = newEvent("personal_data.value_unreadable")

	// PersonalDataErased records an erasure: one data subject's key destroyed, and with
	// it every copy of their personal data made unreadable (ADR-0314).
	//
	// This line is the *only* remaining evidence that the erasure happened, and that is
	// what makes it load-bearing rather than informative. The key is gone, the ciphertext
	// says nothing, and the subject leaves no other trace — so without it an operator
	// could not demonstrate to an auditor that a deletion request was honoured, on the
	// date it was honoured, by whom. Demonstrability is half of what a data-protection
	// obligation asks for.
	//
	// It names the subject's id on purpose, which is the one place these events carry an
	// identifier. An erasure record that does not say *who* was erased proves nothing,
	// and the id is a reference that this design deliberately keeps readable
	// ([ADR-0314](../docs/adr/0314-portal-personal-data.md)'s primary defence) rather
	// than content it protects.
	PersonalDataErased = newEvent("personal_data.erased")

	// PersonalDataKeyWriteRefused records an attempt to write or delete a data key
	// through the ordinary secrets API. Overwriting one makes a subject's data unreadable
	// without erasing it — the same effect as an erasure, with no intent to do it and no
	// record that it happened — so the attempt is refused and the refusal is audited.
	PersonalDataKeyWriteRefused = newEvent("personal_data.key_write_refused")
)

// Identity-aware Worker execution (ADR-0475). The
// decisions themselves are engine facts, durable in the log; these lines are the
// operational trail of what happened around them — a link, a revocation, a refusal at
// the edge — and name principals by id, never a token or a credential.
var (
	// ConnectionLinked is a person linking an external account and authorizing a
	// delegated connection, or re-authenticating one.
	ConnectionLinked = newEvent("identity.connection_linked")
	// ConnectionLinkFailed is a linking flow that did not end in a connection: which
	// check refused it is in the reason, as a failed login's is.
	ConnectionLinkFailed = newEvent("identity.connection_link_failed")
	// ConnectionRevoked is a connection withdrawn by its owner or an administrator.
	ConnectionRevoked = newEvent("identity.connection_revoked")
	// ConnectionRefreshFailed is the broker failing to obtain an access token; a
	// refusal by the provider marks the connection for re-authentication.
	ConnectionRefreshFailed = newEvent("identity.connection_refresh_failed")
	// ConnectionCredentialRefused is an attempt to reach a connection's credential
	// through an operator surface.
	ConnectionCredentialRefused = newEvent("identity.connection_credential_refused")
	// LinkingSetUp is an administrator setting up, or changing, in the Console the app
	// registration people link accounts through: the tenant, the client id, and
	// whether the client secret was replaced — never the secret.
	LinkingSetUp = newEvent("identity.linking_set_up")
	// LinkingRemoved is an administrator removing the Console's linking setup;
	// linking is off until somebody sets it up again.
	LinkingRemoved = newEvent("identity.linking_removed")
	// LinkingSetupUnreadable is a stored linking setup the server could not read at
	// start. Linking stays off rather than half on, until an administrator sets it up
	// again.
	LinkingSetupUnreadable = newEvent("identity.linking_setup_unreadable")
	// PolicyPublished is an administrator publishing a new version of a policy, or
	// switching one on or off.
	PolicyPublished = newEvent("authz.policy_published")
	// AuthorizationRefused is an enforcement point refusing an external action; the
	// decision itself is in the instance's log.
	AuthorizationRefused = newEvent("authz.action_refused")
	// ApprovalAnswered is an approver answering, or withdrawing an answer to, an
	// approval of an action.
	ApprovalAnswered = newEvent("authz.approval_answered")
)

// The FEEL assistant (ADR-0445).
var (
	// FeelAssistantAnswered is one request of the FEEL assistant that reached a model:
	// which Worker and model answered, how the request ended, and what each round
	// failed on. It is the data the assistant's prompt is improved from — how often a
	// model keeps to the contract, which foreign functions it reaches for, how many
	// rounds a model needs — so it is said for every request, not only for failures.
	//
	// It carries the engine's verdicts and never the conversation or the expressions:
	// what an author writes in a chat may be anything, and an expression may hold a
	// literal they typed. The categories are enough to tell a prompt that teaches the
	// dialect from one that does not.
	FeelAssistantAnswered = newEvent("feel_assistant.answered")
)

// Content from an external source
// (ADR-0491).
var (
	// ContentUnlinked: a product manager removed a product's page binding, or one
	// language page of it, with the reason they had to give (§ 6). It is the audit
	// record of that decision: the product owner has no Atlas account to be told.
	ContentUnlinked = newEvent("content.unlinked")
	// ContentQuarantined: a recheck found a bound page protected, unreadable or
	// gone, the account's standing wrong, or the source unreachable, and the
	// product's source content is withheld from every surface outside the
	// catalogue screen until a refresh passes (§ 9). A WARN, said once when the
	// quarantine begins.
	ContentQuarantined = newEvent("content.quarantined")
	// ContentQuarantineLifted: a refresh passed every rule and ended a quarantine.
	ContentQuarantineLifted = newEvent("content.quarantine_lifted")
	// ContentSourceUnreachable: the recheck could not talk to a source — its
	// change feed failed or it could not be opened. Said once when the failures
	// begin; every bound item of the source is quarantined by the same recheck.
	ContentSourceUnreachable = newEvent("content.source_unreachable")
	// ContentSourceRecovered: a source the recheck could not reach answers again.
	// It lifts no quarantine: only a refresh does.
	ContentSourceRecovered = newEvent("content.source_recovered")
	// ContentWatchFailed: the source refused to watch a bound page or ancestor.
	// The fifteen-minute sweep still rechecks it; what is lost is "at once".
	ContentWatchFailed = newEvent("content.watch_failed")
	// ContentSourceChanged: an administrator created, changed or removed a content
	// source — which wiki the shop's descriptions come from and with which
	// credential reference.
	ContentSourceChanged = newEvent("content.source_changed")
)
