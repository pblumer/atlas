// Package exchange administers Microsoft Exchange — Exchange Online and Exchange
// Server — on behalf of a BPMN service task (ADR-0462).
//
// # What it is for
//
// Exchange is administered through PowerShell cmdlets: Add-MailboxPermission,
// New-DistributionGroup, Set-MailboxAutoReplyConfiguration and some hundreds more. A
// process could reach them through a script task, and would then carry in its diagram
// the cmdlet, the encoding each one needs (full access is AccessRights FullAccess with
// InheritanceType All; an alias is a hashtable of Add), a connection to open and a
// credential to open it with. This package offers the cmdlets an administration process
// actually uses as named, checked operations — "grant full access", "add a distribution
// group member" — so a model says what it means and the Worker supplies the encoding.
// The operation table is [catalog.Ops]; this package is the call.
//
// # Two targets, one model
//
// A Worker is configured for one organization, and the organization decides the
// backend:
//
//   - Exchange Online is reached over [OnlineClient]: the REST endpoint the Exchange
//     Online PowerShell module itself sends every cmdlet to, with an app-only token for
//     an app registration that holds Exchange.ManageAsApp. No PowerShell runs on the
//     worker host.
//   - Exchange Server is reached over [OnPremClient]: Remote PowerShell to the server's
//     Microsoft.Exchange endpoint, opened by a pwsh the worker starts for each call.
//     Exchange Server offers no other supported way in.
//
// The operation is the same either way, and where the two need different cmdlets for
// one intent — Send As is a recipient permission online and an Active Directory
// permission on a server — the table holds both and the Worker's target picks. So a
// model moves from one to the other, or serves a hybrid organization, by naming a
// different Worker.
//
// # Worker-only
//
// Like Entra's, this kind has no in-process handler: an app registration with
// Exchange.ManageAsApp and an Exchange administrator role, or a service account in
// Organization Management, can read and change every mailbox in the organization, and
// there is no reason for the engine to hold either. [Resolve] is engine work (evaluate
// the parameter template against the instance's variables) and produces a [Job] of
// plain values with nowhere to put a credential; [Run] is worker work (find the
// Worker, build the cmdlet call for its target, run it).
//
// # What a result is
//
// A read of one object writes that object, or null when there is none — and fails when
// the identity matched several, rather than going on with whichever came first. A
// listing writes an array, bounded by the task's maxResults and failed rather than
// truncated past it. A change writes nothing, except a create or a grant, which may
// keep what it made — one object, or every entry when Exchange answered with several,
// since failing a change that was made would report it as not made. Objects are
// flattened to their properties, complex values to their text, which is the shape both
// backends can give.
package exchange
