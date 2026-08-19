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
| Embedded subprocess | — | subprocess | ✅ |
| Parallel multi-instance activity with output collection | — | multi-instance | ✅ |
| Sequential multi-instance activity | — | multi-instance-sequential | ✅ |
| Standard loop activity (repeat while a condition holds) | WCP-21 | standard-loop | ✅ |
| Call activity invoking a child process | — | call-activity | ✅ |
| Compensation via a boundary and a compensation throw | — | compensation | ✅ |
| Interrupting boundary signal event | — | signal-boundary | ✅ |
| Inclusive (OR) gateway split and synchronizing join | WCP-6, WCP-7 | inclusive-gateway | ✅ |
| Signal start event (broadcast births an instance) | — | signal-start | ✅ |
| First-class data object: output/input associations and data state | — | data-object | ✅ |
| Field-level data-object writes (accrue members) | — | data-object-fields | ✅ |
| Collection data object (isCollection list) | — | collection-data-object | ✅ |
| Transaction subprocess with cancel end and cancel boundary | — | transaction-cancel | ✅ |

## Control-flow patterns

| Pattern | Realized by | Covered |
|---------|-------------|---------|
| WCP-1 Sequence | sequence-flow | ✅ |
| WCP-2 Parallel Split | parallel-gateway | ✅ |
| WCP-3 Synchronization | parallel-gateway | ✅ |
| WCP-4 Exclusive Choice | exclusive-gateway | ✅ |
| WCP-5 Simple Merge | exclusive-gateway | ✅ |
| WCP-6 Multi-Choice | inclusive-gateway | ✅ |
| WCP-7 Structured Synchronizing Merge | inclusive-gateway | ✅ |
| WCP-16 Deferred Choice | event-based-gateway | ✅ |
| WCP-21 Structured Loop | standard-loop | ✅ |

## Gaps

None — every registered feature and pattern has a covering scenario.

## Negative models (rejected at compile)

| Model | Why it must be rejected |
|-------|-------------------------|
| neg-dangling-flow | a sequence flow targets an element that does not exist |
| neg-boundary-bad-host | a boundary event attaches to a host that does not exist |
| neg-unknown-message | a receive task references a message that is not declared |
| neg-loop-unbounded | a standard loop has neither a loop condition nor a loop maximum, so it could never end |
