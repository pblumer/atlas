// Package teamsphone provisions Microsoft Teams Phone voice apps — call queues and auto
// attendants — the resource accounts that answer their calls, and the phone numbers
// people and voice apps are reached at, on behalf of a BPMN service task (ADR-0463).
//
// # Why PowerShell
//
// Microsoft documents these objects only as cmdlets of the MicrosoftTeams PowerShell
// module; Microsoft Graph has no endpoint that creates, changes or removes a voice app
// or its resource account. So the work itself is PowerShell, and what this package adds
// is everything around it that a script task leaves to each model: a closed set of named
// operations the compiler validates at deploy, a credential that lives with the worker
// rather than in a model or on the host's certificate store, and an idempotency rule per
// operation written once.
//
// The PowerShell is fixed and ships inside Atlas (operations.ps1). A model never authors
// any of it; it chooses an operation and supplies values.
//
// # The two halves
//
// [Resolve] is the engine's half: it evaluates the authored values against the variables
// the task sees. [Run] is the worker's half: it normalizes those values into the shapes
// an operation takes, refuses what an operation cannot use, and hands the request to the
// [Client] registered under the task's worker name — a [Pwsh] client in production, a
// [MockTenant] when the worker runs in mock mode. Normalizing in [Run] rather than in each
// client is what makes the mock and the real tenant refuse the same requests.
//
// # Safe to repeat
//
// A job may run twice. Every "ensure" operation looks for what it would create before it
// creates it, and every "remove" operation answers removed=false for something already
// gone, so a retried job converges instead of creating a second queue or failing on the
// first run's success. ensure-call-queue also brings a queue it finds to the agents,
// authorized users and routing settings the model names, so running it again after a
// change changes the queue rather than reporting success over the old values; what the
// model does not name stays as the queue has it. list-free-phone-numbers changes nothing:
// it reads the numbers in the tenant's inventory nobody holds, for a person or a process
// to choose from before ensure-phone-number gives one out.
//
// # Refusals a process can catch
//
// A refusal a process may have a path for — an address that belongs to a person, an
// account of the other kind, a list that would be emptied — is a [Refusal] under one of
// [Codes]. [Run] throws it as a BPMN error when the task's process catches that code, and
// leaves it a failure otherwise, as ADR-0461 decided for script tasks.
package teamsphone
