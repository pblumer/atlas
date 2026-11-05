# ADR-DRAFT: Device Workers — GPIO, buses, displays and inputs of the physical world

- **Status:** Accepted (amended 2026-11-05: a device is adopted in the Console and fetches its configuration itself, see «Adoption»; the slices are reordered to the owner's delivery order)
- **Implementation:** Partial
- **Date:** 2026-11-05
- **Deciders:** Atlas maintainers
- **Open question:** How `atlas worker` behaves on the smallest Linux target (Pi Zero W,
  ARMv6, 512 MB) — resident memory, TLS long-poll cost, start time — and whether a
  MicroPython client on a Pico 2 W can hold a TLS long-poll against the job protocol
  within its RAM. Neither is measured; the tier boundaries below rest on that gap.
  Also unsettled: whether a maintained pure-Go driver exists for the first reference
  e-paper panel, or whether Atlas writes and keeps one; refresh limits are taken from
  the panel's data sheet, not verified on hardware.
- **Question checked:** 2026-11

## Context and problem statement

Atlas should address IoT automation as an audience: a process that switches a relay,
opens a valve, drives a motor or a LED, shows a status on a small display — an
e-paper HAT on a Pi Zero is the first case asked for — reads a sensor, and is started or continued
by something that happens in the physical world — a button, a door contact, a
temperature crossing a threshold.

What exists today:

- Release builds for `linux_arm64` and `linux_arm` (ARMv6, 32-bit Raspberry Pi OS).
  A stripped ARMv6 build of `./cmd/atlas` measured 65.8 MB on 2026-11-05.
- `atlas worker` is a client of the HTTP API that long-polls
  `POST /api/v1/jobs/activate` and settles a job through `complete`, `fail` or
  `error` (ADR-0007, ADR-0157). It connects outbound, so a device behind NAT needs no
  open port. `--handle type=command` already lets any program work a model-authored
  job type: GPIO can be driven today by a script, with no engine change.
- A worker that names the Workers it holds in `connectors` is leased only their jobs
  (ADR-0168, `handleActivateJobsByType`). Two devices of one Worker Type therefore do
  not take each other's jobs, provided each is its own Worker.
- `POST /api/v1/messages` correlates a message to a waiting catch event or starts a
  process at a message start event (ADR-0020, ADR-0035). With `timeToLiveMs` and
  `messageId` a retry within the lifetime is a no-op (ADR-0370).
- The `worker` token scope reaches the job protocol and nothing else. It does **not**
  reach `POST /api/v1/messages`; a device that should raise events needs a broader
  token today.

What is missing is not the transport but the product: no typed operations a modeler
can pick, no place where a device's pins, safe states and inputs are configured, no
narrow credential for a device that sits physically exposed, and no rules for the
failure modes that only the physical world has.

The physical world changes three assumptions every existing Worker Type relies on:

1. **A failure is not only an incident.** A relay left closed when the network, the
   engine or the device fails does damage that no replay undoes. The event log holds
   the *order*, never the state of the output.
2. **At-least-once becomes visible.** A job re-leased after a lost lease runs again.
   Setting a level twice is harmless; a pulse that feeds an animal or opens a door
   twice is not.
3. **Inputs arrive at machine rate.** Every message is a durable event in the WAL. A
   sensor sampled at 10 Hz and published as messages would fill the log with data
   that belongs in a time-series store.

## Decision drivers

- Reach the smallest devices, not only a Pi that can host the whole server.
- Keep the engine boundary unchanged: every physical side effect happens in a Worker
  Instance after the job is durable (I2), nothing on the hot path (I1, I5).
- End-to-end acknowledgement: a job completes only after the device performed the
  effect, not after a broker accepted a message.
- A device credential that, when the device is stolen, cannot do more than that
  device was meant to do.
- A safe physical state that holds without Atlas.
- No cgo (ADR-0010); one binary for the server and the Linux worker (ADR-0011).

## Considered options

1. **Status quo: `--handle` with a script** (Python/gpiozero) per installation.
2. **A built-in Linux GPIO Worker Type** in Go, served by `atlas worker`.
3. **An MQTT bridge Worker Type**: Atlas publishes commands to a broker and turns
   subscribed topics into messages.
4. **A `device` Worker Type contract** with implementations per device tier, speaking
   the native job protocol end to end; MQTT added later for off-the-shelf devices.

## Decision outcome

Chosen option: **4 — a `device` Worker Type defined as a contract, implemented per
tier, over the existing job protocol**, accepted by the programme owner on 2026-11-05.
Delivery is tracked in the [Device Automation programme](../planning/device-automation-program.md).

### The contract

A **Worker** of type `device` is one physical device (`pi-gewaechshaus`,
`pico-tuer-nord`). Its configuration — not the BPMN model — holds what is wired to it:

- **Outputs:** name, pin or bus address, kind (digital, PWM), the **safe state**
  applied at start and on shutdown, and an optional **maximum on-duration** the device
  enforces itself.
- **Displays:** name, panel model (which selects the driver), bus and control pins,
  orientation, and the minimum interval between refreshes.
- **Inputs:** name, pin, edge, debounce, and the **message name** and correlation-key
  template a change publishes.

A model addresses outputs and inputs by name, never by pin number, so rewiring a
device does not redeploy a process.

Operations, first set:

| Operation | Effect | Idempotent |
| --- | --- | --- |
| `digital.write` | set a named output to a level | yes |
| `digital.read` | return the level of a named input or output | yes |
| `pulse` | set a level for a bounded duration, then return to the safe state | no — deduplicated by job key |
| `pwm.set` | set duty cycle and frequency | yes |
| `i2c.read` / `i2c.write` | read or write registers of a configured bus device | write depends on the target |
| `onewire.read` | read a configured 1-Wire sensor (e.g. DS18B20) | yes |
| `display.show` | draw a layout on a named display | yes — skipped when the content is unchanged |

SPI is needed by the display drivers and is implemented with them, but is not an
operation a model calls: a model that sends bytes to a panel controller is wiring
knowledge in the wrong place. UART follows when a concrete use asks for it.

### Displays

A display is an output whose state is a picture. Three properties of e-paper decide
how `display.show` behaves:

- **The picture survives power loss.** That is the point of e-paper, and its danger:
  a dead device keeps showing the last state as if it were current. The device
  therefore draws a "as of" time into every picture itself, and redraws a stale
  marker when it has lost the server for longer than its configured interval. This is
  the display's safe state, enforced on the device like an output's on-duration.
- **A refresh is slow and wears the panel.** A full refresh takes seconds and the
  data sheets state a minimum interval and periodic full refreshes against ghosting.
  The device hashes the rendered picture and skips a refresh that would show the same
  one — which also makes a re-leased job harmless — and honours the minimum interval
  by drawing the newest content when it is due. The job completes once the panel
  reports the refresh finished, not when the bytes were sent.
- **One screen, many writers.** Several process instances may address one display.
  The first slice gives the whole display one layout, last write wins; named regions
  that different processes own come later if use asks for them.

**What travels.** The model states a small declarative layout: text lines with size
and alignment, a QR code, a progress bar, one of a fixed set of icons. Its values may
be FEEL or a feelers template (ADR-0487); the server resolves them into plain values
when it leases the job, as for every Worker Type that runs on a worker (ADR-0168).
The device lays out and rasterises: on the Linux tier in Go with an embedded font
that covers Umlauts. A picture is never a process variable — a bitmap in a variable
would be written into the log with every change.

Rasterising on a microcontroller is the open part: MicroPython's built-in text
rendering does not cover Umlauts. Whether that tier rasterises with its own font or
receives a bitmap rendered for it on lease is decided in the microcontroller slice.

Panels differ by controller and each needs its own initialisation; "e-paper" is not
one driver. Atlas supports a named list of panel models, starting with one reference
panel, and a display whose model is not on the list is refused when the Worker is
configured rather than when a job reaches it.

### Tiers

| Tier | Device | What runs on it |
| --- | --- | --- |
| Edge server | Pi 4 / Pi 5 | `atlas serve` with the `device` Worker Type supervised locally; processes keep running without uplink |
| Linux worker | Pi Zero 2 W (arm64), Pi Zero W (ARMv6) | `atlas worker --connector device`, built in Go against `/dev/gpiochip*` (no sysfs GPIO, no cgo) |
| Microcontroller | Pico 2 W, ESP32 | a reference client in MicroPython speaking the same five HTTP calls; not the Atlas binary |

The job protocol is small enough for the third tier: `jobs/activate`, `complete`,
`fail`, `error`, and `messages`. A protocol conformance suite that any client can be
run against belongs to this decision, so a third-party firmware can claim the
contract rather than resemble it.

### Inputs become messages

A configured input publishes `POST /api/v1/messages` with the configured name, a
correlation key from its template, a `timeToLiveMs`, and a `messageId` of
*device id · boot id · counter*. The boot id keeps a counter reset by a reboot from
colliding with a message still alive in the buffer, which the deduplication would
otherwise drop as a retry.

The device filters at the edge: debounce, change detection, thresholds. Atlas
receives events that matter to a process, not samples. Telemetry belongs in a
time-series store; this record does not make Atlas one.

### A narrow device credential

A new token scope `device` reaches the job protocol routes of the `worker` scope and
`POST /api/v1/messages`, the latter limited to the message names the Worker's inputs
declare. A stolen device can then work its own jobs and raise its own events, and
nothing else. One token per device, revocable on its own.

### Adoption: a device fetches what it is

*Amendment, 2026-11-05, decided by the programme owner.* An operator picks a device in
the Console and gives it a Worker. "Deploying a Worker" onto a device therefore means
**assigning it a configuration**, never pushing software to it, and the device fetches
that configuration itself, over the same outbound connection it leases jobs on.

1. **One generic image per tier.** An SD-card image for the Linux tier, a firmware file
   for a microcontroller. It carries the server's address and a short-lived,
   single-use enrolment code, nothing else — no wiring, no long-lived credential.
2. **The device introduces itself.** At first start it presents the code and describes
   itself: model, serial, the GPIO chips and lines it has, the buses it found. The
   server keeps it as a *pending device* in a design-time store; nothing about it is a
   process event.
3. **An operator adopts it.** In the Console the operator names it and states its
   wiring — outputs, inputs, displays — which creates a Worker of type `device`. Only
   then does the server issue the device its own token, of scope `device`, bound to
   that one Worker. The device collects the token once, keeps it in a file only its
   account can read, and the enrolment code is spent.
4. **The device fetches its configuration** at start and whenever it changes. A new
   wiring is applied between jobs: every output goes to its safe state, the lines are
   released and claimed again as configured.
5. **Revocation is one action.** A lost or stolen device is revoked in the Console; its
   token stops working, and with it every job and message route it had.

**Pushing from the server was weighed and rejected.** It would need the server to reach
each device — through a home router or a site network it usually cannot — and to hold a
login (SSH) for every device, so one leaked server credential opens every device. A
microcontroller has no SSH at all. Fetching keeps every connection outbound and every
credential on the device it belongs to.

**Software is a separate matter.** Assigning a configuration never installs code. Updating
the worker or the firmware on many devices needs signed updates the device verifies
before it applies them, with a way back when one fails; that is its own slice and its
own record.

### Safety that holds without Atlas

- The device applies each output's safe state at start, at orderly shutdown, and
  when it loses the server for longer than a configured interval.
- The maximum on-duration is enforced on the device, not by a BPMN timer: the timer
  is the plan, the device limit is the guarantee.
- A `pulse` remembers the job keys it executed and completes a re-leased one without
  acting again. Across a reboot that memory is lost on the Linux tier only if it is
  kept in RAM; the implementation slice decides whether to persist it, and documents
  the case it leaves open.
- Long physical actions are modelled as `digital.write` on, a BPMN timer, and
  `digital.write` off — never as one job whose lease must outlast the action.

### What Atlas is not here

A supervisory controller: it decides *when* and *whether*. Regulation loops (PID,
motor control) stay on the device; long-poll, lease and batch latencies are
milliseconds to seconds and not bounded.

### Invariants

- I2: every effect runs in a Worker Instance on a job that is already durable; an
  input enters only through the existing message API.
- I1, I5: nothing new on the processor path; the operation set compiles to a reserved
  job type like every other Worker Type.
- I3, I4, I6: no new state, no new event kind in the first slices. Device presence
  and health are runtime topology, not process events (ADR-0203).

### Consequences

- **Positive:** one contract from the Pi 5 to a microcontroller; the BPMN model names
  outputs and inputs and stays independent of wiring and tier.
- **Positive:** end-to-end acknowledgement — a job completes after the effect, which a
  broker-first design cannot give.
- **Positive:** inputs reuse message correlation, buffering and idempotency that
  exist already.
- **Negative:** a microcontroller client is code Atlas maintains outside Go, with its
  own release and test path.
- **Negative:** off-the-shelf devices that speak only MQTT (Shelly, Tasmota) are not
  reached until the MQTT slice.
- **Follow-ups / risks:** the measurements in the open question; a separate,
  smaller edge binary would need its own record against ADR-0011; persisted pulse
  deduplication; rate limiting of messages per device token.

### Slices

The order below is the delivery order the programme owner set on 2026-11-05. The
`device` token scope was first in this record's draft; it moved behind the Linux
outputs because its message allowlist refers to the inputs a device Worker declares,
and that Worker is what the first slice creates. Outputs need nothing beyond today's
`worker` scope.

1. Linux tier: `digital.write`, `digital.read`, `pulse`, safe state and on-duration
   watchdog, tested against a fake gpiochip — then the compiler element, the reserved
   job type, the server's `device` kind and `atlas worker --connector device`, so a
   task drives an output.
2. Adoption: enrolment codes, pending devices, adopting one in the Console, the
   `device` token scope bound to its Worker, the device fetching its configuration —
   after a threat model for physically exposed credentials.
3. Inputs to messages (edges, debounce, `messageId`), with the scope's message-name
   allowlist.
4. `display.show` on the Linux tier for the reference e-paper panel: layout,
   rasteriser, content hash, refresh interval, stale marker.
5. Microcontroller reference client (Pico W class) and the protocol conformance suite.
6. `pwm.set`, `i2c.*`, `onewire.read`.
7. Edge server on a Pi: sizing measured, an example application, the handbook card.
8. Coupling an edge Atlas with a central one, on the cross-instance messaging that
   ADR-0373, ADR-0369 and ADR-0372 decide and that is not built yet.
9. People acting through a device (a button that approves), decided with the
   Identity-aware Execution programme.
10. Signed software updates for a fleet of devices, in their own record.
11. MQTT bridge for off-the-shelf devices, in its own record.

## As built

IOT1, first increment — the device core, not yet reachable from a model:

- `connector/device/catalog` — the operation table (`digital.write`, `digital.read`,
  `pulse`), the name rule for lines and the pulse bounds a deploy and a run share.
- `connector/device` — `Config` (chip, named outputs with `activeLow`, `safe` and
  `maxOnMs`, named inputs with `bias`), `Open`/`Write`/`Read`/`Pulse`/`Safe`/`Close`
  and `Execute` for a resolved job; outputs claimed already in their safe state, the
  watchdog, pulse deduplication by job key (in memory, 4096 keys), and `RefusedError`
  for a job no retry can fix.
- The GPIO character device's v2 ABI laid out by hand (`cdev.go`), because Go aligns
  64-bit fields to 4 bytes on 32-bit ARM and the kernel to 8; the layout and ioctl
  numbers are pinned by test. In CI the system call is exercised only against a file
  that refuses it. On hardware, `TestHardwareLineOnARealChip` (opt-in through
  `ATLAS_DEVICE_HW`) passed on 2026-11-05 on a Raspberry Pi Zero 2 W, Raspberry Pi OS
  Lite 64-bit, `gpiochip0` line 26 with nothing wired to it: both biases read as
  expected, an output claimed high read high, a second claim was refused with EBUSY,
  set and read-back agreed, and a Device claimed, drove and released the line.
- `FakeChip`, for the tests and for a mock mode of the worker.

IOT3, started ahead of its turn at the owner's request (2026-11-05), as the driver
under `display.show`:

- `connector/device/epaper` — the Waveshare 2.13inch panels V2, V3 and V4 by model name,
  their initialisation restated from Waveshare's own drivers and pinned line by line by
  test; a landscape `Frame` (250×122) that is a `draw.Image`, laid out as Waveshare's
  getbuffer lays it; text in the Go fonts, with Umlauts; the HAT link on the device
  core's `Chip` (reset 17, data/command 25, busy 24, power 18) and spidev (mode 0,
  4 MHz, writes of at most 4096 bytes); a busy wait bounded at 20 s.
- On hardware (2026-11-05, the reference Pi Zero 2 W with a Waveshare 2.13inch HAT
  Rev2.1): `waveshare-2in13-v3` drew the test picture; `waveshare-2in13-v2` left the
  panel blank with its busy line never rising, which is what a wrong generation looks
  like. The panel's model must therefore be configured, not guessed from its board.
  Measured with V3: initialisation 54 ms, a full refresh 2.73 s from the update command
  until the busy line fell; with the wrong V2 sequence the "refresh" returned after
  249 ms because the busy line never rose. A refresh that ends far sooner than a
  second is therefore a sign the panel did not run it.
  The picture on the glass matched the frame pixel for pixel as far as a photograph
  shows: the orientation marker top right, the outline, text in two sizes, and the
  Umlaut of "Grüezi" whole; black was solid, without ghosting of the earlier runs.
- Not yet: partial refresh, the content hash and refresh interval, the stale marker,
  and the layout a model states — the driver draws what it is given.

Not yet: the compiler element, the reserved job type, the Worker kind in the server,
`atlas worker --connector device`, the Modeler, the setup doc and the handbook card.

## Pros and cons of the options

### Option 1 — `--handle` with a script
- Good: works today, no engine change, the whole Python sensor ecosystem.
- Bad: no typed operations, no configuration in the Console, no narrow credential, no
  safety rules; every installation writes its own. Not a product for an audience.

### Option 2 — built-in Linux GPIO Worker Type only
- Good: typed, governed, in Go, smallest change that is a product.
- Bad: stops at devices that run Linux; the smallest devices are out of reach.

### Option 3 — MQTT bridge
- Good: reaches most off-the-shelf IoT devices at once; inputs come naturally.
- Bad: needs a broker; QoS acknowledges delivery to the broker, so a job completes
  before the relay switched unless a request/response pattern is added on top; a
  second transport whose durability Atlas does not own.

### Option 4 — `device` contract over the job protocol
- Good: end-to-end acknowledgement, one contract across tiers, reuses messages.
- Bad: the most work; a non-Go client to maintain.

## Links

- relates to [ADR-0203](0203-worker-execution-model.md) (Worker Type, Worker, Worker Instance)
- relates to [ADR-0007](0007-job-worker-protocol.md), [ADR-0157](0157-worker-processes-supervision-and-console.md), [ADR-0168](0168-connector-work-on-a-worker.md)
- relates to [ADR-0020](0020-message-correlation.md), [ADR-0035](0035-message-start-events.md), [ADR-0370](0370-durable-message-buffer.md)
- relates to [ADR-0487](0487-mail-templates.md) (feelers templates for display text)
- relates to [ADR-0010](0010-go-and-no-cgo.md), [ADR-0011](0011-single-binary-distribution-and-web-ui.md)
- relates to [ADR-0289](0289-worker-type-setup-in-the-panel.md), [ADR-0167](0167-released-connectors-ship-in-the-marketplace.md) (what a new Worker Type owes)
