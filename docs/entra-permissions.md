# Entra ID worker — least-privilege Graph permissions

The `entra` Worker Type ([ADR-0172](adr/0172-entra-id-connector.md)) is worker-only:
the tenant id, client id and client secret live in the worker's environment or the
Console vault, never in the engine. That app registration is the most valuable secret
any Atlas worker holds — an application permission such as `User.ReadWrite.All` can
create and disable accounts across the **whole** directory. This page names the smallest
set of Microsoft Graph **application** permissions each operation needs, so an operator
grants what their processes use and nothing more.

## Two facts that bound the minimization

- **Application permissions are tenant-wide.** Unlike a delegated permission or an
  administrative-unit-scoped directory role, an app permission cannot be narrowed to a
  subset of the directory. The only real levers are therefore: grant **as few scopes as
  possible**, use a **dedicated app registration** for this worker alone, and let the
  **process-side gate** (a fail-closed check on a test-object naming convention, as in
  [`examples/account-bestellung`](../examples/account-bestellung/)) be the runtime
  boundary.
- **`Directory.ReadWrite.All` is the wrong grant.** Many walkthroughs reach for it; it is
  the read/write-everything permission over the directory and the worker never needs
  it. If it is present on the app, remove it — the narrow per-object permissions below are
  sufficient.

## Operation → minimal application permission

Derived from the Graph request each operation issues (`connector/entra/offload.go`,
`request()`):

| Operation(s) | Graph request | Minimal application permission |
|---|---|---|
| `create-user`, `update-user`, `delete-user`, `enable`, `disable`, `reset-password` | `POST` / `PATCH` / `DELETE /users/{id}` | **`User.ReadWrite.All`** |
| `get-user`, `list-users`, `delta-users` | `GET /users[/delta]` | `User.Read.All` (subset of `User.ReadWrite.All`) |
| `assign-license` | `POST /users/{id}/assignLicense` | `User.ReadWrite.All` — no `Organization.Read.All` needed, the worker sends explicit `skuId`s rather than enumerating SKUs |
| `add-group-member`, `remove-group-member` | `POST` / `DELETE /groups/{id}/members` | **`GroupMember.ReadWrite.All`** (tighter) or `Group.ReadWrite.All` |
| `create-group`, `update-group`, `delete-group`, `add-group-owner`, `remove-group-owner` | `POST` / `PATCH` / `DELETE /groups[/{id}/owners]` | **`Group.ReadWrite.All`** |
| `get-group`, `list-groups`, `delta-groups` | `GET /groups[/delta]` | `Group.Read.All` (subset of `Group.ReadWrite.All`) |
| `create-team` | `PUT /groups/{id}/team` | `Group.ReadWrite.All` (or `Team.Create`) |
| `add-team-member`, `add-team-owner` | `POST /teams/{id}/members` | `TeamMember.ReadWrite.All` |
| `create-channel` | `POST /teams/{id}/channels` | `Channel.Create` |
| `archive-team` | `POST /teams/{id}/archive` | `TeamSettings.ReadWrite.All` |
| `assign-role` | `POST /roleManagement/directory/roleAssignments` | `RoleManagement.ReadWrite.Directory` |

Read-only deployments (a re-certification report, a directory export, a delta sync that
only observes) need only the `*.Read.All` variants — grant the `ReadWrite` ones only for
the flows that write.

## The common case: a user-and-group lifecycle

A joiner/mover/leaver or self-service-provisioning process — create and update users,
enable/disable, delete, list and delta-read, manage group membership, create groups,
assign licences — is covered by exactly two permissions:

- **`User.ReadWrite.All`**
- **`Group.ReadWrite.All`**

If such a process never creates or deletes groups but only manages membership, tighten
`Group.ReadWrite.All` to **`GroupMember.ReadWrite.All`**. Everything else in the table —
Teams operations, directory-role assignment — is opt-in: grant those permissions only
when a process actually uses those operations, and drop them otherwise.

## Applying it in the Entra admin center

1. **App registrations →** your worker's app **→ API permissions**.
2. Remove every permission except the ones your processes use (see the table). In
   particular remove `Directory.ReadWrite.All` / `Directory.Read.All` if present.
3. **Add a permission → Microsoft Graph → Application permissions**, select the minimal
   set (for the common case: `User.ReadWrite.All`, `Group.ReadWrite.All`).
4. **Grant admin consent** — application permissions take effect only once consented.
5. Check the app's service principal holds **no directory role** it does not need
   (Entra → Roles and administrators).

## Two caveats operators miss

- **Passwords and privileged accounts.** `User.ReadWrite.All` is enough for ordinary
  accounts, but **resetting a password or modifying an admin/role-bearing account**
  additionally requires the app to hold a directory role (e.g. *User Administrator*) — and
  even then it cannot act on holders of higher-privileged roles. Use `reset-password` only
  against ordinary accounts and no directory role is needed, which matches the
  test-object boundary the example processes enforce.
- **The client secret.** Keep it in the Atlas vault (Console), not in long-lived argv or
  plaintext environment, and rotate it. The worker reads it from
  `ATLAS_ENTRA_<NAME>_CLIENT_SECRET` (or `_CLIENT_SECRET_REF` for a vault key).

## See also

- [ADR-0172 — Entra ID connector](adr/0172-entra-id-connector.md) — why the Worker Type
  is worker-only and the credential never enters the engine. It predates
  [ADR-0203](adr/0203-worker-execution-model.md) and says *connector* throughout.
- [`examples/account-bestellung`](../examples/account-bestellung/) — a fail-closed,
  test-object-gated provisioning flow, the process-side boundary this page refers to.
- [MIM comparison](comparisons/mim.md) — where this Worker Type sits against the MIM
  management-agent surface.
