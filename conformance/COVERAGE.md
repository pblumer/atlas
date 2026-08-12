# BPMN conformance coverage

Generated from the register in `scenario.go` by `go test ./conformance -update`. Do not edit by hand.

## Features

| Feature | Patterns | Scenarios | Covered |
|---------|----------|-----------|---------|
| None start and end events | — | sequence | ✅ |
| Sequence flow between activities | WCP-1 | linear-independent, sequence | ✅ |
| Inline FEEL script task (in-engine, no worker) | — | exclusive-gateway, linear-independent, parallel-independent, sequence | ✅ |
| Data-based exclusive gateway with default flow | WCP-4, WCP-5 | exclusive-gateway | ✅ |
| Parallel fork and synchronizing join | WCP-2, WCP-3 | parallel-independent | ✅ |
| User task (human-completed job) | — | user-task | ✅ |
| Service task (worker-completed job with outputs) | — | service-task | ✅ |
| Intermediate message catch event | — | message-catch | ✅ |
| Intermediate timer catch event | — | timer-catch | ✅ |
| Receive task (message wait as an activity) | — | receive-task | ✅ |
| Interrupting boundary timer event | — | boundary-timer-interrupting | ✅ |
| Non-interrupting boundary message event | — | boundary-message-noninterrupting | ✅ |
| Event-based gateway (deferred choice) | WCP-16 | event-gateway-message, event-gateway-timer | ✅ |
| Message start event | — | message-start | ✅ |
| Timer start event | — | timer-start | ✅ |
| Job failure raises an incident; resolve resumes it | — | incident | ✅ |
| Interrupting boundary error event | — | boundary-error | ✅ |
| Signal throw and catch (1:n broadcast) | — | signal-throw-catch | ✅ |

## Control-flow patterns

| Pattern | Realized by | Covered |
|---------|-------------|---------|
| WCP-1 Sequence | sequence-flow | ✅ |
| WCP-2 Parallel Split | parallel-gateway | ✅ |
| WCP-3 Synchronization | parallel-gateway | ✅ |
| WCP-4 Exclusive Choice | exclusive-gateway | ✅ |
| WCP-5 Simple Merge | exclusive-gateway | ✅ |
| WCP-16 Deferred Choice | event-based-gateway | ✅ |

## Gaps

None — every registered feature and pattern has a covering scenario.

## Negative models (rejected at compile)

| Model | Why it must be rejected |
|-------|-------------------------|
| neg-dangling-flow | a sequence flow targets an element that does not exist |
| neg-boundary-bad-host | a boundary event attaches to a host that does not exist |
| neg-unknown-message | a receive task references a message that is not declared |
