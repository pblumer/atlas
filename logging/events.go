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
