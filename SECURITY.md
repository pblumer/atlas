# Security Policy

## Status

Atlas is in early development. The `0.x` line is a **developer preview** and is
**not ready for production use** — the pre-1.0 API and on-disk formats are
unstable, and there are no hard security guarantees at this stage. Treat it as
experimental.

## Supported versions

Only the **latest `0.x` release** (and the `main` branch it is cut from) is
supported. Security fixes land in the next release rather than as backported
patches to older tags; there are no long-term-support branches before 1.0.

| Version | Supported |
|---------|-----------|
| latest `0.x` / `main` | ✅ |
| older `0.x` tags | ❌ |

## Reporting a vulnerability

Please report security issues **privately**. Do not open a public GitHub issue for a vulnerability.

- Use GitHub's [private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability) on this repository, or
- contact the maintainer directly via the address on their GitHub profile.

Please include:

- a description of the issue and its impact,
- steps to reproduce (a minimal BPMN model and command sequence is ideal),
- affected commit/version,
- any suggested remediation if you have one.

## What to expect

- Acknowledgement of your report as soon as practical.
- An assessment of severity and scope.
- Coordinated disclosure once a fix is available, with credit to the reporter unless you prefer otherwise.

Because the project is pre-release, fixes will generally land on the main branch rather than as backported patches.

## Scope notes

When Atlas matures, areas of particular security relevance will include:

- **Expression evaluation** (FEEL): untrusted process definitions must not be able to escape the evaluator or exhaust resources.
- **Deployment input**: BPMN XML parsing must be hardened against malicious input (entity expansion, oversized models).
- **The job-worker protocol**: authentication, authorization, and job-lease fencing.
- **The exported-log stream**: ensuring it does not leak sensitive variable data without controls.

These will be addressed as the corresponding milestones land (see the [roadmap](ROADMAP.md)).

## Identity-aware Worker execution: what is enforced where

[ADR-0475](docs/adr/0475-identity-aware-worker-execution.md)
(proposed, partly built) lets a governed Worker act only after Atlas decides and records a
Permit. How strong that guarantee is depends on the mode, and a report about it should
say which mode it concerns:

| Mode | What Atlas enforces | What it does not |
|---|---|---|
| *legacy* (no identity policy) | nothing new | — |
| `service` + `shadow` | nothing: decisions are recorded and never block | this is **not** enforcement |
| `service` + `enforce` | a refusal fails the job; a completion without a recorded Permit is refused | the Worker Instance holds its own credential, so a modified Worker can still call the target system; enforcement is cooperative |
| `delegated-fixed`, `delegated-per-job` (always enforced) | the person's credential is released only after a recorded Permit, for one action, only to the supervised process serving that Worker Type (it presents a per-start broker key), and only for an endpoint that is Microsoft Graph | a released access token is valid at the provider until it expires (typically about an hour); Atlas cannot recall it. A compromise of the SharePoint Worker process itself obtains what it is released |

Changing or deleting a governed Worker is an administrator's act, like its identity
policy. Approving an action and allowing a Worker to act as you need your own session; an
application you granted access to your account can do neither.

In scope: a way to obtain a delegated credential without a recorded Permit, to have an
approval count for a different action, to approve an action that runs for yourself, to
reach a connection credential through any route, to redirect a delegated Worker's
traffic, or to make a refused action's consequence disappear on restart. Out of scope for this build, because it is not
claimed: brokering to external Worker Instances (refused), Worker Types other than
SharePoint (not governed), and egress control of a Worker Instance's network traffic.
