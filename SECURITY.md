# Security Policy

## Status

Chrampfer is in early development and is **not yet ready for production use**. There are no supported releases and no security guarantees at this stage. Treat the current code as experimental.

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

When Chrampfer matures, areas of particular security relevance will include:

- **Expression evaluation** (FEEL): untrusted process definitions must not be able to escape the evaluator or exhaust resources.
- **Deployment input**: BPMN XML parsing must be hardened against malicious input (entity expansion, oversized models).
- **The job-worker protocol**: authentication, authorization, and job-lease fencing.
- **The exported-log stream**: ensuring it does not leak sensitive variable data without controls.

These will be addressed as the corresponding milestones land (see the [roadmap](ROADMAP.md)).
