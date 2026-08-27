# ADR-DRAFT: The Active Directory mockup switch belongs in the Console

- **Status:** Proposed
- **Date:** 2026-08-26
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0181](0181-ad-connector-mock-mode.md) gave the Active Directory connector a mockup
mode and put the switch on the worker: `ATLAS_AD_MOCK=1` in the environment the AD
worker is started with. The reasoning was about *who owns the decision* — an operator,
not a model — and that reasoning stands. What it got wrong is the **ceremony**.

An environment variable is set once, by whoever starts the process. Since
[ADR-0182](0182-ad-default-offload.md) the AD worker is a child Atlas starts itself, so
"set the variable" means **restart the server** — and a supervised worker restarted from
the Workers view does not help, because it re-inherits the environment of the running
parent, in which the variable is still absent. So the switch that exists to make trying
a process out cheap costs an engine restart, and the person who most wants to flip it —
a process author part-way through drafting a joiner — is the person least placed to
restart the server everyone else is using.

That is a bad fit between a decision's owner and its mechanism, and it showed up the
first time somebody asked how to mock a directory on a running instance.

## Decision drivers

- **Flip it where the decision is made.** The operator deciding what an integration
  talks to is already in the Console's Connectors page.
- **No engine restart.** Trying a process out must not cost every other instance on the
  server.
- **The model still says nothing.** ADR-0181's core holds: a mockup flag in a model is
  one that eventually ships set, and a task that reports success while touching nothing
  is the worst failure mode available.
- **One contract with the worker.** What the Console writes and what a hand-run worker
  reads must be the same variable, or the supervised path quietly becomes the only
  tested one (ADR-0157).
- **Do not disturb a running installation.** A server started with `ATLAS_AD_MOCK` today
  must behave identically tomorrow.

## Considered options

1. **Keep the environment variable only.** Document the restart.
2. **A Console switch**, stored org-wide, rendered into the supervised worker's
   environment.
3. **An AD connector record**, like mail's: AD becomes a managed kind and the mockup is
   a property of a record a model names.
4. **A checkbox on the task** in the Modeler.

## Decision outcome

Chosen: **option 2 — a switch in the Console**, on the Connectors page, beside the
managed connectors and the vault.

The mechanism is the one [ADR-0182](0182-ad-default-offload.md) already built. That
record made the engine render the AD worker's environment — the bind-password
references its deployed models name — and made a change to it restart the child through
the existing refresh. The switch is one more line in that same rendering. Flip it, the
worker comes back mocked, and Atlas keeps running. There was no new delivery mechanism
to invent, which is the strongest argument that the switch belongs where it now is.

**A stored decision is authoritative; no stored decision changes nothing.** The absence
of the setting and a stored "off" are different states. With no record, whatever
`ATLAS_AD_MOCK` the server was started with keeps deciding — so an existing installation
is untouched until somebody flips the switch. With a record, the engine renders `1` or
`0`, and a stored "off" overrides an inherited "on": a switch that says off while the
worker still simulates would be lying to the person who flipped it, and the Console says
which of the two states it is in.

**The names stay the worker's own.** The Console writes `ATLAS_AD_MOCK` and
`ATLAS_AD_MOCK_SEED` — the same variables an operator sets by hand for a worker in
another network. There is no private channel between a supervised worker and its
parent, so the documented contract and the one the Console uses cannot drift apart.

**Why not option 3.** It is the most literal reading of "configure it on the connector",
and it fails on AD's shape: [ADR-0166](0166-active-directory-connector.md) decided the
directory is *model* data — an AD task authors its own server URL and bind DN, and names
no connector. Giving AD a record would either mean a record nothing references, or
reversing ADR-0166 so that models name connectors like mail does. That is a real
option for a real reason (per-directory granularity: one test forest mocked, the
production one not), and it is a bigger decision than a switch. It stays on the
follow-up list.

**Why not option 4.** Unchanged from ADR-0181: it is model data, it ships, and the
failure is silent.

### Consequences

- **Positive:** the mockup is reachable where an operator already configures
  integrations; no server restart, so trying a process out costs nothing anybody else
  notices; the seed file is configured in the same place instead of on a command line;
  and the state is *visible* — a page that says "mockup" is a better answer to "did that
  account really get created?" than reading a worker's log.
- **Negative / trade-offs accepted:** the switch is instance-wide, so a server cannot
  serve one directory mocked and another for real — that is option 3's granularity and
  it is not here. The setting is stored design-time state, so it travels with a
  design-time backup: a restored instance restores the switch, which is right for a test
  instance and worth knowing for a production one. And the seed path is the *worker's*
  path, typed in a browser that may be nowhere near that host — an unreadable path fails
  the worker's start, visibly, in the Workers view.
- **Follow-ups / risks to watch:** option 3 if per-directory granularity is ever wanted;
  the Console currently shows the switch's state but not the worker's confirmation of
  it, which the Workers view has and could be surfaced next to the switch.

## Pros and cons of the options

### Option 1 — environment variable only
- Good: nothing to build; one contract.
- Bad: costs an engine restart, which is the wrong price for a thing meant to make
  drafting cheap.

### Option 2 — a Console switch (chosen)
- Good: no restart; visible; reuses ADR-0182's delivery; keeps the model clean.
- Bad: instance-wide, not per directory; stored state travels with a backup.

### Option 3 — an AD connector record
- Good: per-directory granularity; the most literal "open the config and switch it".
- Bad: needs ADR-0166 reversed, so a model would have to name a connector it currently
  does not have.

### Option 4 — a checkbox on the task
- Good: right where the author is working.
- Bad: model data — it ships to production, and the task then reports success while
  doing nothing.

## Links

- amends ADR-0181 (Active Directory connector mock mode) — the switch it placed in the environment
- builds on ADR-0182 (Active Directory on a worker by default) — whose environment rendering delivers it
- relates to ADR-0166 (Active Directory connector) — why AD has no connector record to hang this on
- relates to ADR-0157 (worker processes, supervision and console) — the Workers view that confirms it
- relates to ADR-0041 (connector management and secret store) — the Console page it now shares
