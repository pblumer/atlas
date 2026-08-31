#!/usr/bin/env python3
"""Generate the Atlas ArchiMate model as an Open Group Model Exchange File.

This emits ``atlas.xml`` in the ArchiMate 3.0 *Model Exchange File Format* —
the tool-neutral, standardized XML defined by The Open Group. Any conformant
tool imports it; in Archi it is ``File -> Import -> Other -> Open Exchange File``,
after which Archi saves it as a native ``.archimate`` file. See ADR-0099 and the
sibling ``README.md``.

Usage:
    python3 gen_open_exchange.py        # (re)writes atlas.xml next to this script

The model here is the *same* enterprise-architecture view documented in
``../enterprise-architecture.md`` and drawn by ``../diagrams/gen_diagrams.py`` —
elements, relationships, a folder tree organized by ArchiMate layer, a couple of
property definitions (ADR reference, roadmap status), and generated diagram
views. It is a communication/round-trip artifact, deliberately coarse-grained and
subordinate to the code and the deep-dives, exactly as the doc says.

Editing the model means editing this generator, never hand-patching the XML.
"""
import os
from xml.sax.saxutils import escape, quoteattr

OUT = os.path.dirname(os.path.abspath(__file__))
NS = "http://www.opengroup.org/xsd/archimate/3.0/"
XSI = "http://www.w3.org/2001/XMLSchema-instance"
SCHEMA_LOC = (
    "http://www.opengroup.org/xsd/archimate/3.0/ "
    "http://www.opengroup.org/xsd/archimate/3.0/archimate3_Model.xsd"
)

# Light ArchiMate layer fill colours (r, g, b) used for diagram nodes.
FILL = {
    "motivation": (231, 219, 245),
    "business": (251, 239, 168),
    "application": (203, 228, 250),
    "technology": (203, 238, 215),
    "implementation": (255, 224, 224),
    "external": (221, 227, 234),
}

# ---------------------------------------------------------------------------
# Model definition
#
# Each element: (id, xsi:type, name, layer, documentation, props)
#   props is a dict that may carry {"adr": "ADR-000N", "status": "done"}.
# ---------------------------------------------------------------------------

ELEMENTS = [
    # ---- Motivation --------------------------------------------------------
    ("stk-ea", "Stakeholder", "Enterprise Architect", "motivation",
     "Owns capability fit, governance, and the invariants.", {}),
    ("stk-modeler", "Stakeholder", "Process Modeler", "motivation",
     "Needs models to be authorable and readable, then executable.", {}),
    ("stk-taskuser", "Stakeholder", "Business / Task User", "motivation",
     "Needs human tasks to be reliable and never silently lost.", {}),
    ("stk-ops", "Stakeholder", "Operations", "motivation",
     "Needs to observe, audit, and recover long-running instances.", {}),
    ("stk-integrator", "Stakeholder", "Integration Developer", "motivation",
     "Needs a stable protocol to attach external work (job workers).", {}),
    ("stk-maintainer", "Stakeholder", "Maintainer / AI Agent", "motivation",
     "Needs a codebase whose correctness rules are explicit.", {}),

    ("drv-throughput", "Driver", "Throughput pressure", "motivation",
     "Many instances per second per partition is a first-class goal.", {}),
    ("drv-durability", "Driver", "Durability", "motivation",
     "A workflow engine that drops a token is worse than useless.", {}),
    ("drv-longrunning", "Driver", "Long-running processes", "motivation",
     "Timers, message waits, and multi-week instances must survive restarts.", {}),
    ("drv-robustness", "Driver", "Operational robustness", "motivation",
     "Transient failures must pause, not crash, an instance.", {}),
    ("drv-conformance", "Driver", "Standard conformance", "motivation",
     "Full BPMN 2.0 execution semantics, not a subset dialect.", {}),
    ("drv-productivity", "Driver", "Contributor productivity", "motivation",
     "Humans and AI agents must change the engine safely.", {}),

    ("asm-interpret", "Assessment", "Runtime XML interpretation is a throughput killer", "motivation",
     "Most BPMN engines interpret XML at runtime and write state to SQL one "
     "transaction at a time — both are throughput killers.", {}),
    ("asm-fsync", "Assessment", "One fsync per event caps throughput", "motivation",
     "One fsync per event caps you at a few thousand events per second.", {}),
    ("asm-dualwrite", "Assessment", "In-place state mutation invites consistency bugs", "motivation",
     "Did the DB write and the in-memory change both succeed? An entire class "
     "of consistency bugs that in-place mutation invites.", {}),

    ("goal-durable", "Goal", "Durable execution of long-lived processes", "motivation",
     "Survive crashes and run long-lived processes.", {}),
    ("goal-bpmn", "Goal", "Full BPMN 2.0 coverage", "motivation",
     "Subprocesses, boundary events, event subprocesses.", {}),
    ("goal-throughput", "Goal", "High throughput per partition", "motivation",
     "Many instances per second per partition.", {}),
    ("goal-purego", "Goal", "Pure Go, no CGO, embedded state store", "motivation",
     "", {"adr": "ADR-0010"}),

    ("out-batch", "Outcome", "Throughput scales with batch size", "motivation",
     "Throughput scales with batch size, not with disk round-trips.", {}),
    ("out-replay", "Outcome", "Recovery is a deterministic log replay", "motivation",
     "Single-writer plus event sourcing makes recovery a trivial fold.", {}),

    ("prin-compile", "Principle", "Compile, don't interpret", "motivation",
     "Expensive work happens once at deploy, never on the hot path.", {"adr": "ADR-0004"}),
    ("prin-eventsourcing", "Principle", "Event sourcing over state mutation", "motivation",
     "The log is the single source of truth; state is a fold of it.", {"adr": "ADR-0001"}),
    ("prin-groupcommit", "Principle", "Group commit for durability", "motivation",
     "Make many events durable with a single fsync.", {"adr": "ADR-0005"}),
    ("prin-singlewriter", "Principle", "Single writer per partition", "motivation",
     "One goroutine owns a partition's state; scale by adding partitions.", {"adr": "ADR-0002"}),

    ("req-inv1", "Requirement", "Invariant 1: no allocation on the hot path", "motivation", "", {}),
    ("req-inv2", "Requirement", "Invariant 2: durable before visible", "motivation",
     "Append, one fsync, commit, then side effects.", {}),
    ("req-inv3", "Requirement", "Invariant 3: single writer per partition", "motivation",
     "Cross-partition work is async messaging only.", {}),
    ("req-inv4", "Requirement", "Invariant 4: one applyToState, live and on recovery", "motivation", "", {}),
    ("req-inv5", "Requirement", "Invariant 5: compile, don't interpret", "motivation",
     "No parse/validate/compile on the hot path.", {}),
    ("req-inv6", "Requirement", "Invariant 6: events are facts, commands are intentions", "motivation",
     "Only events persist.", {}),

    ("con-purego", "Constraint", "Pure Go, no CGO", "motivation", "", {"adr": "ADR-0010"}),
    ("con-binary", "Constraint", "Single self-contained binary with embedded web UI", "motivation",
     "", {"adr": "ADR-0011"}),
    ("con-coverage", "Constraint", "95% statement coverage; TDD by default", "motivation",
     "", {"adr": "ADR-0018"}),

    # ---- Business ----------------------------------------------------------
    ("role-ea", "BusinessRole", "Enterprise Architect", "business",
     "Capability governance, invariants.", {}),
    ("role-modeler", "BusinessRole", "Process Modeler", "business",
     "Process design and deployment (Modeler, MCP authoring).", {}),
    ("role-performer", "BusinessRole", "Task Performer", "business",
     "Human-task completion via forms.", {}),
    ("role-ops", "BusinessRole", "Operations", "business",
     "Monitoring, incident resolution, timeline inspection.", {}),
    ("role-integrator", "BusinessRole", "Integration Developer", "business",
     "Authors Worker Types and the workers that run them.", {}),
    ("role-admin", "BusinessRole", "Administrator", "business",
     "Deploys the binary, manages secrets and sharing scopes.", {}),

    ("bsvc-automation", "BusinessService", "Business Process Automation", "business",
     "Durable, end-to-end execution — never drops a token.", {}),
    ("bsvc-design", "BusinessService", "Process Design & Deployment", "business",
     "From a BPMN draft to a runnable, versioned process.", {}),
    ("bsvc-humantask", "BusinessService", "Human-Task Handling", "business",
     "Form-based human work, including public start links.", {}),
    ("bsvc-decision", "BusinessService", "Business Decision Making (DMN)", "business",
     "Central, versioned, explainable business rules.", {}),
    ("bsvc-monitoring", "BusinessService", "Process Monitoring & Audit", "business",
     "A complete, replayable history of every instance.", {}),

    ("bproc-lifecycle", "BusinessProcess", "Process lifecycle", "business",
     "Model, deploy, run, monitor, improve.", {}),
    ("bproc-domain", "BusinessProcess", "Domain process (e.g. order-to-cash)", "business",
     "A concrete deployed BPMN model, executed as instances.", {}),

    ("bevt-message", "BusinessEvent", "Message / signal received", "business", "", {}),
    ("bevt-timer", "BusinessEvent", "Deadline / timer reached", "business", "", {}),
    ("bevt-incident", "BusinessEvent", "Incident raised", "business", "", {}),

    ("bobj-bpmn", "BusinessObject", "Process Model (BPMN)", "business", "", {}),
    ("bobj-dmn", "BusinessObject", "Decision Model (DMN)", "business", "", {}),
    ("bobj-form", "BusinessObject", "Form", "business", "", {}),
    ("bobj-instance", "BusinessObject", "Process Instance", "business", "", {}),
    ("bobj-usertask", "BusinessObject", "User Task", "business", "", {}),
    ("bobj-variables", "BusinessObject", "Process Variables", "business", "", {}),
    ("bobj-incident", "BusinessObject", "Incident", "business", "", {}),
    ("contract-durable", "Contract", "Durable Execution (durable before visible)", "business",
     "The SLA the platform honours: durable before visible.", {}),

    # ---- Application -------------------------------------------------------
    ("app-engine", "ApplicationComponent", "Atlas Engine (core)", "application",
     "A single embeddable library composed of the sub-components below.", {}),
    ("app-compiler", "ApplicationComponent", "Graph Compiler", "application",
     "BPMN XML to an immutable, integer-indexed CompiledProcess.", {"adr": "ADR-0004"}),
    ("app-processor", "ApplicationComponent", "Processor (single-writer)", "application",
     "Batch cycle: fold commands into durable events.", {"adr": "ADR-0005"}),
    ("app-datamodel", "ApplicationComponent", "Data model / applyToState", "application",
     "(ValueType, Intent) records; identical live and on recovery.", {"adr": "ADR-0001"}),
    ("app-wal", "ApplicationComponent", "WAL manager", "application",
     "Segmented append log, one fsync per batch, forward replay.", {"adr": "ADR-0005"}),
    ("app-statestore", "ApplicationComponent", "State-store wrapper", "application",
     "Column families / indexes over Pebble; transactions.", {"adr": "ADR-0003"}),
    ("app-timer", "ApplicationComponent", "Timer service", "application",
     "Due-date index scan, FEEL schedules, triggering.", {"adr": "ADR-0055"}),
    ("app-jobstore", "ApplicationComponent", "Job store", "application",
     "Activatable jobs per type; worker subscription.", {"adr": "ADR-0007"}),
    ("app-feel", "ApplicationComponent", "FEEL expression engine", "application",
     "Compile-once / eval-many, behind an expr boundary.", {"adr": "ADR-0015"}),
    ("app-incidentmgmt", "ApplicationComponent", "Incident management", "application",
     "Failure to paused state to operator resolution.", {}),
    ("app-vault", "ApplicationComponent", "Secret vault", "application",
     "Engine-internal encrypted secret store.", {"adr": "ADR-0069"}),

    ("chan-webui", "ApplicationComponent", "Web UI", "application",
     "Embedded bpmn-js: Modeler, DMN editor, Operations view, public forms.",
     {"adr": "ADR-0013"}),
    ("chan-rest", "ApplicationComponent", "REST / HTTP API", "application",
     "Client command submission and queries; OpenAPI + embedded explorer.",
     {"adr": "ADR-0043"}),
    ("chan-mcp", "ApplicationComponent", "MCP Server", "application",
     "Authoring & operations over MCP, as a stdio adapter over the HTTP API.",
     {"adr": "ADR-0016"}),

    ("conn-rest", "ApplicationComponent", "REST worker type", "application",
     "Service task calls an external HTTP API.", {}),
    ("conn-mail", "ApplicationComponent", "Mail worker type", "application",
     "Email via Gmail / Microsoft Graph (OAuth).", {}),
    ("conn-script", "ApplicationComponent", "Script worker type", "application",
     "Polyglot scripts (e.g. PowerShell) run outside the engine.", {"adr": "ADR-0047"}),
    ("conn-dmn", "ApplicationComponent", "DMN / temis worker type", "application",
     "Business-rule tasks against the temis engine.", {"adr": "ADR-0050"}),
    ("conn-clio", "ApplicationComponent", "clio event bridge", "application",
     "At-least-once ingestion of external events, idempotent delivery.", {"adr": "ADR-0075"}),
    ("conn-csv", "ApplicationComponent", "CSV import worker", "application",
     "Bulk data import as a job.", {}),

    ("asvc-deploy", "ApplicationService", "Deploy / Compile", "application", "", {}),
    ("asvc-execution", "ApplicationService", "Instance Execution", "application", "", {}),
    ("asvc-jobdist", "ApplicationService", "Job Distribution (gRPC)", "application", "", {}),
    ("asvc-correlation", "ApplicationService", "Message Correlation", "application", "", {"adr": "ADR-0020"}),
    ("asvc-timer", "ApplicationService", "Timer Scheduling", "application", "", {}),
    ("asvc-decision", "ApplicationService", "Decision Evaluation", "application", "", {}),
    ("asvc-humantask", "ApplicationService", "Human-Task", "application", "", {}),
    ("asvc-timeline", "ApplicationService", "Query / Timeline replay", "application", "", {"adr": "ADR-0065"}),

    ("dobj-eventlog", "DataObject", "Event log (WAL)", "application",
     "The single source of truth.", {}),
    ("dobj-compiled", "DataObject", "CompiledProcess", "application",
     "Immutable, lock-free readable.", {}),
    ("dobj-statestore", "DataObject", "State store (column families)", "application",
     "The materialization.", {}),
    ("dobj-varstore", "DataObject", "Variable store", "application",
     "Referenced by scope key, never copied into records.", {}),

    ("aif-rest", "ApplicationInterface", "HTTP / REST", "application", "", {}),
    ("aif-grpc", "ApplicationInterface", "gRPC job stream", "application", "", {}),
    ("aif-mcp", "ApplicationInterface", "MCP", "application", "", {}),
    ("aif-web", "ApplicationInterface", "Web (browser)", "application", "", {}),

    # ---- Technology --------------------------------------------------------
    ("tec-binary", "Node", "Atlas single binary", "technology",
     "Self-contained Go executable, no CGO.", {"adr": "ADR-0010"}),
    ("tec-goruntime", "SystemSoftware", "Go runtime", "technology",
     "Goroutines, scheduler, GC.", {}),
    ("tec-partition", "Node", "Partition", "technology",
     "Own queue, processor, WAL, state; scales via instanceKey % N.", {"adr": "ADR-0002"}),
    ("tec-pebble", "SystemSoftware", "Pebble", "technology",
     "Embedded pure-Go LSM-tree KV store.", {"adr": "ADR-0003"}),
    ("tec-filesystem", "SystemSoftware", "Filesystem", "technology",
     "Carries WAL segments & SST files; fsync durability.", {}),

    ("tsvc-append", "TechnologyService", "Durable append + group commit", "technology",
     "One fsync per batch; throughput scales with batch size.", {}),
    ("tsvc-kv", "TechnologyService", "Key-value storage", "technology",
     "Indexed state materialization.", {}),
    ("tsvc-replay", "TechnologyService", "Log replay / recovery", "technology",
     "State as a deterministic fold over the log.", {}),
    ("tsvc-routing", "TechnologyService", "Partition routing", "technology",
     "A bit-shift, not a lookup; partition is baked into the key.", {"adr": "ADR-0006"}),

    ("path-grpc", "Path", "gRPC streaming", "technology", "Job workers.", {"adr": "ADR-0007"}),
    ("path-https", "Path", "HTTPS", "technology", "REST & Web UI.", {}),
    ("path-mcp", "Path", "MCP transport", "technology", "", {}),

    ("art-binary", "Artifact", "atlas binary", "technology",
     "Embeds the web UI assets.", {}),
    ("art-wal", "Artifact", "WAL segments", "technology",
     "Append-only; one fsync per batch.", {}),
    ("art-sst", "Artifact", "Pebble SST files", "technology",
     "Materialized state.", {}),
    ("art-models", "Artifact", "BPMN / DMN / form files", "technology", "", {}),

    ("ext-temis", "Node", "temis", "external",
     "Deterministic DMN/FEEL decision engine.", {"adr": "ADR-0050"}),
    ("ext-clio", "Node", "clio", "external",
     "Event store / streaming source.", {"adr": "ADR-0075"}),
    ("ext-mail", "Node", "Gmail / Microsoft Graph", "external", "Email providers.", {}),
    ("ext-workers", "Node", "External job workers", "external",
     "Polyglot processes such as PowerShell, gRPC-connected.", {}),

    # ---- Implementation & Migration ---------------------------------------
    ("plat-m0", "Plateau", "M0 Foundations", "implementation",
     "The three pillars end to end, with crash recovery.", {"status": "done"}),
    ("plat-m1", "Plateau", "M1 Core BPMN", "implementation",
     "Gateways, process variables, FEEL, I/O mappings.", {"status": "in progress"}),
    ("plat-m2", "Plateau", "M2 Events & timers", "implementation",
     "Timer / message / signal / error and boundary events.", {"status": "in progress"}),
    ("plat-m3", "Plateau", "M3 Structure", "implementation",
     "Subprocesses, event subprocesses, call activities.", {"status": "planned"}),
    ("plat-m4", "Plateau", "M4 Operability", "implementation",
     "Incidents, metrics, operational tooling.", {"status": "planned"}),
    ("plat-m5", "Plateau", "M5 Scale-out", "implementation",
     "Cross-partition messaging and horizontal scale.", {"status": "planned"}),
    ("plat-m6", "Plateau", "M6 Ecosystem", "implementation",
     "A broader Worker Type / integration surface.", {"status": "planned"}),
    ("wp-server", "WorkPackage", "Milestone S: single-binary server & web UI", "implementation",
     "Parallel workstream to the engine timeline.", {}),
    ("wp-authoring", "WorkPackage", "Milestone A: Modeler & authoring experience", "implementation",
     "Parallel workstream to the engine timeline.", {}),
]

ETYPE = {e[0]: e[1] for e in ELEMENTS}
ELAYER = {e[0]: e[3] for e in ELEMENTS}

# ---------------------------------------------------------------------------
# Relationships: (id, xsi:type, source, target, name, extra)
#   extra may carry {"accessType": "Write"|"Read"|"ReadWrite"|"Access"}.
# ---------------------------------------------------------------------------

RELATIONSHIPS = [
    # Motivation: stakeholders hold drivers
    ("r-stkops-dur", "Association", "stk-ops", "drv-durability", "", {}),
    ("r-stktask-dur", "Association", "stk-taskuser", "drv-durability", "", {}),
    ("r-stkmod-conf", "Association", "stk-modeler", "drv-conformance", "", {}),
    ("r-stkint-rob", "Association", "stk-integrator", "drv-robustness", "", {}),
    ("r-stkea-conf", "Association", "stk-ea", "drv-conformance", "", {}),
    ("r-stkmnt-prod", "Association", "stk-maintainer", "drv-productivity", "", {}),
    # Assessments influence drivers
    ("r-asmint-thr", "Influence", "asm-interpret", "drv-throughput", "", {}),
    ("r-asmfsync-thr", "Influence", "asm-fsync", "drv-throughput", "", {}),
    ("r-asmdw-dur", "Influence", "asm-dualwrite", "drv-durability", "", {}),
    # Drivers influence goals
    ("r-drvdur-gdur", "Influence", "drv-durability", "goal-durable", "", {}),
    ("r-drvlong-gdur", "Influence", "drv-longrunning", "goal-durable", "", {}),
    ("r-drvthr-gthr", "Influence", "drv-throughput", "goal-throughput", "", {}),
    ("r-drvconf-gbpmn", "Influence", "drv-conformance", "goal-bpmn", "", {}),
    ("r-drvprod-gpure", "Influence", "drv-productivity", "goal-purego", "", {}),
    # Durability driver influences the event-sourcing principle
    ("r-drvdur-prines", "Influence", "drv-durability", "prin-eventsourcing", "", {}),
    # Principles realize goals
    ("r-prines-gdur", "Realization", "prin-eventsourcing", "goal-durable", "", {}),
    ("r-pringc-gthr", "Realization", "prin-groupcommit", "goal-throughput", "", {}),
    ("r-princomp-gthr", "Realization", "prin-compile", "goal-throughput", "", {}),
    ("r-prinsw-gthr", "Realization", "prin-singlewriter", "goal-throughput", "", {}),
    # Outcomes realize goals
    ("r-outbatch-gthr", "Realization", "out-batch", "goal-throughput", "", {}),
    ("r-outreplay-gdur", "Realization", "out-replay", "goal-durable", "", {}),
    # Requirements (invariants) realize principles
    ("r-inv1-gc", "Realization", "req-inv1", "prin-groupcommit", "", {}),
    ("r-inv2-es", "Realization", "req-inv2", "prin-eventsourcing", "", {}),
    ("r-inv2-gc", "Realization", "req-inv2", "prin-groupcommit", "", {}),
    ("r-inv3-sw", "Realization", "req-inv3", "prin-singlewriter", "", {}),
    ("r-inv4-es", "Realization", "req-inv4", "prin-eventsourcing", "", {}),
    ("r-inv5-comp", "Realization", "req-inv5", "prin-compile", "", {}),
    ("r-inv6-es", "Realization", "req-inv6", "prin-eventsourcing", "", {}),
    # Architecture realizes principles and invariants (motivation trace)
    ("r-compiler-comp", "Realization", "app-compiler", "prin-compile", "", {}),
    ("r-wal-es", "Realization", "app-wal", "prin-eventsourcing", "", {}),
    ("r-datamodel-es", "Realization", "app-datamodel", "prin-eventsourcing", "", {}),
    ("r-proc-gc", "Realization", "app-processor", "prin-groupcommit", "", {}),
    ("r-part-sw", "Realization", "tec-partition", "prin-singlewriter", "", {}),
    ("r-proc-inv2", "Realization", "app-processor", "req-inv2", "", {}),
    ("r-proc-inv4", "Realization", "app-datamodel", "req-inv4", "", {}),
    ("r-part-inv3", "Realization", "tec-partition", "req-inv3", "", {}),
    ("r-compiler-inv5", "Realization", "app-compiler", "req-inv5", "", {}),

    # Business: roles assigned to behaviour
    ("r-rmod-bdes", "Assignment", "role-modeler", "bsvc-design", "", {}),
    ("r-rmod-life", "Assignment", "role-modeler", "bproc-lifecycle", "", {}),
    ("r-rperf-bht", "Assignment", "role-performer", "bsvc-humantask", "", {}),
    ("r-rops-bmon", "Assignment", "role-ops", "bsvc-monitoring", "", {}),
    ("r-rint-bauto", "Association", "role-integrator", "bsvc-automation", "", {}),
    ("r-radm-bauto", "Association", "role-admin", "bsvc-automation", "", {}),
    # Business events trigger the domain process
    ("r-evmsg-dom", "Triggering", "bevt-message", "bproc-domain", "", {}),
    ("r-evtim-dom", "Triggering", "bevt-timer", "bproc-domain", "", {}),
    ("r-evinc-dom", "Triggering", "bevt-incident", "bproc-domain", "", {}),
    # Business process access to objects
    ("r-dom-vars", "Access", "bproc-domain", "bobj-variables", "", {"accessType": "Write"}),
    ("r-dom-form", "Access", "bproc-domain", "bobj-form", "", {"accessType": "Read"}),
    ("r-dom-inst", "Access", "bproc-domain", "bobj-instance", "", {"accessType": "Write"}),
    ("r-life-bpmn", "Access", "bproc-lifecycle", "bobj-bpmn", "", {"accessType": "ReadWrite"}),
    ("r-dom-contract", "Association", "bsvc-automation", "contract-durable", "", {}),

    # Application service realizes business service (cross-layer)
    ("r-aexec-bauto", "Realization", "asvc-execution", "bsvc-automation", "", {}),
    ("r-adeploy-bdes", "Realization", "asvc-deploy", "bsvc-design", "", {}),
    ("r-aht-bht", "Realization", "asvc-humantask", "bsvc-humantask", "", {}),
    ("r-adec-bdec", "Realization", "asvc-decision", "bsvc-decision", "", {}),
    ("r-atl-bmon", "Realization", "asvc-timeline", "bsvc-monitoring", "", {}),
    ("r-aexec-dom", "Serving", "asvc-execution", "bproc-domain", "", {}),

    # Application: engine composition
    ("r-eng-comp", "Composition", "app-engine", "app-compiler", "", {}),
    ("r-eng-proc", "Composition", "app-engine", "app-processor", "", {}),
    ("r-eng-dm", "Composition", "app-engine", "app-datamodel", "", {}),
    ("r-eng-wal", "Composition", "app-engine", "app-wal", "", {}),
    ("r-eng-ss", "Composition", "app-engine", "app-statestore", "", {}),
    ("r-eng-timer", "Composition", "app-engine", "app-timer", "", {}),
    ("r-eng-job", "Composition", "app-engine", "app-jobstore", "", {}),
    ("r-eng-feel", "Composition", "app-engine", "app-feel", "", {}),
    ("r-eng-inc", "Composition", "app-engine", "app-incidentmgmt", "", {}),
    ("r-eng-vault", "Composition", "app-engine", "app-vault", "", {}),
    # Engine serves the channels
    ("r-eng-web", "Serving", "app-engine", "chan-webui", "", {}),
    ("r-eng-rest", "Serving", "app-engine", "chan-rest", "", {}),
    ("r-eng-mcp", "Serving", "app-engine", "chan-mcp", "", {}),
    # Components assigned to their services
    ("r-comp-adeploy", "Assignment", "app-compiler", "asvc-deploy", "", {}),
    ("r-proc-aexec", "Assignment", "app-processor", "asvc-execution", "", {}),
    ("r-job-ajob", "Assignment", "app-jobstore", "asvc-jobdist", "", {}),
    ("r-proc-acorr", "Assignment", "app-processor", "asvc-correlation", "", {}),
    ("r-timer-atimer", "Assignment", "app-timer", "asvc-timer", "", {}),
    ("r-feel-adec", "Assignment", "app-feel", "asvc-decision", "", {}),
    ("r-proc-aht", "Assignment", "app-processor", "asvc-humantask", "", {}),
    ("r-proc-atl", "Assignment", "app-processor", "asvc-timeline", "", {}),
    # Job distribution serves the worker types; they serve the domain process
    ("r-ajob-crest", "Serving", "asvc-jobdist", "conn-rest", "", {}),
    ("r-ajob-cmail", "Serving", "asvc-jobdist", "conn-mail", "", {}),
    ("r-ajob-cscript", "Serving", "asvc-jobdist", "conn-script", "", {}),
    ("r-ajob-cdmn", "Serving", "asvc-jobdist", "conn-dmn", "", {}),
    ("r-ajob-cclio", "Serving", "asvc-jobdist", "conn-clio", "", {}),
    ("r-ajob-ccsv", "Serving", "asvc-jobdist", "conn-csv", "", {}),
    ("r-crest-dom", "Serving", "conn-rest", "bproc-domain", "", {}),
    ("r-cmail-dom", "Serving", "conn-mail", "bproc-domain", "", {}),
    ("r-cscript-dom", "Serving", "conn-script", "bproc-domain", "", {}),
    ("r-cdmn-dom", "Serving", "conn-dmn", "bproc-domain", "", {}),
    # Processor access to data objects (durable before visible)
    ("r-proc-elog", "Access", "app-processor", "dobj-eventlog", "", {"accessType": "Write"}),
    ("r-proc-sstore", "Access", "app-processor", "dobj-statestore", "", {"accessType": "ReadWrite"}),
    ("r-proc-vars", "Access", "app-processor", "dobj-varstore", "", {"accessType": "ReadWrite"}),
    ("r-comp-compiled", "Access", "app-compiler", "dobj-compiled", "", {"accessType": "Write"}),
    ("r-proc-compiled", "Access", "app-processor", "dobj-compiled", "", {"accessType": "Read"}),
    # Component composition of interfaces
    ("r-web-ifweb", "Composition", "chan-webui", "aif-web", "", {}),
    ("r-rest-ifrest", "Composition", "chan-rest", "aif-rest", "", {}),
    ("r-mcp-ifmcp", "Composition", "chan-mcp", "aif-mcp", "", {}),
    ("r-job-ifgrpc", "Composition", "app-jobstore", "aif-grpc", "", {}),

    # Technology: binary hosts partitions and runtime
    ("r-bin-part", "Composition", "tec-binary", "tec-partition", "", {}),
    ("r-go-bin", "Serving", "tec-goruntime", "tec-binary", "", {}),
    # Deployment assignment of artifacts to nodes
    ("r-bin-artbin", "Assignment", "tec-binary", "art-binary", "", {}),
    ("r-part-artwal", "Assignment", "tec-partition", "art-wal", "", {}),
    ("r-part-artsst", "Assignment", "tec-partition", "art-sst", "", {}),
    # System software serves application components (cross-layer)
    ("r-pebble-ss", "Serving", "tec-pebble", "app-statestore", "", {}),
    ("r-fs-wal", "Serving", "tec-filesystem", "app-wal", "", {}),
    ("r-fs-pebble", "Serving", "tec-filesystem", "tec-pebble", "", {}),
    ("r-part-proc", "Serving", "tec-partition", "app-processor", "", {}),
    # Nodes/software assigned to technology services
    ("r-part-tappend", "Assignment", "tec-partition", "tsvc-append", "", {}),
    ("r-pebble-tkv", "Assignment", "tec-pebble", "tsvc-kv", "", {}),
    ("r-part-treplay", "Assignment", "tec-partition", "tsvc-replay", "", {}),
    ("r-bin-trouting", "Assignment", "tec-binary", "tsvc-routing", "", {}),
    # Technology service realizes the durable-execution contract
    ("r-tappend-contract", "Realization", "tsvc-append", "contract-durable", "", {}),
    ("r-tkv-sstore", "Serving", "tsvc-kv", "app-statestore", "", {}),
    # Artifacts realize what they carry
    ("r-artbin-eng", "Realization", "art-binary", "app-engine", "", {}),
    ("r-artwal-elog", "Realization", "art-wal", "dobj-eventlog", "", {}),
    ("r-artsst-sstore", "Realization", "art-sst", "dobj-statestore", "", {}),
    ("r-artmodels-bpmn", "Realization", "art-models", "bobj-bpmn", "", {}),
    # External systems serve their worker types
    ("r-exttemis-cdmn", "Serving", "ext-temis", "conn-dmn", "", {}),
    ("r-extclio-cclio", "Serving", "ext-clio", "conn-clio", "", {}),
    ("r-extmail-cmail", "Serving", "ext-mail", "conn-mail", "", {}),
    ("r-extwork-cscript", "Serving", "ext-workers", "conn-script", "", {}),
    # Communication paths
    ("r-pgrpc-work", "Association", "path-grpc", "ext-workers", "", {}),
    ("r-pgrpc-job", "Association", "path-grpc", "aif-grpc", "", {}),
    ("r-phttps-mail", "Association", "path-https", "ext-mail", "", {}),
    ("r-phttps-temis", "Association", "path-https", "ext-temis", "", {}),
    ("r-pmcp-mcp", "Association", "path-mcp", "aif-mcp", "", {}),

    # Implementation roadmap: plateau chain and parallel workstreams
    ("r-m0-m1", "Triggering", "plat-m0", "plat-m1", "", {}),
    ("r-m1-m2", "Triggering", "plat-m1", "plat-m2", "", {}),
    ("r-m2-m3", "Triggering", "plat-m2", "plat-m3", "", {}),
    ("r-m3-m4", "Triggering", "plat-m3", "plat-m4", "", {}),
    ("r-m4-m5", "Triggering", "plat-m4", "plat-m5", "", {}),
    ("r-m5-m6", "Triggering", "plat-m5", "plat-m6", "", {}),
    ("r-wps-m1", "Association", "wp-server", "plat-m1", "", {}),
    ("r-wpa-m1", "Association", "wp-authoring", "plat-m1", "", {}),
]

# ---------------------------------------------------------------------------
# Property definitions
# ---------------------------------------------------------------------------

PROP_DEFS = [
    ("propid-adr", "ADR", "string"),
    ("propid-status", "Roadmap status", "string"),
]

# ---------------------------------------------------------------------------
# Organizations (folder tree), by ArchiMate layer
# ---------------------------------------------------------------------------

def ids_in(layer):
    return [e[0] for e in ELEMENTS if e[3] == layer]

ORG_FOLDERS = [
    ("Motivation", ids_in("motivation")),
    ("Business", ids_in("business")),
    ("Application", ids_in("application")),
    ("Technology", ids_in("technology")),
    ("External Systems", ids_in("external")),
    ("Implementation & Migration", ids_in("implementation")),
]

# ---------------------------------------------------------------------------
# Views (diagrams). Each view is a list of bands: (title, [element ids]).
# The layout engine places boxes left-to-right per band, bands top-to-bottom,
# and draws a connection for every relationship whose endpoints are both in
# the view.
# ---------------------------------------------------------------------------

VIEWS = [
    ("view-overview", "Atlas — ArchiMate 3.2 layered view", [
        ("Motivation", ["drv-durability", "prin-eventsourcing", "req-inv2"]),
        ("Business", ["bsvc-automation", "bproc-domain", "bobj-instance"]),
        ("Application", ["app-engine", "chan-rest", "conn-dmn"]),
        ("Technology", ["tec-binary", "tec-partition", "tec-pebble"]),
    ]),
    ("view-motivation", "Motivation trace — one concern, all the way down", [
        ("Stakeholder", ["stk-ops"]),
        ("Driver", ["drv-durability"]),
        ("Goal", ["goal-durable"]),
        ("Principle", ["prin-eventsourcing"]),
        ("Requirement", ["req-inv2"]),
        ("Architecture", ["app-processor", "app-wal"]),
    ]),
    ("view-business", "Business layer", [
        ("Roles — active structure", ["role-modeler", "role-performer", "role-ops"]),
        ("Business services — behaviour",
         ["bsvc-automation", "bsvc-humantask", "bsvc-decision", "bsvc-monitoring"]),
        ("Business objects — passive structure",
         ["bobj-bpmn", "bobj-instance", "bobj-usertask", "bobj-incident"]),
    ]),
    ("view-application", "Application layer", [
        ("Channels", ["chan-webui", "chan-rest", "chan-mcp"]),
        ("Atlas Engine — core",
         ["app-compiler", "app-processor", "app-datamodel", "app-wal", "app-statestore"]),
        ("Worker types — job execution",
         ["conn-rest", "conn-mail", "conn-script", "conn-dmn", "conn-clio"]),
    ]),
    ("view-technology", "Technology & deployment layer", [
        ("Atlas single binary", ["tec-binary", "tec-goruntime", "art-binary"]),
        ("Partitions — single-writer", ["tec-partition", "art-wal", "art-sst"]),
        ("Durable storage", ["tec-pebble", "tec-filesystem"]),
        ("External systems", ["ext-temis", "ext-clio", "ext-mail", "ext-workers"]),
    ]),
    ("view-implementation", "Implementation roadmap — plateaus M0 to M6", [
        ("Plateaus",
         ["plat-m0", "plat-m1", "plat-m2", "plat-m3", "plat-m4", "plat-m5", "plat-m6"]),
        ("Parallel workstreams", ["wp-server", "wp-authoring"]),
    ]),
]

# ---------------------------------------------------------------------------
# Emit
# ---------------------------------------------------------------------------

def txt(tag, s, indent):
    return f'{indent}<{tag} xml:lang="en">{escape(s)}</{tag}>\n'

def emit_elements(out):
    out.append("  <elements>\n")
    for eid, etype, name, layer, doc, props in ELEMENTS:
        out.append(f'    <element identifier="{eid}" xsi:type="{etype}">\n')
        out.append(txt("name", name, "      "))
        if doc:
            out.append(txt("documentation", doc, "      "))
        emit_props(out, props, "      ")
        out.append("    </element>\n")
    out.append("  </elements>\n")

def emit_relationships(out):
    out.append("  <relationships>\n")
    for rid, rtype, src, tgt, name, extra in RELATIONSHIPS:
        attrs = f'identifier="{rid}" source="{src}" target="{tgt}" xsi:type="{rtype}"'
        if rtype == "Access" and extra.get("accessType"):
            attrs += f' accessType="{extra["accessType"]}"'
        if name:
            out.append(f"    <relationship {attrs}>\n")
            out.append(txt("name", name, "      "))
            out.append("    </relationship>\n")
        else:
            out.append(f"    <relationship {attrs}/>\n")
    out.append("  </relationships>\n")

def emit_props(out, props, indent):
    if not props:
        return
    out.append(f"{indent}<properties>\n")
    if "adr" in props:
        out.append(f'{indent}  <property propertyDefinitionRef="propid-adr">\n')
        out.append(txt("value", props["adr"], indent + "    "))
        out.append(f"{indent}  </property>\n")
    if "status" in props:
        out.append(f'{indent}  <property propertyDefinitionRef="propid-status">\n')
        out.append(txt("value", props["status"], indent + "    "))
        out.append(f"{indent}  </property>\n")
    out.append(f"{indent}</properties>\n")

def emit_organizations(out):
    out.append("  <organizations>\n")
    for label, ids in ORG_FOLDERS:
        out.append("    <item>\n")
        out.append(txt("label", label, "      "))
        for i in ids:
            out.append(f'      <item identifierRef="{i}"/>\n')
        out.append("    </item>\n")
    # Views folder
    out.append("    <item>\n")
    out.append(txt("label", "Views", "      "))
    for vid, _, _ in VIEWS:
        out.append(f'      <item identifierRef="{vid}"/>\n')
    out.append("    </item>\n")
    out.append("  </organizations>\n")

def emit_property_definitions(out):
    out.append("  <propertyDefinitions>\n")
    for pid, name, ptype in PROP_DEFS:
        # propertyDefinition/name is a plain xs:string in the schema (no xml:lang),
        # unlike the LangString name on elements, views, and organization labels.
        out.append(f'    <propertyDefinition identifier="{pid}" type="{ptype}">\n')
        out.append(f"      <name>{escape(name)}</name>\n")
        out.append("    </propertyDefinition>\n")
    out.append("  </propertyDefinitions>\n")

# --- view layout ---
BOX_W, BOX_H = 170, 66
HGAP, VGAP = 26, 58
MARGIN = 24
LABEL_H = 22

def emit_views(out):
    out.append("  <views>\n")
    out.append("    <diagrams>\n")
    for vid, title, bands in VIEWS:
        nodes = []          # (node_id, elem_id, x, y)
        node_of = {}        # elem_id -> node_id (first placement in this view)
        y = MARGIN + 30
        for bi, (btitle, ids) in enumerate(bands):
            x = MARGIN
            for elem_id in ids:
                nid = f"node-{vid}-{elem_id}"
                nodes.append((nid, elem_id, x, y))
                node_of.setdefault(elem_id, nid)
                x += BOX_W + HGAP
            y += BOX_H + VGAP
        # connections whose endpoints are both present
        conns = []
        for rid, rtype, src, tgt, name, extra in RELATIONSHIPS:
            if src in node_of and tgt in node_of:
                conns.append((f"conn-{vid}-{rid}", rid, node_of[src], node_of[tgt]))
        out.append(f'      <view identifier="{vid}" xsi:type="Diagram">\n')
        out.append(txt("name", title, "        "))
        for nid, elem_id, x, yy in nodes:
            r, g, b = FILL[ELAYER[elem_id]]
            out.append(
                f'        <node identifier="{nid}" elementRef="{elem_id}" '
                f'xsi:type="Element" x="{x}" y="{yy}" w="{BOX_W}" h="{BOX_H}">\n'
                f'          <style>\n'
                f'            <fillColor r="{r}" g="{g}" b="{b}" a="100"/>\n'
                f'          </style>\n'
                f'        </node>\n'
            )
        for cid, rid, s, t in conns:
            out.append(
                f'        <connection identifier="{cid}" relationshipRef="{rid}" '
                f'xsi:type="Relationship" source="{s}" target="{t}"/>\n'
            )
        out.append("      </view>\n")
    out.append("    </diagrams>\n")
    out.append("  </views>\n")

def build():
    out = []
    out.append('<?xml version="1.0" encoding="UTF-8"?>\n')
    out.append(
        f'<model xmlns="{NS}" xmlns:xsi="{XSI}" '
        f'xsi:schemaLocation={quoteattr(SCHEMA_LOC)} '
        f'identifier="id-atlas-model">\n'
    )
    out.append(txt("name", "Atlas Enterprise Architecture", "  "))
    out.append(txt(
        "documentation",
        "An ArchiMate 3.2 layered view of the Atlas BPMN workflow engine: "
        "motivation, business, application, technology, and implementation & "
        "deployment. Generated from gen_open_exchange.py; a communication and "
        "round-trip artifact, subordinate to the code and the deep-dives "
        "(see ADR-0099 and docs/architecture/enterprise-architecture.md).",
        "  ",
    ))
    emit_elements(out)
    emit_relationships(out)
    emit_organizations(out)
    emit_property_definitions(out)
    emit_views(out)
    out.append("</model>\n")
    return "".join(out)

if __name__ == "__main__":
    xml = build()
    path = os.path.join(OUT, "atlas.xml")
    with open(path, "w", encoding="utf-8") as f:
        f.write(xml)
    print("wrote", path, f"({len(xml)} bytes)")
    print("elements:", len(ELEMENTS), "relationships:", len(RELATIONSHIPS),
          "views:", len(VIEWS))
