# ADR-DRAFT: The Entra Worker reads the tenant's licence subscriptions and the last sign-in

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-05
- **Deciders:** Atlas maintainers
- **Open question:** Neither `list-subscribed-skus` nor `signInActivity` has run against a real tenant through this Worker yet; the request shapes, permissions and the P1 requirement come from Microsoft's documentation and a recorded Graph. Does the devenv tenant (deploy/opentofu/m365-devenv) answer both as documented once `LicenseAssignment.Read.All` and `AuditLog.Read.All` are consented?
- **Question checked:** 2026-11

## Context and problem statement

An organisation on a Microsoft Enterprise Agreement buys Microsoft 365 licences in
quotas — so many E3, so many E5, so many F3 — and has to know, before the true-up, how
many of each it has bought, how many are assigned, and which assigned ones belong to
people who no longer sign in. The example application «Lizenzmanagement» manages those
quotas as a process. It needs two reads the Entra Worker Type (ADR-0172) did not offer:

1. the tenant's subscriptions with their bought and consumed units — Microsoft Graph
   `GET /subscribedSkus`;
2. each user's last sign-in — the `signInActivity` property of a user.

The generic REST Worker Type could call both, but it would need a second credential for
the same tenant — the reason ADR-0172 exists.

## Decision drivers

- One credential per tenant: the Entra Worker already holds it (ADR-0172).
- The result must be shaped like the Worker's other listings, so a model handles it as
  it handles `list-users`.
- Least privilege: the permission each read needs is named, and the devenv grants exactly
  that.
- What Graph refuses must be refused at deploy, not answered by a 400 at run time.

## Considered options

1. A new operation `list-subscribed-skus` on the Entra Worker; `signInActivity` read
   through the `select` that `list-users` and `get-user` already take.
2. A dedicated operation for the last sign-in (`get-sign-in-activity`).
3. The generic REST Worker Type with its own app registration.

## Decision outcome

Chosen option: **1**.

**`list-subscribed-skus`** is a row in the Entra Worker's operation table and its
compiler mirror: `GET /subscribedSkus`, a listing (`IsList`) whose result variable
receives — like `list-users`, `list-groups` and `list-deleted-groups` — a bare **JSON
array**, one item per SKU as Graph sends it: `skuId`, `skuPartNumber`,
`capabilityStatus`, `appliesTo`, `consumedUnits`, `prepaidUnits {enabled, suspended,
warning, lockedOut}`, `servicePlans`, `subscriptionIds`, `accountName`, `accountId`, `id`.
Nothing is renamed or flattened. A `resultVariable` is required.

Microsoft documents that the method "supports only the `$select` OData query parameter
… It doesn't support `$filter`". So the operation is marked *select-only*
(`SelectOnly` / `selectOnly`): it takes `select` and the `maxUsers` bound (default 1000,
far above any tenant's SKU count), and the compiler refuses `filter`, `search`,
`advancedQuery` and `pageSize` on it, naming the field; the Worker repeats the refusal
for a job built by hand. The request carries the select and nothing else — no `$top`, no
`$count`, no `ConsistencyLevel` header. Pages are still followed if Graph ever offers a
`@odata.nextLink`.

**Permission:** the least privileged application permission Microsoft names is
**`LicenseAssignment.Read.All`**; `Organization.Read.All`, `Organization.ReadWrite.All`,
`Directory.Read.All` and `Directory.ReadWrite.All` also suffice but grant more
([List subscribedSkus, v1.0](https://learn.microsoft.com/en-us/graph/api/subscribedsku-list?view=graph-rest-1.0)).

**The last sign-in** needs no new operation. `list-users` and `get-user` already pass an
authored `select` through as `$select`, so a model writes
`select="id,userPrincipalName,assignedLicenses,signInActivity"` and reads
`=konto.signInActivity.lastSuccessfulSignInDateTime` (or `lastSignInDateTime`, the last
interactive *attempt*, successful or not). Microsoft documents that "details for the
**signInActivity** property require a Microsoft Entra ID P1 or P2 license and the
`AuditLog.Read.All` permission", beside `User.Read.All`, and that with `signInActivity`
selected a page holds at most 500 users
([List users, v1.0](https://learn.microsoft.com/en-us/graph/api/user-list?view=graph-rest-1.0),
example 11; [signInActivity](https://learn.microsoft.com/en-us/graph/api/resources/signinactivity?view=graph-rest-1.0)).
`lastSuccessfulSignInDateTime` exists since 2023-12-01 and is not backfilled.

### Consequences

- **Positive:** a licence-quota process reads bought and assigned units and the last
  sign-in through the Worker it already has, with one more read permission each.
- **Positive:** the listing envelope is the one every Entra listing has, so a model sums
  `prepaidUnits.enabled` and `consumedUnits` over an array.
- **Negative / trade-offs accepted:** `signInActivity` silently depends on the tenant's
  licence. Without P1 Graph refuses the select; the job fails with Graph's error, which
  is the honest answer.
- **Follow-ups / risks to watch:** there is no Entra mock mode in Atlas (unlike AD's
  `ATLAS_AD_MOCK` or SharePoint's mockup, ADR-0484); the operation is tested against a
  fake Graph in `connector/entra/subscribedskus_test.go`, which carries an E3/E5/F3
  tenant. A model run end to end without a tenant still completes the Entra jobs by hand.
- **Devenv:** `deploy/opentofu/m365-devenv` grants the Entra worker app
  `LicenseAssignment.Read.All` and `AuditLog.Read.All` by default.

## Pros and cons of the options

### Option 1 — `list-subscribed-skus`, and `select` for the sign-in
- Good: one credential; a listing shaped like the others; no new field for the sign-in.
- Bad: a model has to know to name `signInActivity` in the select.

### Option 2 — a dedicated sign-in operation
- Good: discoverable in the operation list.
- Bad: a second read of the same user object; a process that lists users for their
  licences would list them again for their sign-in.

### Option 3 — the REST Worker Type
- Good: no change to Atlas.
- Bad: a second app registration and credential for the same tenant; no paging, no
  deploy-time check.

## Links

- relates to ADR-0172 (the Entra Worker Type)
- `docs/entra-permissions.md`
