// Package teamsphone provisions Microsoft Teams Phone voice apps — call queues and auto
// attendants — and the resource accounts that answer their calls, on behalf of a BPMN
// service task (ADR-0463).
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
// first run's success.
package teamsphone
