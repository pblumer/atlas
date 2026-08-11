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

## Control-flow patterns

| Pattern | Realized by | Covered |
|---------|-------------|---------|
| WCP-1 Sequence | sequence-flow | ✅ |
| WCP-2 Parallel Split | parallel-gateway | ✅ |
| WCP-3 Synchronization | parallel-gateway | ✅ |
| WCP-4 Exclusive Choice | exclusive-gateway | ✅ |
| WCP-5 Simple Merge | exclusive-gateway | ✅ |

## Gaps

None — every registered feature and pattern has a covering scenario.
