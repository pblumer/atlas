// Package entra provisions identities in Microsoft Entra ID (formerly Azure AD)
// through the Microsoft Graph API on behalf of a BPMN service task (ADR-draft-entra-id-connector).
//
// # What it is for
//
// It is Entra's counterpart to the Active Directory connector (ADR-0166), and it
// exists for the same reason that one does. A process *could* reach Graph with the
// generic REST connector: it speaks HTTP and JSON, and ADR-0152 gave REST an OAuth2
// client-credentials grant. What it cannot do is say what the operation *means*.
// Disabling an account in Entra is a PATCH of `accountEnabled` to false; removing a
// group member is a DELETE of a `$ref` sub-resource whose URL nobody remembers. A
// modeler should pick "Disable account", not hand-author a URL and a JSON fragment
// per lifecycle step — and should not be able to get the fragment subtly wrong.
//
// # Worker-only
//
// Like the SQL connectors (ADR-draft-generic-sql-connector) and unlike everything built before them, this
// kind has no in-process handler: [ADR-0164] decided that new connector kinds are
// built worker-first. The tenant id, client id and client secret live in the
// worker's own environment, so the engine holds no Entra credential — which matters
// more here than almost anywhere, because an app registration with
// User.ReadWrite.All and Group.ReadWrite.All can create and disable accounts across
// the whole directory.
//
// [ADR-0168]'s split applies as it does everywhere else: [Resolve] is engine work
// (find the task detail, evaluate its FEEL against the instance's variables, read
// the attributes variable) and produces a [Job] of plain values; [Run] is worker
// work (resolve the connector name against the worker's own registry, call Graph).
// A [Job] has nowhere to put a client secret.
//
// [ADR-0164]: https://github.com/pblumer/atlas/blob/main/docs/adr/0164-no-in-process-service-tasks.md
// [ADR-0168]: https://github.com/pblumer/atlas/blob/main/docs/adr/0168-connector-work-on-a-worker.md
package entra
