# Device Automation: delivery programme

**Programme:** IOT (Device Automation)  
**Baseline checked:** 2026-11-05, `main` (programme registration only; no runtime implementation claimed).  
**Roadmap owner:** [Milestone IOT](../../ROADMAP.md#milestone-iot--device-automation-).  
**Architecture authority:** [ADR-draft-device-workers-for-the-physical-world](../adr/draft-device-workers-for-the-physical-world.md) (**Accepted / Not started**).  
**Programme owner:** pblumer.  
**Reference device:** Raspberry Pi Zero 2 W (64-bit, `linux_arm64`; a stripped build measured 65.3 MB on 2026-11-05) with an e-paper HAT on the 40-pin header; the panel's exact model is still to be read off the HAT.

## Outcome and boundaries

A process modelled in Atlas acts on the physical world and is driven by it: it switches
a relay, a valve, a motor or a LED, shows a status on an e-paper display, reads a
sensor, and is started or continued by a button, a contact or a threshold. The same
model runs whether the device is a Raspberry Pi that hosts the server, a Pi Zero that
runs `atlas worker`, or a microcontroller that speaks the job protocol.

The demonstration that closes the programme's core (IOT1–IOT3): a Pi Zero with an
e-paper HAT and a relay, served by a central Atlas. A process switches the relay,
shows its state on the display, and continues when a button is pressed; pulling the
network cable leaves the relay in its safe state within the configured interval and
marks the picture as stale; reconnecting resumes without a second pulse.

**Non-goals.** Atlas is a supervisory controller: it decides *when* and *whether*.
Regulation loops (PID, motor control) stay on the device. Atlas is not a time-series
store: sensor samples are filtered on the device, and only events a process waits for
become messages. No arbitrary code is loaded into the engine (ADR-0203).

**Invariants.** Every physical effect runs in a Worker Instance on a job that is
already durable (I2); nothing new on the processor path (I1, I5); inputs enter through
the existing message API, so no new state and no new event kind in IOT1–IOT3 (I3, I4,
I6).

## Baseline

| Area | Verified reference | Status / limitation |
|---|---|---|
| Release builds for Raspberry Pi | `.github/workflows/release.yml`, `docs/install.md` | `linux_arm64` and `linux_arm` (ARMv6). A stripped ARMv6 build measured 65.8 MB on 2026-11-05; memory on a Pi Zero not measured |
| External worker over the HTTP job protocol | ADR-0007, ADR-0157, `worker/worker.go` | Outbound long-poll; `--handle type=command` already drives GPIO through a script, without typed operations or safety rules |
| Routing to one device | ADR-0168, `api/handlers.go` `handleActivateJobsByType` | A worker naming its Workers in `connectors` is leased only their jobs |
| Inputs as messages | ADR-0020, ADR-0035, ADR-0370 | `messageId` with `timeToLiveMs` makes a retry a no-op |
| Narrow device credential | `api/apitokenscope.go` | **Missing.** The `worker` scope does not reach `POST /api/v1/messages`; a device raising events needs a broader token today |

## Slices

| Slice | Status | Depends on | Closure gate |
|---|---|---|---|
| **IOT0 — Decision and threat model** | 🚧 ADR accepted, threat model open | — | Threat model for a physically exposed device credential: theft, revocation, message flooding, replay of a captured request |
| **IOT1 — Linux outputs** | 🚧 next | — | `device` Worker Type with `digital.write`, `digital.read`, `pulse`; outputs addressed by name; safe state at start and shutdown; on-duration watchdog on the device; pulse deduplicated by job key; tests against a fake chip; setup doc and handbook card; runs with the existing `worker` scope |
| **IOT2 — Inputs and device scope** | 🔲 | IOT0, IOT1 | Edges and debounce publish messages with a stable `messageId`; `device` scope limited to declared message names; rate limit per token; negative tests for undeclared names |
| **IOT3 — E-paper display** | 🔲 | IOT1 | `display.show` on one reference panel: layout resolved on lease, rasterised on the device, unchanged pictures skipped, refresh interval honoured, stale marker; no bitmap in a process variable |
| **IOT4 — PWM, I2C, 1-Wire** | 🔲 | IOT1 | `pwm.set`, `i2c.read`/`i2c.write`, `onewire.read` against configured devices |
| **IOT5 — Microcontroller client** | 🔲 | IOT1, IOT2 | MicroPython client for a Pico 2 W and a protocol conformance suite; measured TLS long-poll within the device's RAM |
| **IOT6 — Edge server on a Pi** | 🔲 | IOT1 | Measured sizing on Pi Zero W, Zero 2 W and Pi 5; example application; handbook |
| **IOT7 — Edge and central coupling** | 🔲 | IOT6 | New ADR first: what crosses between two engines and what each may do while the other is unreachable |
| **IOT8 — People acting through a device** | 🔲 | IOT2, IOT3 | Decided with the [IAE programme](identity-aware-execution-program.md): to whom a button press is attributed before a device may complete human work |
| **IOT9 — MQTT bridge** | 🔲 | IOT2 | Own ADR; a job completes on the effect, not on broker delivery |

### Why IOT1 comes first

The record's first draft put the `device` token scope first, as the smallest change.
It is not independent: the scope limits a device to the message names *its Worker's
inputs declare*, and that Worker configuration is what IOT1 creates. Outputs, on the
other hand, need nothing beyond today's `worker` scope. Building IOT1 first gives a
device that can be configured and tried on hardware, and gives IOT2 the configuration
its allowlist refers to.

### Dependency path

```text
IOT0 threat model ----------------------.
IOT1 Linux outputs --+--> IOT2 inputs + device scope --+--> IOT5 microcontroller
                     |                                  +--> IOT9 MQTT
                     +--> IOT3 e-paper ----+--> IOT8 people at a device (with IAE)
                     +--> IOT4 PWM/I2C/1-Wire
                     +--> IOT6 edge server --> IOT7 edge/central coupling
```

## Open questions

- Resident memory and TLS cost of `atlas worker` on a Pi Zero W (ARMv6, 512 MB).
- Whether MicroPython on a Pico 2 W holds a TLS long-poll within its RAM, and how it
  renders text with Umlauts.
- Whether a maintained pure-Go driver exists for the reference e-paper panel.
- Whether a separate, smaller edge binary is warranted; it would need its own record
  against ADR-0011.
