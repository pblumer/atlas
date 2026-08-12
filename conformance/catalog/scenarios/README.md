# Scenario pages

Auto-generated index of the conformance scenarios. Each page carries the model's
description, its diagram, how the instance is driven, and the outcome Atlas must
produce. Regenerate with `go test ./conformance -update`; do not edit by hand.

## Positive scenarios (30)

| Scenario | Features | Patterns |
|---|---|---|
| [sequence](sequence.md) | `start-end-event`, `sequence-flow`, `script-task` | WCP-1 |
| [exclusive-gateway](exclusive-gateway.md) | `exclusive-gateway`, `script-task` | WCP-4, WCP-5 |
| [parallel-independent](parallel-independent.md) | `parallel-gateway`, `script-task` | WCP-2, WCP-3 |
| [linear-independent](linear-independent.md) | `sequence-flow`, `script-task` | WCP-1 |
| [user-task](user-task.md) | `user-task` | — |
| [service-task](service-task.md) | `service-task` | — |
| [message-catch](message-catch.md) | `message-catch` | — |
| [timer-catch](timer-catch.md) | `timer-catch` | — |
| [receive-task](receive-task.md) | `receive-task` | — |
| [boundary-timer-interrupting](boundary-timer-interrupting.md) | `boundary-timer-interrupting` | — |
| [boundary-message-noninterrupting](boundary-message-noninterrupting.md) | `boundary-message-noninterrupting` | — |
| [event-gateway-message](event-gateway-message.md) | `event-based-gateway` | WCP-16 |
| [event-gateway-timer](event-gateway-timer.md) | `event-based-gateway` | WCP-16 |
| [message-start](message-start.md) | `message-start` | — |
| [timer-start](timer-start.md) | `timer-start` | — |
| [incident](incident.md) | `incident` | — |
| [boundary-error](boundary-error.md) | `boundary-error` | — |
| [signal-throw-catch](signal-throw-catch.md) | `signal` | — |
| [subprocess](subprocess.md) | `embedded-subprocess` | — |
| [multi-instance](multi-instance.md) | `multi-instance` | — |
| [multi-instance-sequential](multi-instance-sequential.md) | `multi-instance-sequential` | — |
| [call-activity](call-activity.md) | `call-activity` | — |
| [compensation](compensation.md) | `compensation` | — |
| [signal-boundary](signal-boundary.md) | `signal-boundary` | — |
| [inclusive-gateway](inclusive-gateway.md) | `inclusive-gateway` | WCP-6, WCP-7 |
| [signal-start](signal-start.md) | `signal-start` | — |
| [data-object](data-object.md) | `data-object` | — |
| [data-object-fields](data-object-fields.md) | `field-level-data-object` | — |
| [collection-data-object](collection-data-object.md) | `collection-data-object` | — |
| [transaction-cancel](transaction-cancel.md) | `transaction-cancel` | — |

## Negative models (4)

Models that must be **rejected at compile** — the suite asserts the engine refuses
them rather than executing something ill-defined.

| Model | Why it is rejected |
|---|---|
| [`neg-dangling-flow.bpmn`](../../models/neg-dangling-flow.bpmn) | a sequence flow targets an element that does not exist |
| [`neg-boundary-bad-host.bpmn`](../../models/neg-boundary-bad-host.bpmn) | a boundary event attaches to a host that does not exist |
| [`neg-unknown-message.bpmn`](../../models/neg-unknown-message.bpmn) | a receive task references a message that is not declared |
| [`neg-terminate-end.bpmn`](../../models/neg-terminate-end.bpmn) | a terminate end event is unsupported and must not silently degrade to a plain end |
