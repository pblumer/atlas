// Package playground runs a BPMN model on the real Atlas engine inside a
// throwaway sandbox, on a clock the caller owns.
//
// It is the machinery behind the Modeler's Playground tab: an author points it at
// a draft (or a deployed version), feeds it cases, and watches what the process
// actually does — without deploying, without durable state, and without any
// possibility of an external side effect.
//
// # What a sandbox is
//
// A [Sandbox] is a complete engine in a box: its own partition, its own
// write-ahead log and state store in a temporary directory, and its own
// [engine.Processor]. It compiles the model with the real compiler and executes it
// on the real processor, so control flow, FEEL, DMN, gateways, boundary events and
// multi-instance behave exactly as they do in production — there is no second
// implementation to drift (the reason ADR-0030 rejected a browser-side simulator).
//
// Three things make it a playground rather than a second engine:
//
//   - A virtual clock. Simulated time is moved by the scheduler, never read from
//     the wall: it jumps to the next stub completion or the next due timer. A model
//     that waits three days finishes in microseconds.
//   - A stub policy ([StubSet]) supplied as *run configuration* rather than model
//     content, so the model under test is byte-for-byte the model that deploys.
//   - Isolation by absence: a sandbox registers no workers, no vault, no mail
//     transport and no HTTP client. A REST task cannot reach the network because
//     nothing in a sandbox could dial it. That is a structural property, not a
//     setting — see TestConnectorTaskIsAnsweredByTheStubAndNeverCalled.
//
// # What it deliberately is not
//
// Timings are *modelled*, not measured: a report is exactly as good as the stub
// durations someone configured. The sandbox answers "where does it pile up if the
// work takes this long", never "how fast is our endpoint".
//
// # Threading
//
// A Sandbox is owned by exactly one goroutine, like every other partition in Atlas
// (invariant I3). It carries no lock of its own; callers that serve concurrent
// requests put it behind a runloop.Loop, which is what [Session] does.
package playground
