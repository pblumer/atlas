# ADR-0536: The Worker SDK is Apache-2.0, and atlas hub lists executable Worker Types in two curated tiers

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-05
- **Deciders:** pblumer (maintainer)
- **Open question:** whether the licence split below holds legally: that a Worker Type
  speaking the Worker protocol over HTTP and linking an Apache-2.0 SDK is not a work
  based on the AGPL-3.0-only engine; that the current code of `workersdk` may be
  relicensed (it was contributed under AGPL-3.0-only with DCO sign-off, no CLA); and
  what liability the registry operator takes on by attesting a release in the
  `verified` tier. This record is written on that gap and needs legal review before it
  is accepted.
- **Question checked:** 2026-11

## Context and problem statement

[ADR-0535](0535-worker-types-as-distributed-artifacts.md) makes Worker Types signed
artifacts that atlas launches and atlas hub distributes, for four origins: free and
commercial Worker Types by atlas, third-party and community Worker Types, and
organisation-internal ones. It leaves two decisions open and makes both a gate before
the first Worker Type binary is published:

1. **The licence of the Worker SDK and the protocol contract.** atlas is
   AGPL-3.0-only. A Worker Type built against an AGPL SDK and distributed is, on the
   common reading, itself bound by the AGPL. That decides whether a vendor can ship a
   Worker Type without publishing its source.
2. **What the registry signature attests.** Under
   [ADR-0499](0499-blueprint-registry-and-distribution.md) a release in a signed index
   is *attested by the registry operator*. For a Blueprint that is harmless. For
   executable code with access to production credentials, the operator of the public
   atlas hub then vouches for code somebody else wrote.

The owner's direction (2026-11-05): vendors should be able to offer Worker Types
without disclosing their source; the public hub should be curated, possibly with a
separate area that is explicitly not vetted.

## Decision drivers

- The engine's protection stays as it is: nobody may run a modified atlas as a closed
  service.
- Vendors of the systems atlas integrates (ERP, ITSM, identity) can publish Worker
  Types on their own terms, including closed source, because the most valuable Worker
  Types come from them.
- An organisation can build Worker Types for its own use without any licence question.
- What a signature in the public atlas hub means is stated, limited and checkable; no
  unvetted executable code is listed under the atlas hub key.
- An operator can decide from the signed index alone which releases it accepts, and
  where it lets them run.
- The curation effort fits one maintainer's capacity and can grow later.

## Considered options

### Licence

1. **SDK and protocol contract under AGPL-3.0-only**, like the engine.
2. **SDK under MPL-2.0**: changes to the SDK's own files stay open, the Worker Type's
   code does not.
3. **SDK and protocol contract under Apache-2.0** (chosen).
4. **No SDK, only the protocol contract** under a permissive licence.

### Curation

A. **Open registry:** anyone publishes, atlas hub signs listing only.
B. **Fully curated, one tier:** every release reviewed by hand.
C. **Two curated tiers in atlas hub (`verified`, `community`); anything unvetted only in
   registries other people run** (chosen).

## Decision outcome

Chosen: **licence option 3** and **curation option C**.

### 1. Licence split

| Part | Licence |
|---|---|
| The engine, the server, the Console and every package not named below | AGPL-3.0-only, unchanged |
| `workersdk` as its own Go module | Apache-2.0 |
| The Worker protocol contract (`docs/contracts/worker-protocol/`) | Apache-2.0 |
| The Worker Types atlas ships in this repository | AGPL-3.0-only, unchanged; their licence is a separate decision |
| Commercial Worker Types by atlas | the owner's own licence terms; they are built from the owner's own code against the Apache-2.0 SDK |
| Third-party, community and organisation-internal Worker Types | their author's choice, closed source included |

Apache-2.0 rather than MIT because it carries an explicit patent grant and patent
retaliation clause, which matters for an SDK that vendors build products on.

**Conditions before the relicensing takes effect:**

- `workersdk` imports no AGPL package of this repository. Met since 2026-11-05: the
  lookup wire shape moved into `workersdk/lookup` and the logger became an option
  (ADR-0535, *As built*); `TestTheSDKImportsNothingOfAtlas` admits no atlas package
  outside `workersdk`.
- The holders of rights in the code moved into the module agree to the relicensing,
  and legal review confirms it (open question).
- The module carries its own `LICENSE` file. Contributions to it are accepted under
  Apache-2.0 with DCO sign-off (inbound equals outbound), stated in `CONTRIBUTING.md`.
- `THIRD_PARTY_NOTICES.md` and the release notes say which parts are under which
  licence.

**What this does not change:** a modified atlas server offered over a network still
owes its users the source under AGPL section 13. The split moves only the client of
the protocol out of the AGPL, not the server.

### 2. Two curated tiers in atlas hub

The public atlas hub lists executable Worker Types in exactly two tiers. The tier and
what was checked are part of each release's signed entry in the index, so a trust
policy can act on them:

```json
"attestation": {
  "tier": "verified",
  "checked": ["publisher-identity", "namespace", "publisher-signature",
              "provenance", "automated-scan", "manual-review"],
  "reviewedAt": "2026-11-05",
  "reviewMode": "source"
}
```

| | `verified` | `community` |
|---|---|---|
| Publisher identity | legal entity or person verified, domain control proven | account and domain or e-mail verified |
| Namespace bound to the publisher's key | yes | yes |
| Publisher signature (ADR-0535 §6) | required | required |
| Build provenance | required | shown if present |
| Automated checks | manifest validation, malware scan, SBOM, declared network targets and prerequisites match observed behaviour in a sandbox run | the same automated checks |
| Duplicate-delivery test | required for every operation declared `natural`, `key` or `check`: against a mock of its target the same job is delivered twice, and once more after a simulated crash between side effect and report, and the effect happens once ([ADR-draft-operations-declare-idempotency-and-effect](draft-operations-declare-idempotency-and-effect.md) §5); operations declared `none` are exempt and listed as such | shown if run |
| Manual review | yes: source review for open source; for closed source a review under NDA, or the vendor's attestation plus the behavioural test, recorded as `reviewMode` | no |
| Security contact and disclosure policy | required | required |
| Commitment to act on a revocation request | within a stated time | best effort |
| Trust policy default on a new installation | accepted | refused until the operator enables it |

**No third tier in atlas hub.** Unvetted executable code is never signed with the atlas
hub key. The «Wild West» exists where it belongs: in registries other people operate,
which the open registry protocol of ADR-0499 already supports. An operator adds such a
registry explicitly, pins its key, and the Console shows its releases as coming from
that registry, not from atlas hub.

**The attestation states what was checked, not that the code is safe.** The listing
terms say so, and the Console shows the `checked` list beside every release rather
than a single badge. What the operator of atlas hub is liable for is part of the open
question.

**Blueprints, element templates and other non-executable content** are not affected;
they keep the ADR-0499 rules.

### 3. Trust policy defaults

ADR-0535 §8 lets the operator decide which tiers may run where. The defaults on a new
installation are:

- first-party (`io.atlas.*`) and `verified`: all launchers;
- `community`: refused; once enabled, only `container` and `kubernetes`;
- organisation-internal registries: as the operator configures them;
- on Windows, the `process` launcher runs only first-party, `verified` and
  organisation-internal Worker Types (ADR-0535 §2).

### 4. Review capacity

The `verified` tier is deliberately small at first: the maintainer reviews, and a
release waits in `community` (or unlisted) until reviewed. Delegating review to named
reviewers later needs only that their key may sign attestations, recorded in a
follow-up record; the index format does not change.

### 5. De-listing

A release whose publisher no longer meets its tier's criteria, or that fails a later
check, is moved down or revoked through the revocation list of ADR-0535 §7. Moving a
release down is itself a signed index change; installations see the new tier at their
next refresh and apply their trust policy to it, including stopping instances where
the policy no longer admits the release.

## Consequences

- **Positive:** vendors can ship closed-source Worker Types, commercial Worker Types by
  atlas are possible without a dual licence for the engine, and organisation-internal
  Worker Types raise no licence question.
- **Positive:** the engine stays under the AGPL; the protection against a closed
  hosted fork is unchanged.
- **Positive:** an operator reads in the signed index what was checked and decides from
  that; atlas hub never signs unvetted executable code.
- **Negative / trade-offs accepted:** a competitor may build Worker Types and keep them
  closed; that is the point of the split and is accepted.
- **Negative / trade-offs accepted:** curation costs review time and carries a
  liability question; closed-source review is weaker than source review and is labelled
  as such.
- **Negative / trade-offs accepted:** two licences in one repository need care in
  notices and in contributions.
- **Follow-ups / risks to watch:** legal review (open question); listing terms and a
  publisher agreement; the `attestation` field in the next index schema version
  (ADR-0499 compatibility review); key and process for signing attestations; the SDK's
  own Go module, which ADR-0535 defers to this decision.

## Pros and cons of the options

### Licence option 1 — SDK under AGPL-3.0-only

- Good: the whole ecosystem stays open; only the owner, as rights holder, can ship
  closed Worker Types, which is a commercial advantage.
- Bad: the protocol is plain HTTP and JSON, so a vendor can implement it without the
  SDK and avoid the AGPL; the licence then mostly adds friction and a legal question
  rather than openness. Vendors who will not publish source stay away, which costs the
  integrations atlas most needs.

### Licence option 2 — SDK under MPL-2.0

- Good: improvements to the SDK itself stay open; file-level copyleft works with Go's
  static linking, where LGPL's relinking requirement does not fit well.
- Bad: a second copyleft licence for vendors' lawyers to assess, for little practical
  gain over Apache-2.0 on a small client library. A defensible alternative if keeping
  SDK changes open matters more than adoption.

### Licence option 3 — SDK under Apache-2.0 (chosen)

- Good: the lowest barrier for vendors and organisations; explicit patent grant.
- Bad: SDK improvements can stay closed.

### Licence option 4 — protocol contract only, no SDK

- Good: no relicensing of existing code.
- Bad: every author reimplements leasing, fencing, retries and queries, which is where
  correctness bugs live; the built-in Worker Types already need the SDK.

### Curation option A — open registry

- Good: lowest barrier, fastest growth.
- Bad: the atlas hub key would sign unvetted code with production credentials; open
  package registries are a recurring target of supply-chain attacks.

### Curation option B — one fully curated tier

- Good: the strongest statement per release.
- Bad: every release waits on manual review; does not scale with one maintainer, and
  gives community authors no place to start.

### Curation option C — two curated tiers, unvetted elsewhere (chosen)

- Good: a clear, checkable statement per release; a place for community work that is
  off by default; unvetted code exists without the atlas hub signature.
- Bad: two sets of criteria to run and explain.

## Links

- answers the licence and curation gates of
  [ADR-0535](0535-worker-types-as-distributed-artifacts.md)
- extends [ADR-0499](0499-blueprint-registry-and-distribution.md) (attestation in the
  signed index)
- relates to [ADR-0483](0483-technical-capability-conformance-governance.md)
  (conformance evidence as an input to `verified`)
- relates to [ADR-0299](0299-worker-type-admission-criteria.md) (admission criteria for
  Worker Types in this repository)
