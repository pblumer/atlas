# Pega and Atlas

This document compares the [Pega Platform](https://www.pega.com/products/platform)
(Pega Infinity) with Atlas, **on capabilities only**. It deliberately contains no
commercial, licensing, pricing, or contractual comparison, and no vendor-selection
recommendation beyond what follows from the capabilities themselves.

- **Pega Platform** is a full-stack, model-driven application platform whose centre
  is *case management*, with an application-generation surface (UI, data model,
  reporting, security) and a decisioning stack around it.
- **Atlas** is a durable BPMN 2.x workflow **engine** plus the surfaces needed to
  author, run, watch, and work its processes.

The two overlap in one region — routing work through a modelled process, giving
people tasks, and calling systems — and differ almost everywhere else, in both
directions.

> [!IMPORTANT]
> **What the two columns mean.** For Atlas, *implemented* means present in this
> repository and covered by its tests; the [roadmap](../../ROADMAP.md) marks what
> is not. Atlas is a `0.x` developer preview and is not production-ready, which
> qualifies every Atlas capability statement below: a capability being present is
> not a claim that it is hardened, stable across releases, or operable at scale.
> For Pega, the statements are drawn from public product documentation and
> announcements; Pega is an established product whose exact surface varies by
> version, edition, and deployment model, and this document does not verify Pega
> claims against a running installation. Where the author is unsure, the text says
> so — see [What this comparison does not know](#what-this-comparison-does-not-know).

## Executive summary

The honest one-line framing:

> Pega puts the **case** at the centre and generates an application around it.
> Atlas puts the **process instance and its event log** at the centre and executes
> a standard model over it.

Neither statement is a criticism of the other product. They are different scopes:
Pega's scope is *build and run the whole business application*; Atlas's scope is
*execute the process correctly, and be inspectable while doing it*.

| Dimension | Pega Platform | Atlas |
|---|---|---|
| Primary category | Model-driven application platform with case management at its core | Durable BPMN 2.x workflow engine with modeler, operations and task surfaces |
| Scope of the product | Process, data model, UI, security, reporting, integration, decisioning, DevOps | Process execution, authoring, operations, human tasks, worker integration |
| Process notation | Proprietary case life cycle (stages/steps) and flow rules; BPMN-like stencil, not BPMN-conformant | BPMN 2.x XML as the executed artefact |
| Decision modelling | Proprietary decision tables/trees, strategies, adaptive and predictive models | DMN decisions (via the embedded temis engine) and FEEL expressions |
| Runtime state | Relational database, rule resolution at runtime, distributed services | Append-only event log plus materialized state in an embedded store |
| Recovery principle | Platform-level transaction, queue and retry semantics | Deterministic replay of the event log through the same apply path |
| Topology | Multi-node cluster with externalized Kafka, Cassandra, Elasticsearch | Single self-contained binary; today single node, single partition |
| End-user UI | Generated application UI (Constellation), DX API, mobile channels | Task inbox with modelled forms, public start links, embeddable forms |
| Case constructs | Stages, sub-cases, SLAs, attachments, correspondence, participants | Not modelled as such; BPMN instances, subprocesses and call activities |
| Business reporting | Report definitions, insights, dashboards | Not present; metrics, event export and instance search instead |
| Operational forensics | Tracer, log analysis, Predictive Diagnostic Cloud | Step-by-step instance replay with per-step variable and decision snapshots |
| Maturity | Established enterprise product | `0.x` developer preview |

## Where each one is genuinely stronger

Stated as strongly as the opposing case deserves, before any qualification.

**Pega's real strengths, in Pega's terms.** Pega does not merely run a process; it
generates the *application* around it. A case type carries a data model, a
generated UI in several channels, participants, attachments, correspondence,
service levels with escalation, reporting, and a security model — all from one set
of models, all versioned in the same rule repository, all deployable through the
same pipeline. Its layered rule structure (rulesets, class hierarchy,
circumstancing) lets one application specialize behaviour by product, region,
channel or customer segment without forking the process. Its decisioning stack
solves a class of problem Atlas does not address at all: real-time next-best-action
with adaptive models learning from outcomes. And it does this in clustered,
horizontally-scaled, multi-environment deployments with an established operational
practice. Anyone dismissing Pega as "a workflow tool with overhead" has
misunderstood what is inside the box.

**Atlas's real strengths, in Atlas's terms.** Atlas executes the standard —
BPMN 2.x XML is the artefact, not a picture of one — so the model is portable, and
its semantics are defined by a specification rather than by a vendor's runtime.
Its state is an append-only event log, which makes two things possible that a
state-mutating platform cannot offer as cheaply: exact deterministic recovery
(replay reproduces live state through the *same* apply function), and a
step-by-step reconstruction of any instance after the fact, with the variable
values *as of that step* and the DMN rules that fired. Coverage is checkable rather
than asserted: a [conformance suite](../../conformance/) registers execution
features against the recognized workflow control-flow patterns, backed by five
oracles including replay equivalence and a differential test against an unrelated
engine. Operationally it is one binary with no database, broker or sidecar to
provision. And it is built to be driven by an agent: the same HTTP API is exposed
as 65 Model Context Protocol tools.

## Process modelling and semantics

| Capability | Pega Platform | Atlas |
|---|---|---|
| Executed artefact | Flow rules and case life cycle rules in the rule repository | BPMN 2.x XML, compiled at deploy time into an integer-indexed graph |
| BPMN 2.0 conformance | Not a conformance target; the stencil resembles BPMN but flow rules are implementation artefacts | The stated target; coverage tracked in [`COVERAGE.md`](../../conformance/COVERAGE.md) |
| BPMN import | Available as a design aid in Pega GenAI Blueprint (generates a case life cycle from a BPMN diagram) | BPMN is the native input; import from other tools is on the roadmap for round-trip fidelity |
| Model portability to other engines | Not applicable — the model is a Pega rule | The deployed artefact is standard XML |
| Case life cycle (stages) | First-class: stages, steps, optional processes, stage entry/exit | Not present as a construct; expressed as BPMN structure |
| Gateways | Decision shapes, when rules, decision tables/trees | Exclusive, parallel, inclusive, event-based |
| Subprocess forms | Sub-processes, sub-cases, spin-off flows | Embedded, event, transaction, ad-hoc and call-activity subprocesses |
| Boundary events | Ticket/SLA-driven interruption, exception flows | Timer, message, signal, error, escalation, conditional, compensation — interrupting and non-interrupting |
| Multi-instance / iteration | Split-for-each, split-join shapes | Multi-instance (parallel and sequential) and standard-loop activities |
| Compensation | Not a first-class BPMN compensation construct (modelled explicitly) | BPMN compensation, including in transaction subprocesses |
| Lanes, pools, message flows | Swimlanes in flows; collaboration is not the modelling centre | Lanes, collaborations with pools and message flows |
| Data in the model | Case data model (properties, data classes), full type system | BPMN data objects as first-class typed, event-sourced entities, with input/output associations |
| Data model above the process | Enterprise class structure and data classes span applications | Process information model: a UML class-diagram subset that gives a data object's type something to resolve against, plus UML model import |
| Simulation before deploy | Blueprint previews and testing tooling | Browser-side token simulation ("play mode") of the un-deployed model |
| Model versioning | Rulesets and rule versions with rule resolution at runtime | Immutable versioned deployments; drafts with version history |
| Behaviour specialization without forking | Circumstancing and the class-inheritance layer cake — a distinctive Pega capability | No equivalent; a variant is a different model or a decision inside one |

The last row is the single largest modelling difference in Pega's favour, and it
is structural rather than incidental: Atlas's compile-time, immutable-graph design
is precisely what rules out runtime rule resolution.

## Decisioning and AI

| Capability | Pega Platform | Atlas |
|---|---|---|
| Business rules | Decision tables, decision trees, when rules, declarative expressions | DMN decision tables with hit policies, evaluated by the embedded temis engine |
| DMN standard | Not supported for import from external systems | The decision format; an embedded DMN editor and a read-only decision-requirements view |
| Expression language | Pega expressions and Java-backed rules | FEEL, compiled at deploy time, used for conditions, timers, mappings, cardinality |
| Decision auditability | Rule execution visible via tracer and logs | Every evaluation recorded as a fact with inputs, outputs and the rule that hit, visible in instance replay |
| Real-time next-best-action | Customer Decision Hub: strategies, propositions, arbitration | Not present, and not a goal |
| Adaptive / predictive models | Adaptive Decision Manager, Prediction Studio | Not present |
| Generative AI in authoring | Blueprint, Infinity Studio, agent skills for building and reviewing apps | In-canvas modeler AI copilot |
| AI agent as a process step | Agentic workflow orchestration of Pega and third-party agents under governance (Infinity '26) | AI agent task executed on the job path as a managed worker, with recorded tool calls |
| Agent-facing API | MCP interfaces for workflows and app-building tools (Infinity '26) | 65 MCP tools over the full HTTP API: author, deploy, run, work tasks, inspect |
| Document/content AI | Document-processing agent services, content extraction | Not present |

## Human work

| Capability | Pega Platform | Atlas |
|---|---|---|
| Work assignment | Assignments to operators and work queues, with routing rules | User tasks with claim, assignment to a person, and candidate groups |
| Routing sophistication | Skills-based, availability, load, cascading approvals | Assignment and candidate groups; no skills or load-based routing |
| Service levels | SLAs at case, stage, process and step level, with goal/deadline milestones and configurable escalation actions | Due date and priority on a user task; escalation modelled explicitly with boundary timer events |
| End-user UI | Generated application UI (Constellation), themes, design system, several channels | Tasks app with forms built in the browser; the modeler's documentation shown as work instruction |
| UI extensibility | React-based Constellation components; DX API for custom front-ends | Forms plus embeddable public forms; hosted apps on an isolated origin |
| Headless consumption | DX API (model-driven REST returning data and UI metadata) | REST API with OpenAPI spec and embedded explorer; no UI-metadata API |
| Mobile | Mobile channels and apps | Not present (responsive web only) |
| Offline work | Offline mobile features (traditional UI mode) | Not present |
| Unauthenticated intake | Web channels and portals | Public process start links, and public forms embeddable cross-origin |
| Attachments and correspondence | Case attachments, correspondence rules, email/SMS templates | Not present as case constructs; outbound mail is a worker capability |
| Participants / stakeholders | Case participants with roles | Not present; RACI-style responsibility metadata on the model |
| Localization | Multi-language application support | Not present |

Service levels and case-shaped human work are the second large capability gap in
Pega's favour, and unlike the layer cake it is not architecturally precluded in
Atlas — it is simply not built.

## Execution, persistence and recovery

| Capability | Pega Platform | Atlas |
|---|---|---|
| State storage | Relational database (work, assignments, history), with Cassandra for decisioning data | Append-only write-ahead log plus materialized state in an embedded key-value store, in the data directory |
| External dependencies | Database, plus externalized Kafka, Elasticsearch/SRS, Cassandra as required | None: one binary, one data directory |
| Execution model | Rule resolution and interpretation at runtime across cluster nodes | Compiled graph; one single-writer goroutine per partition folds commands into events |
| Durability discipline | Database transactions, queue processors, retries | Durable before visible: append → one `fsync` → commit state → side effects |
| Throughput technique | Horizontal node scaling | Group commit (many events per `fsync`), no allocation on the hot path, integer-indexed graph |
| Crash recovery | Platform and database recovery; queued work resumes | Deterministic replay of the log through the same apply function used live |
| Recovery acceleration | Not applicable in the same sense | Recovery checkpoints and WAL compaction |
| Time-travel over one instance | Not available as a product feature | Step-by-step replay with per-step variable snapshots and decision records |
| Long-running instances | Core capability | Core capability (timers, messages, signals, conditional events) |
| Instance migration to a new version | Rule versioning means running work picks up resolved rules | Explicit instance migration with a migration plan, single and bulk |
| Retention and deletion | Archival and purge tooling | History retention with hard delete, per-definition TTL; WAL compaction is a separate step |
| Backup | Database backup practice | Backup and restore, plus whole-instance snapshots |

The architectural trade is visible here in both directions. Pega's runtime rule
resolution buys specialization and hot-fix agility that Atlas's compiled,
immutable deployment deliberately gives up. Atlas's event log buys exact replay and
per-step forensics that a state-mutating platform cannot reconstruct after the
fact.

## Integration

| Capability | Pega Platform | Atlas |
|---|---|---|
| Outbound integration | Connectors: REST, SOAP, JDBC/SQL, MQ, Kafka, file, email, and more | Worker Types: REST, SOAP, SQL, mail (SMTP/Gmail/Graph), SharePoint, Jira, BMC Remedy, Google Sheets, LDAP, Active Directory, Entra ID, SCIM, web scraping, CSV, directory/file |
| Inbound integration | Services (REST, SOAP, email, file, queue) | HTTP API, message correlation, message/signal start events, inbound watches (Jira, Google), Postgres change events |
| Data virtualization | Data pages: cached, parameterized, scoped data access | Not present |
| Custom code in a step | Java, activities, functions | Script tasks in JavaScript, Python and PowerShell, executed by a worker |
| Credential handling | Authentication profiles, keystores | Engine-internal encrypted secret vault (AES-256-GCM); credentials never sit in the model |
| Robotic automation | Pega Robotics (attended/unattended), Workforce Intelligence | Not present |
| Integration without the target existing | Mocking within test tooling | Mockup service tasks (scripted answer, duration, failure rate), OpenAPI mock server, SQL and AD mock modes |
| Where integration code runs | Platform nodes | Operator's choice: worker instances in-process-supervised or out-of-process; the model does not decide |
| Extensibility model | Build rules and components in the platform | One Go package per Worker Type plus one registry entry; worker SDK over the HTTP job protocol |

Integration *breadth* favours Pega in the enterprise-connector sense (queues,
mainframe-adjacent, packaged application connectors), and Atlas's mocking surface
is unusually strong for its stage — a process can run end to end before any of its
integrations exist.

## Operations and observability

| Capability | Pega Platform | Atlas |
|---|---|---|
| Live process view | Case views, work queue monitoring | Live token view: every instance of a process version on one diagram, with per-element token counts |
| Instance forensics | Tracer, Clipboard, log files, PDC | Instance timeline plus step replay with variables and decisions as of each step |
| Error handling | Flow errors, broken queue items, retries | First-class incident model with resolution, retries as a task property, and incident repair forms |
| Repair from the incident | Requeue and manual intervention | Fix the worker configuration directly from the incident; retry |
| Metrics | Autonomic Event Services, Predictive Diagnostic Cloud, node-level monitoring | Prometheus metrics endpoint (token-protected) |
| Event export | Database and BI integration | OpenSearch event exporter |
| Search | Search and Reporting Service (Elasticsearch-based) | Instance search over searchable variables, literal search terms, and an archive search |
| Bulk operations | Bulk actions on work | Bulk terminate and bulk migrate |
| Business reporting | Report definitions, insights, dashboards, process mining (separate offering) | **Not present** — no report builder, no business dashboards, no mining |
| Architecture-level view | Enterprise-level app documentation | Panorama: ArchiMate 3.2 architecture modelling with live operational overlays and a derived landscape mesh |

## Application lifecycle and governance

| Capability | Pega Platform | Atlas |
|---|---|---|
| Unit of delivery | Application with rulesets and versions | Process application: versioned, publishable, deployable unit |
| Source control | Branch-based development inside the platform; export/import of rulesets | Git-backed applications; drafts with version history |
| Promotion between environments | Deployment Manager pipelines | Remote deployment targets; deploy an application release to another server |
| Collaborative authoring | Multi-developer branching and merging in the rule repository | Live collaborative modeling sessions on a diagram |
| Automated testing of processes | Unit and scenario test rules, test coverage tooling | **Not present** as a product feature (the engine's own test suite is not a user-facing capability) |
| Validation before deploy | Rule validation, guardrails, and warnings | Problems panel validating the model against the engine version that will run it |
| Deactivating / deprecating a version | Rule availability settings | Deactivate a deployed process; deprecate a version |

## Security and access

| Capability | Pega Platform | Atlas |
|---|---|---|
| Authentication | Extensive: SSO, SAML, OIDC, Kerberos, custom | Local accounts (bcrypt, session cookie) and OpenID Connect with claim-to-role mapping |
| Authorization model | Access groups, roles, privileges, ABAC (attribute-based access control), field-level security | Four roles enforced per route (`admin`, `modeler`, `operator`, `user`), project visibility (`viewer`/`editor`), groups, worker ownership |
| Field-level / data-level security | Yes, including ABAC on properties | Not present; binding process-variable visibility to a permission is an open item |
| Machine access | OAuth 2.0 clients, service accounts | API tokens with fail-closed scopes; MCP as an OAuth resource server |
| Audit | Case history and security auditing | `auth.*` security audit events (login, failure with reason, throttling, authorization denial, account lifecycle), grant audit log, variable write attribution |
| Encryption at rest | Platform and database-level encryption options | **Only vault secrets are encrypted**; log, state store and sidecar stores are plaintext |
| Multi-tenancy | Multitenant capability exists in the product line | Not present: one installation is one data directory, one user directory, one authorization model |
| Transport security | Standard | Built-in TLS listener |

## Scale and availability

| Capability | Pega Platform | Atlas |
|---|---|---|
| Horizontal scaling | Multi-node clusters, tiers, containerized deployment with Helm charts | **Not available today**: one process per data directory, one partition; replicated partition cells are designed but not delivered |
| High availability | Established cluster patterns | **Not available today**; no WAL replication, no failover |
| Throughput ceiling | Scales with nodes and database | Bounded by one writer per partition; the design goal is very high per-partition throughput, with published benchmark baselines |
| Kubernetes | Official Helm charts, externalized services | Container image and a Helm chart, fixed at one replica by construction |
| Geographic distribution | Supported deployment patterns | Not addressed |

This is the most consequential capability gap in Pega's favour for any deployment
whose availability requirement exceeds a single node.

## Choosing between them, on capabilities alone

**Pega fits when** the deliverable is a whole business application rather than a
process: a case with a data model, generated UI across channels, participants,
attachments, correspondence, service levels and business reporting; behaviour that
must be specialized per product, region or segment without forking; real-time
decisioning or adaptive models; clustered, highly available, multi-environment
operation.

**Atlas fits when** the deliverable is the execution of a formal process model:
BPMN as a portable, standard artefact readable by business and engineering alike;
decisions expressed in DMN and audited per evaluation; a durable, replayable record
that answers "why did this instance do that?" from recorded facts rather than
reconstructed logs; operation as a single binary with no database or broker; and
authoring or operation driven by an AI agent over MCP.

**Neither fits** if the requirement is Pega's application scope *and* Atlas's
standards portability *and* production maturity at once: Pega does not execute the
standard, and Atlas is not production-ready.

## What this comparison does not know

Stated explicitly, because the gaps change how much weight the tables can carry:

1. **Pega version drift.** Pega's surface differs by version and edition. The
   statements here reflect public documentation and the Infinity '26 announcements;
   individual features may have moved between editions, been renamed, or been
   deprecated.
2. **Pega BPMN and DMN support.** The public position is that BPMN 2.0 is not an
   execution-conformance target and DMN import is not supported, with BPMN import
   appearing as a Blueprint design aid. Whether any newer import or export path
   exists beyond that is not verified here.
3. **Pega's exact SLA, routing and ABAC semantics** are summarized from product
   documentation, not from a running installation.
4. **Pega Robotics' current product positioning** is not verified; RPA capability
   is asserted to exist, its emphasis in the current release is not.
5. **Performance is not compared at all.** Atlas publishes reproducible benchmarks
   on a named machine; no equivalent, comparable Pega figures were consulted, and
   cross-product throughput claims without a shared workload would be meaningless.
6. **Atlas capability statements are repository-derived.** Several capabilities are
   described in architecture decision records still marked *Proposed* even though
   the code exists; the tables above follow the code and documentation, not the ADR
   status field.
7. **Non-functional qualities are out of scope**: usability, accessibility,
   ecosystem, skills availability, support model, and everything commercial.

## Related

- [n8n and Atlas](n8n.md) — integration automation versus durable BPMN orchestration
- [MIM and Atlas](mim.md) — Microsoft Identity Manager's connector surface mapped to Atlas Worker Types
- [Conformance suite](../../conformance/) — what Atlas covers of BPMN, and the oracles behind the claim
- [Roadmap](../../ROADMAP.md) — what is built, in progress, and planned
