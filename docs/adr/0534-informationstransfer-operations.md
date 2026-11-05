# ADR-0534: Worker operations for transferring resources between two accounts

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-05
- **Deciders:** Patrick Blumer (maintainer)
- **Open question:** The Entra operations restore-user and permanently-delete-user, the 404 Graph answers for an id outside the recycle bin, and the Exchange Server operations disable-mailbox and restore-mailbox-content follow Microsoft's documentation; none of them has yet been run against a tenant or an Exchange Server. Nor is it verified whether list-user-groups returns the groups whole under GroupMember.Read.All instead of Directory.Read.All.
- **Question checked:** 2026-11

## Context and problem statement

The example "Informationstransfer" moves what belongs to a person from a source account
to a target account: the Active Directory account, its Exchange mailbox and its Entra ID
identity. Three Worker Types take part, and each lacked something the transfer needs:

- **Entra ID.** The transfer copies the source account's group memberships, but no
  operation read the groups one user is a member of. And once the source account is
  deleted (`delete-user`, which puts it in the recycle bin), nothing could take it back
  out if the transfer is rolled back, or remove it for good when it is finished.
- **Exchange Server.** The content of the source mailbox has to reach the target mailbox.
  On Exchange Server that is two steps: disconnect the source mailbox (`Disable-Mailbox`),
  then copy the disconnected content into the target (`New-MailboxRestoreRequest`).
  Neither was an operation.
- **Active Directory.** A search or a sync returned every attribute through go-ldap's
  string values. `objectGUID`, `objectSid` and `mS-DS-ConsistencyGuid` are bytes, so a
  process received them as broken text it could neither compare nor pass on — yet base64
  of `objectGUID` is the account's `onPremisesImmutableId` in Entra ID, the anchor the
  transfer needs to match the AD account to its cloud identity (hard match).

## Decision drivers

- Each step of the transfer is one named, checked operation, not a hand-authored Graph
  call or cmdlet, so a model says what it means and the deploy refuses what cannot run.
- A step that records a fact must not report one that did not happen: a final deletion
  of an id that was never in the recycle bin is a wrong fact, not a no-op.
- What a target cannot do is refused before anything is sent, with what to do instead,
  as the remote-mailbox and Places operations already are (ADR-0462, ADR-0532).
- A change of result shape stays as small as the problem: only the attributes the
  transfer needs change.

## Considered options

1. A REST task against Graph and a `run-get-cmdlet`-style escape hatch for the writes.
2. New operations on the existing Worker Types, wired like their neighbours.
3. A dedicated "transfer" Worker Type that performs the whole move.

## Decision outcome

Chosen option: **2, new operations on the existing Worker Types**, because each step is a
small variation of an operation the Worker Types already have (a listing, a recycle-bin
call, an Exchange Server-only cmdlet), and the compiler, the Modeler and the handbook
already know how to present that. A REST task would put Graph's paging and the recycle
bin's semantics in the diagram; a transfer Worker Type would hide three systems behind
one task that could not be retried step by step.

The operations, as built:

- **Entra `list-user-groups`** — `GET /users/{id}/memberOf/microsoft.graph.group`. The
  *direct* memberships, not `transitiveMemberOf`: the transfer copies what somebody
  granted, and a nested group's grant follows from that group. It is a listing like
  `list-groups` (every page followed, `maxUsers` bound, `filter`, `search` and `select`
  allowed), and it always runs as an advanced query, because Microsoft documents
  `ConsistencyLevel: eventual` and `$count` as required for the cast; `advancedQuery="false"`
  on it is refused at deploy. Microsoft names `Directory.Read.All` as the least privileged
  application permission.
- **Entra `restore-user`** — `POST /directory/deletedItems/{id}/restore`, no body, the
  restored user as an optional result. Microsoft: within 30 days; a formerly synchronised
  user comes back cloud-managed.
- **Entra `permanently-delete-user`** — `DELETE /directory/deletedItems/{id}`, no body.
  Both recycle-bin operations address `/directory/deletedItems`, never `/users`, so an
  active account cannot be reached through them: its id is Graph's `404`, which fails the
  job. Microsoft names `User.DeleteRestore.All` as the least privileged permission for
  both.
- **Exchange `disable-mailbox`** — `Disable-Mailbox -Identity`, Exchange Server only. In
  Exchange Online a mailbox follows its account and the account's licence, and there is no
  such step for a user mailbox; the operation is refused there with that sentence.
- **Exchange `restore-mailbox-content`** — `New-MailboxRestoreRequest`, Exchange Server
  only, area mailbox, returning the request object. Required `SourceDatabase`,
  `SourceStoreMailbox`, `TargetMailbox`; optional `Name`, `TargetRootFolder`,
  `TargetIsArchive`, `IncludeFolders`, `ExcludeFolders`, `BadItemLimit`,
  `AcceptLargeDataLoss`, `BatchName`. `AllowLegacyDNMismatch` is fixed to true: the target
  is by design a different mailbox than the one disconnected, which Exchange refuses
  without it. Exchange Online restores only a soft-deleted or inactive mailbox, which needs
  the source account deleted, so the operation is refused there; between two active
  accounts the content is moved by people. The request's progress is read with
  `run-get-cmdlet` and `Get-MailboxRestoreRequest`, so there is no read operation of its
  own.
- **AD binary attributes** — a search and a sync answer `objectGUID`, `objectSid` and
  `mS-DS-ConsistencyGuid` (any case) as standard base64 of the raw bytes. The mock
  directory keeps raw bytes as AD does (an LDIF seed's `objectGUID::` line decodes to them)
  and answers, and shows in the Console, through the same encoding. The form lookup keeps
  refusing `objectGUID`; it is unchanged.

### Consequences

- **Positive:** the transfer is a sequence of retryable, deploy-checked tasks; a final
  deletion is reported only when one happened; an AD account's anchor reaches a process in
  the one format Entra ID compares.
- **Negative / trade-offs accepted:** a model that read `objectGUID` or `objectSid` from a
  search before now receives base64 instead of the bytes it got as text — a change of shape,
  accepted because the old value could not be used for anything. `list-user-groups` reads
  with eventual consistency, so a membership granted a moment ago may be missing. The write
  path is not converted: a model that writes `mS-DS-ConsistencyGuid` still sends the text it
  authored.
- **Follow-ups / risks to watch:** run the recycle-bin operations and the two Exchange
  Server operations against a tenant and a server (the open question above). Microsoft
  documents that a disabled mailbox is marked as disconnected only a short time after
  `Disable-Mailbox` (`Update-StoreMailboxState` speeds it up), so a restore immediately
  afterwards can fail and belongs in a retry.

## Pros and cons of the options

### Option 1 — REST task and escape hatch
- Good: no code.
- Bad: Graph paging, the advanced-query header and the recycle bin's 404 become the
  model's problem; the escape hatch is read-only by design (ADR-0462), so the Exchange
  writes would need a new write hatch.

### Option 2 — new operations
- Good: one row per operation in tables the compiler, the worker and the Modeler already
  share; the neighbours' tests hold the new rows too.
- Bad: three more Entra operations and two more Exchange operations to document.

### Option 3 — a transfer Worker Type
- Good: one task in the diagram.
- Bad: three systems behind one job that cannot be retried or rolled back step by step,
  and a credential bundle that can write to all of them.

## Links

- amends ADR-0172 (Entra ID Worker), ADR-0462 (Exchange Worker), and the AD Worker's
  result shape
- relates to ADR-0532 (Exchange Online-only operations refused on Exchange Server)
