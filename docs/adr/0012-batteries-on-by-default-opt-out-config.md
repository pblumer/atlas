# ADR-0012: Batteries-on-by-default, opt-out configuration

- **Status:** Proposed
- **Date:** 2026-07-03
- **Deciders:** Core team

## Context and problem statement

ADR-0011 introduces `cmd/atlasd`, a server binary that will grow several optional
capabilities over time — the web surfaces (operations console, modeler), an MCP
endpoint, metrics, and more. Every optional capability raises the same question:
is it off until enabled, or on until disabled?

The two philosophies pull in opposite directions. "Secure/minimal by default"
ships nothing extra unless asked; "batteries-on by default" ships everything and
lets you subtract. The choice sets the first-run experience and the ergonomics of
every future feature, so it is worth deciding once, explicitly, rather than
letting each feature pick its own default.

## Decision drivers

- **Developer-first experience.** A newcomer should get the full product from a
  single `atlasd` with no flags — see the engine, the web UI, MCP, everything —
  and only then decide what to remove.
- **Discoverability.** Features that are off by default are features most people
  never find. On-by-default makes the surface self-advertising.
- **One consistent rule** for every current and future capability, so behavior is
  predictable and features don't each invent their own default.
- **Uniform configuration mechanism.** Toggles must work in the environments
  `atlasd` runs in (containers, CI), i.e. environment variables, not only flags.
- **Honest about production.** On-by-default must not quietly become
  insecure-by-default; the escape hatch and the hardening story must be clear.

## Considered options

1. **Batteries-on by default, opt-out via environment variables.** Bare `atlasd`
   enables every capability; each is disabled explicitly (e.g. `ATLAS_WEB=off`).
2. **Minimal by default, opt-in.** Bare `atlasd` runs only the core; every
   surface must be switched on.
3. **Profiles / presets.** Named bundles (`--profile=dev|prod`) select which
   capabilities run.

## Decision outcome

Chosen option: **batteries-on by default, opt-out via environment variables.**

- Running `atlasd` with no configuration enables **everything it offers** — API,
  web surfaces, MCP, and any future optional capability.
- Turning a capability **off requires an explicit environment variable**. Not
  setting the variable always means "on."
- Convention (details are a follow-up, but the shape is decided): each optional
  capability gets one boolean-style env var under the `ATLAS_` prefix that
  defaults to enabled, and disabling is unambiguous, e.g.
  `ATLAS_WEB=off`, `ATLAS_MCP=off`. The default (unset) is always the fully
  enabled state.
- **This is a standing principle, not a one-off.** Every future optional feature
  added to `atlasd` is on by default and opt-out by the same convention, unless a
  later ADR supersedes this one.

### Consequences

- **Positive:** Zero-config full-featured first run — the core developer promise.
  Features are discoverable because they are present, not hidden. A single,
  predictable rule for the whole binary. Subtractive configuration composes
  cleanly with container env injection.
- **Negative / trade-offs accepted:** On-by-default is a larger attack/resource
  surface out of the box. Operators who want a minimal footprint must know which
  variables to set — the burden is on the person subtracting, not the person who
  forgot to add. New capabilities inherit exposure automatically, so each must be
  safe (or safely bindable) in its default-on state.
- **Follow-ups / risks to watch:** Define the exact env-var grammar and accepted
  truthy/falsy values in one place, and enumerate the capability toggles as they
  land. Ensure default-on surfaces are not default-*insecure* — bind/auth
  defaults (e.g. localhost binding, or required auth before remote exposure) are
  their own decision, separate from this on/off principle. Document every toggle
  in one discoverable place (a config reference). Consider whether a single
  "minimal/production" master switch is worth offering later as sugar over the
  individual opt-outs (would not replace them).

## Pros and cons of the options

### Option 1 — batteries-on, opt-out
- Good: best first-run experience; discoverable; one consistent rule; matches the
  dev-first principle.
- Bad: larger default surface; hardening is subtractive and must be documented.

### Option 2 — minimal, opt-in
- Good: smallest, most conservative default surface.
- Bad: newcomers see a fraction of the product; features are easy to miss;
  contradicts the dev-first goal.

### Option 3 — profiles/presets
- Good: expresses intent ("prod") in one switch.
- Bad: hides which capabilities a profile implies; still needs per-capability
  toggles underneath; more machinery than the principle needs today. Can be added
  later on top of option 1.

## Links

- applies to `cmd/atlasd` and its surfaces from ADR-0011
- governs configuration defaults for all future optional capabilities of `atlasd`
