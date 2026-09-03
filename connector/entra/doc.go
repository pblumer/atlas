// Package entra provisions identities in Microsoft Entra ID (formerly Azure AD)
// through the Microsoft Graph API on behalf of a BPMN service task (ADR-0172).
//
// # What it is for
//
// It is Entra's counterpart to the Active Directory worker (ADR-0166), and it
// exists for the same reason that one does. A process *could* reach Graph with the
// generic REST worker: it speaks HTTP and JSON, and ADR-0152 gave REST an OAuth2
// client-credentials grant. What it cannot do is say what the operation *means*.
// Disabling an account in Entra is a PATCH of `accountEnabled` to false; removing a
// group member is a DELETE of a `$ref` sub-resource whose URL nobody remembers. A
// modeler should pick "Disable account", not hand-author a URL and a JSON fragment
// per lifecycle step — and should not be able to get the fragment subtly wrong.
//
// # Listing, and who follows the pages
//
// [Ops] covers a joiner/mover/leaver lifecycle, and one operation in it is not a
// single call: list-users is a Graph *collection*, which is paged. Following
// @odata.nextLink is this package's work rather than a model's — a process looping
// over a continuation token would be carrying Graph's paging protocol in its
// diagram, which is the encoding this worker exists to keep out of one. So a
// result variable receives the whole listing as one array, and never a page of it.
//
// Two bounds keep that honest. maxUsers caps what may reach a process variable and
// *fails* when exceeded rather than truncating (a short result set is a wrong
// answer, not a partial one), and [maxListPages] ends a chain of pages that never
// does. A third bound is [GraphClient]'s: a continuation may only stay on the
// worker's own endpoint, so a redirected page cannot carry the directory-wide
// bearer to another host.
//
// # Advanced queries
//
// Graph gates endsWith, ne, not and $search behind *advanced query support*, which is
// two things that only work together: the ConsistencyLevel: eventual header and
// $count=true in the query. [Job.Advanced] sends both, so there is no way to author
// half of it and collect the 400 that follows.
//
// It is opted into rather than read out of the filter text — the filter may be a FEEL
// expression with nothing to read at deploy, and eventual consistency changes what
// the process is told about the directory. A [Job.Search] implies it, because Graph
// runs a $search no other way. And it rides on every page: [Request.Eventual] is part
// of the request the paging loop keeps, since Graph rejects a continuation fetched
// without the header that made the query legal.
//
// # Worker-only
//
// Like the SQL workers (ADR-0173) and unlike everything built before them, this
// kind has no in-process handler: [ADR-0164] decided that new Worker Types are
// built worker-first. The tenant id, client id and client secret live in the
// worker's own environment, so the engine holds no Entra credential — which matters
// more here than almost anywhere, because an app registration with
// User.ReadWrite.All and Group.ReadWrite.All can create and disable accounts across
// the whole directory.
//
// [ADR-0168]'s split applies as it does everywhere else: [Resolve] is engine work
// (find the task detail, evaluate its FEEL against the instance's variables, read
// the attributes variable) and produces a [Job] of plain values; [Run] is worker
// work (resolve the worker name against the worker's own registry, call Graph).
// A [Job] has nowhere to put a client secret.
//
// [ADR-0164]: https://github.com/pblumer/atlas/blob/main/docs/adr/0164-no-in-process-service-tasks.md
// [ADR-0168]: https://github.com/pblumer/atlas/blob/main/docs/adr/0168-connector-work-on-a-worker.md
package entra
