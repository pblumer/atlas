# ADR-0481: A storage reserve, and an engine that halts on a failed write

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-24
- **Deciders:** Atlas maintainers
- **Open question:** Whether the default reserve of 1 GiB covers what the state store still writes after the server stops taking writes on a large store. It was derived from component sizes (two 16 MiB memtables, a manifest of up to 128 MiB, a 64 MiB WAL segment, a checkpoint's copied files, a compaction's output), not measured on a multi-GiB store under load, and a compaction's output grows with the store. Pebble ends the process when a manifest write fails (`Logger.Fatalf` in `version_set.go`); a reserve too small for that write turns a pause into a crash.
- **Question checked:** 2026-10

## Context and problem statement

An operator asked for the disk to be watched from the Console's dashboard: how much space there is, how much is used, and when it will run out. The answer to "can Atlas even read the capacity from inside its own process" is yes — one `statfs` (Linux, macOS, FreeBSD) or `GetDiskFreeSpaceExW` (Windows) call per file system, already in `golang.org/x/sys`. The harder question came out of asking it: what does Atlas do when the disk actually fills?

It was tried, on a 2–4 MiB tmpfs, with the WAL, and then the WAL and the state store, on it:

1. The write that hits `ENOSPC` is cut short. `wal.Log.Sync` returned the error but kept both the torn bytes in the segment and the staged batch in memory.
2. The processor left the failed batch's commands in its queue. The next run — any request at all — processed them again, so once space was freed, two instances whose creation had been answered with an error were created anyway (90 live where 88 had been acknowledged), and the WAL received the failed batch's events a second time.
3. The next batch was written behind the torn bytes. On restart the reader found damage with data after it, which no crash can leave, and refused to start (ADR-0283). **A server that had lived through a full disk could not be restarted without repairing the log by hand.**

The processor's own design document always had these failures stop it — its batch-cycle sketch calls `p.fatal(err)` when the append or the commit fails (`docs/architecture/processor.md`) — but the code returned the error to whichever request was running and carried on with the next.

Separately, Pebble ends the process with `Fatalf` when it cannot write its manifest, so the state store is not something a full disk may be allowed to reach at all.

The requirement the operator set is that no acknowledged data may be lost, whatever it costs — including freezing the server to read-only.

## Decision drivers

- No acknowledged write may be lost, and no write answered with an error may happen later anyway.
- A full disk must leave a server that restarts cleanly.
- Work the server has turned away must stay with the caller, who can retry; it must not be held in memory, where a crash would lose it.
- The guard must not cost the hot path anything (I1), must read nothing that grows with the data (ADR-0382), and must not touch state outside the single writer (I3).
- It has to work on the three platforms Atlas runs on: Linux containers (Docker, Kubernetes), Windows services, and bare hosts.
- A forecast without keeping a history.

## Considered options

1. **Display only** — show capacity and use, refuse nothing.
2. **Cache writes in memory while the disk is full** and apply them once space is back.
3. **A reserve below which new writes are refused up front**, with the WAL and the engine made safe for the case the reserve does not prevent.
4. **Exit the process on a failed write** and let the supervisor restart it.

## Decision outcome

Chosen option: **3, a reserve, with the failure path made safe underneath it**, because it is the only one that keeps every acknowledged write and never acknowledges one it cannot keep.

**The reserve.** A monitor (`api/storage`) reads every file system the server writes to — the data directory, `wal/` and `state/`, folded into one when they share a device — every five seconds, off the run loop. While less than the reserve is available on any of them, the server is *frozen*:

- Every request that is not a read is refused with **507 Insufficient Storage** and `Retry-After`, before it reaches the engine, so nothing is changed and the caller keeps what it sent. The check sits between the authentication layer and the route mux, and costs one atomic load per request until the server is actually frozen.
- A short allowlist stays open, each entry with its reason in `writesWhileFrozen`: sign-in and sign-out, the OAuth token endpoint, in-memory presence beacons, `POST /api/v1/checkpoints` (a checkpoint with compaction is how WAL space is given back), and the completion, failure and error reports of jobs already leased. The last group matters most: the work behind a report has already happened, and the bundled worker drops a refused report (`_ = w.complete(...)`), so refusing it would make the job run a second time once its lease ran out.
- The server starts no work of its own: the timer scheduler skips its ticks, the retention sweep and the inbound bridge pause, and the in-process workers lease nothing new. A due timer stays due and fires on the first tick after; an inbound source keeps what it has not delivered, because the cursor advances only past what was published.
- It resumes by itself once the reserve and a quarter of it are free again. No restart.

The default reserve is 1 GiB (`--storage-reserve-mb`, `ATLAS_STORAGE_RESERVE_MB`). It is sized from what still has to fit after the server stops: a state-store flush, a compaction's output, a rewritten manifest, a checkpoint's copied files, a WAL segment roll, and the requests already admitted when the reading was taken — roughly half a gibibyte, doubled. `0` keeps the monitor and its warnings and turns the freeze off. A file system smaller than four reserves is said once at start.

**The failure path.** The reserve narrows the window; it cannot close it — another process can fill a shared disk between two readings. So the case it misses is made safe on its own:

- `wal.Log.Sync` rolls a failed write back: it truncates the segment to where the batch began, puts the offset there, and drops the staged batch. If that truncation fails, or if an fsync fails — after which nothing can be known about what reached the disk — the log stops accepting batches until it is reopened (`Log.Err`). A rolled segment that could not be created stops it too. Nothing is ever written behind a torn batch.
- The processor **halts** once a batch could not be made durable: a failed sync, or a failure to commit state after the batch is in the log. Every later run returns `engine.ErrHalted` without touching the disk again (`Processor.Halted`, safe from any goroutine). The queue holds commands whose callers were told they failed; running them later would contradict that answer, and only a restart knows exactly what is durable, because recovery reads it from the log. Reads go on.
- A halted server answers every write with **503** until it is restarted, logs `event=engine.halted` once, and the dashboard says that a restart — not more space — is what it needs.

**The forecast.** Each file system carries two numbers: an exponentially weighted mean of its consumption rate, with a six-hour time constant, and how long it has been observed. The mean starts at zero and is divided by the weight it has accumulated, so the first hour is a plain average rather than the first reading's noise. A forecast is shown after an hour; it reaches the reserve within a week and the state is a warning. Nothing is stored, so it starts over on every restart. The same readings are exported as `atlas_storage_capacity_bytes`, `atlas_storage_available_bytes`, `atlas_storage_reserve_bytes`, `atlas_storage_frozen`, `atlas_storage_consumption_bytes_per_second` and `atlas_engine_halted`, so an installation with Prometheus can ask `predict_linear` over as long a window as its retention holds — the history lives in the system built to keep it.

### Consequences

- **Positive:** A full disk no longer damages the log, no longer executes refused work later, and no longer leaves a server that cannot restart. Below the reserve the server pauses instead of failing, and resumes without anybody restarting it. The dashboard, `GET /api/v1/storage`, the `atlas_storage` MCP tool and `/metrics` all report the reading the freeze was decided on.
- **Negative / trade-offs accepted:** A frozen server refuses some requests that write nothing, such as validation and preview endpoints that happen to be `POST`s; the allowlist is kept short because every entry spends reserve. A halted engine needs a restart even when the write that failed was cleanly rolled back — keeping it running would mean undoing in-memory state the failed batch already changed (keys, positions, the queue), and a restart is the one point at which that is known to be right. The first request that meets the full disk is answered 500, the ones after it 503. The reserve is 1 GiB of every disk; on a small volume that is a large share.
- **Follow-ups / risks to watch:** The numbers are only as true as the file system reports them: a network share may report its whole pool, a `hostPath` or `local-path` volume the node's disk, a thin-provisioned one more than exists. WAL compaction is still off by default (`--compact-wal`), so the WAL grows until an operator turns it on; the forecast makes that visible but does not change it. Pebble's `Fatalf` on a failed manifest write remains reachable if something else fills the disk faster than a reading — the open question above.

## Pros and cons of the options

### Option 1 — display only
- Good: no behaviour change; nothing to get wrong.
- Bad: leaves all three failures above in place. An operator who is not looking at the dashboard when the disk fills loses the ability to restart.

### Option 2 — cache writes in memory while full
- Good: the client never sees an error.
- Bad: a write acknowledged from memory is lost by the next crash, and a server short of disk is exactly the server most likely to crash. It breaks I2 (durable before visible). The data's durable copy while the disk is full is the one the caller still holds — so the caller has to be told no.

### Option 3 — reserve plus safe failure path (chosen)
- Good: nothing acknowledged is ever lost, nothing refused happens later; pauses and resumes on its own; works the same on every platform.
- Bad: a reserve has to be sized, and an estimate can be wrong; some harmless requests are refused while frozen.

### Option 4 — exit on a failed write
- Good: simple, and a restart does recover correctly once the WAL no longer writes behind torn bytes.
- Bad: on a disk that is still full the restart fails too, and a supervisor turns that into a crash loop that also takes reads away. It is what Pebble already does for its manifest, which is the reason for the reserve, not a model for the rest.

## As built

- `internal/diskspace` reads a directory's file system: `statfs` with `f_frsize` on Linux (as `df` and `statvfs` count), `f_bsize` on macOS and FreeBSD, `GetDiskFreeSpaceExW` and `GetVolumePathNameW` on Windows; every other platform reports `ErrUnsupported`, and the monitor then freezes nothing. `UsedFraction` is df's `used / (used + available)`, not `used / total`, which on a disk with a large root reservation reads nearly full while most of it is empty.
- `api/storage` holds the monitor, its state (`ok`, `warn`, `frozen`, `halted`, `unknown`), the forecast and `GET /api/v1/storage` (admin). A failed reading changes nothing: a frozen server stays frozen, an open one stays open, and the state reads `unknown`.
- `api/storageguard.go` builds the monitor, holds the allowlist and the admission check, and `writesPaused` for the background loops. `TestEveryWriteAFrozenServerTakesIsARoute` keeps the allowlist naming mounted routes.
- `wal.Log`: `abandon`, `Discard`, `Err`, and a closed log that answers `os.ErrClosed`. `engine`: `ErrHalted`, `Processor.Halted`.
- `state.Store.DiskSpaceUsage` reports the store's own account of its bytes from Pebble's metrics, without touching a file. Tables a checkpoint still links after the store compacted them away are not in it.
- Log events `storage.low`, `storage.frozen`, `storage.resumed`, `storage.unreadable`, `engine.halted`.
- Verified on a tmpfs with the built binary: frozen at the reserve (507 with `Retry-After`, reads 200), resumed without a restart once space was freed; with the freeze off, a full disk halted the engine (first write 500, then 503, process alive), and after freeing space a restart recovered exactly the acknowledged state.

## Links

- relates to ADR-0005 (durable before visible), ADR-0283 (damage before the end of a segment is refused), ADR-0285 (batch framing), ADR-0131 (checkpoints and WAL compaction), ADR-0142 (metrics), ADR-0382 (whole-store reads leave the writer), ADR-0016 (MCP over the HTTP API)
