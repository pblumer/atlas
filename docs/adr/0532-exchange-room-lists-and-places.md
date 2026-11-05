# ADR-0532: Rooms Outlook can find — room lists and Places metadata in the Exchange Worker

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-05
- **Deciders:** Atlas maintainers
- **Open question:** Which Exchange Online management role the classic `Set-Place` needs
  for an app-only session. Microsoft's reference for `Set-Place` names none and points to
  the generic permissions lookup; its page for the successor `Set-PlaceV3` names
  `TenantPlacesManagement`, `MailRecipients` or a Global Administrator. The record
  assumes the *Mail Recipients* role, which the Exchange Recipient Administrator and
  Exchange Administrator roles the Worker's setup already assigns include. Not yet run
  against a tenant.
- **Question checked:** 2026-11

## Context and problem statement

A meeting room ordered through the example `examples/m365-dienste` («Sitzungszimmer
(Raumpostfach)») was bookable by its address but did not appear in Outlook's Room Finder
("Browse all rooms", the location suggestions). The provisioning created the room mailbox,
set its calendar processing and wrote the location into `CustomAttribute4` only.

Microsoft documents what Room Finder needs
([Configure rooms and workspaces for Room Finder](https://learn.microsoft.com/en-us/microsoft-365-apps/outlook/calendaring/configure-room-finder-rooms-workspaces)):

- Rooms are listed through **room lists**, distribution groups created with
  `New-DistributionGroup -RoomList`, whose members are the room mailboxes. A room in no
  room list is not browsable.
- Room Finder filters by the room's **Places metadata**, set with `Set-Place`: *City*,
  *Floor* and *Capacity* above all, and *FloorLabel*, *Building*, *Tags* and the address
  beside them. It shows room lists as the values of its *Building* filter.
- Changes take 24 to 48 hours to appear.

The Exchange Worker (ADR-0462) could not do either as a named operation:
`create-distribution-group` does not take the `RoomList` switch, and no row ran
`Set-Place`. `Get-DistributionGroup -RecipientTypeDetails RoomList` was already reachable
through `list-distribution-groups`, which declares `RecipientTypeDetails`.

[`Set-Place`](https://learn.microsoft.com/en-us/powershell/module/exchangepowershell/set-place)
"is available only in the cloud-based service": Exchange Server has no Places, and in a
hybrid organization a synchronised room's city and address are written on premises
(`Set-User`, `Set-Mailbox`).

## Decision drivers

- An operation names what it means and adds the encoding Exchange needs (ADR-0462): a
  model should not author `RoomList: true` on a generic group call.
- An operation a target cannot run is refused before anything is sent, with what to do
  instead, as the remote-mailbox operations are on Exchange Online.
- The parameter list of an operation that changes something is closed unless the cmdlet
  is too wide to list.

## Considered options

1. Add `RoomList` to the optional parameters of `create-distribution-group`, and run
   `Set-Place` through `update-mailbox`-style open operation.
2. Named operations: `create-room-list` (the switch set by the operation), `set-place`
   and `get-place` with a closed parameter list, Exchange Online only.
3. Leave both to a PowerShell script task.

## Decision outcome

Chosen option: **2**, because it is how every other intent of the Worker is said, and
because a room list is a group with a different meaning (rooms, not people), not a group
with one more flag.

- `create-room-list` — `New-DistributionGroup` with `RoomList` set by the operation, on
  both targets. Required `Name`; optional `DisplayName`, `Alias`, `PrimarySmtpAddress`,
  `ManagedBy`, `Members`, `Notes`, `OrganizationalUnit`. The join, depart and sender
  options of `create-distribution-group` are not offered: they are about people. Members
  are added and removed with the existing group-member operations; an existing room list
  is found with `list-distribution-groups` and `RecipientTypeDetails` `RoomList`, and the
  room lists a room is in with the `Filter` `Members -eq '<distinguished name>'`.
- `set-place` — `Set-Place`, Exchange Online only. Required `Identity`; at least one of
  `Capacity`, `City`, `Building`, `Floor`, `FloorLabel`, `Label`, `Tags`,
  `CountryOrRegion`, `State`, `Street`, `PostalCode`, `GeoCoordinates`, `Phone`,
  `IsWheelChairAccessible`, `AudioDeviceName`, `DisplayDeviceName`, `VideoDeviceName`,
  `MTREnabled`. `ParentId` is left out: Microsoft marks it experimental and for
  organizations onboarding to Microsoft Places, whose `Set-PlaceV3` (a separate module)
  manages that hierarchy. A value that evaluates to null is left out, as on every
  operation, so a standort without a floor sets no floor.
- `get-place` — `Get-Place -Identity`, an object, Exchange Online only.
- On an Exchange Server Worker both Places operations are refused before the call, with
  the message that city and address are properties of the account there.

### The example

`examples/m365-dienste` uses them in a called process `proc_m365_raumliste`
(`raumliste.bpmn`): it splits the standort («Bern, 5. Stock») into city and floor, names
the room list with the naming rule M365Namensregel (objekttyp `raumliste`:
`<NAME_PREFIX>RL-<Ort>`, alias `<NAME_PREFIX>rl-<ort>`), creates it when no room list has
that alias, adds the room unless it already is a member, and sets the Places metadata
when the application constant `ORTSANGABEN` is true. The provisioning calls it, the
return removes the room from every room list it is in, and a new process
`proc_m365_raum_nachfuehren` calls it for every room mailbox the catalogue created, which
is how rooms provisioned before this change are repaired. The on-premises copy of the
example sets `ORTSANGABEN` to false.

A correction of a position's details («Angaben ändern», ADR-0359) does not start a
process — it records the correction, and the product has no lifecycle process whose
change action could. So the repair is the separate process, reading the standort and
capacity from the room mailbox itself (`CustomAttribute4`, `ResourceCapacity`).

### Consequences

- **Positive:** rooms the catalogue creates appear in Room Finder; one model runs against
  either target, with the Places step switched off where it cannot run.
- **Negative / trade-offs accepted:** three more rows in the operation table and its
  Modeler copy. `set-place` cannot reach the Microsoft Places hierarchy (`ParentId`,
  buildings and floors as objects).
- **Follow-ups / risks to watch:** the open question above; whether the
  `Members -eq` filter on `Get-DistributionGroup` answers immediately after an
  `Add-DistributionGroupMember` in every tenant (the repair process is idempotent and may
  simply be run again).

## Pros and cons of the options

### Option 1 — a switch on the group operation, an open Places operation
- Good: no new rows.
- Bad: a model would author the encoding the operation exists to add; an open
  `Set-Place` would accept whatever Microsoft adds to it without review.

### Option 2 — named operations
- Good: the intent is named; the Exchange Server refusal says what to do instead.
- Bad: a closed list must be extended when a model needs a parameter it leaves out.

### Option 3 — a PowerShell script task
- Good: nothing to add to the Worker.
- Bad: the credential and the session would be the script's, which is what the Exchange
  Worker exists to avoid (ADR-0462).

## Links

- amends ADR-0462 — the Exchange Worker's operation table
- relates to ADR-0359 — why a correction of a position's details starts no process
