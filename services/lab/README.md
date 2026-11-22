# Lab — section 6

Independent Go module, PostgreSQL database, migrations, API and queue worker.
Build without the repository workspace: `make test build` in this directory.
Lab imports no other QSlate module. Auth and Strategy are HTTP dependencies;
only Lab owns its queue, snapshots, artifacts and event tables.

## HTTP contract

Public prefix `/qslate/v1`; see [OpenAPI](openapi.yaml) and the root AsyncAPI.
Every REST operation verifies the JWT, live Auth session and resource ownership.
Other users' runs and artifacts return 404. The gateway never exposes private
Strategy catalogue routes or the database. Only Auth holds the signing key.

| Method | Path after prefix | Purpose |
| --- | --- | --- |
| POST | `/backtests/run` | Freeze inputs and atomically enqueue |
| POST | `/backtests/{backtestId}/cancel` | Idempotent queued/running cancellation |
| GET | `/backtests/{backtestId}/status` | Durable state, progress and provenance |
| GET | `/backtests` | Owner-scoped cursor history |
| GET | `/backtests/{backtestId}/summary` | Actual completed engine metrics |
| GET | `/backtests/{backtestId}/trades` | Paginated actual engine trades |
| GET | `/backtests/{backtestId}/equity-curve` | Decimal-string equity and percentage drawdown |
| GET | `/backtests/{backtestId}/logs` | Bounded, credential-pattern-redacted log lines |
| DELETE | `/backtests/{backtestId}` | Terminal run and artifacts after worker cleanup |
| POST | `/backtests/compare` | 2–20 distinct owned completed summaries |
| POST | `/backtests/optimize` | Bounded grid/seeded Monte Carlo trials |

Run/optimization require an ASCII `Idempotency-Key` (16–128 characters).
The key is scoped to the owner and operation; changing the payload returns 409.
Concurrent requests are serialized per owner; a batch and its replay response
are committed atomically. Replays need no currently available source or dataset.
Cancellation also requires the header but is naturally idempotent by run ID.
Deletion keeps the idempotency receipt, so replay never recreates deleted work.
Receipts currently have no automatic TTL; operational retention is future work.

`RunInput` requires strategy/version, dataset/version and decimal-string capital.
Optional `parameters` override the pinned strategy configuration. `window` defaults
to 20, with optional `commission_rate`, `slippage_bps` and `class_name`. Optional
`period_start` and `period_end` must be supplied together as RFC3339 timestamps;
the half-open UTC interval is limited to 366 days and is frozen with the run.
When a strategy version includes `parameter_schema`, Lab rejects unknown values,
wrong types and values outside the declared minimum/maximum.
Only `execution_target: "cloud"` is implemented; `local` requires section 12.
When `MARKET_DATA_SERVICE_URL` is configured (the Compose default), Lab retrieves
the exact ready, owner-authorized dataset version from Market Data's private
`POST /catalogue/dataset-snapshot`. CSV bytes and SHA-256 are verified and frozen
in the run before enqueueing. A remote error never falls back to local files.
Without this origin, the standalone operator-managed CSV adapter remains available.
Installed Marketplace strategies resolve through Strategy's authenticated
`POST /catalogue/execution-snapshot`, requiring an active pinned installation.
Old retained installation grants alone do not authorize new executions.

Optimization returns `{optimization_id, seed, backtests: [...]}`. Poll the returned
run IDs through the ordinary status/history routes; there is no fabricated parent
job result. Sorted parameter keys and a recorded seed make trial selection stable.
Grid search must fit entirely within `max_runs`; Monte Carlo samples unique
combinations. Configuration limits default to 25 trials and 50 outstanding runs
per owner. Completed numerical metrics are supplied by the actual Python engine,
including its custom nested metrics; non-finite JSON numbers become null.

## Engine and execution boundary

`simulator/` is imported from colleague branch
`18-implement-candle-by-candle-simulation-engine-in-python`, revision
`fb63ed3180ac263019c1b6524da90826f6b316a2`; no merge of its older Go architecture.
The added stdin/NDJSON adapter calls `simulator.job.runner.run_job`. The only
engine transport hook is optional progress reporting; trading calculations are
unchanged. Strategies must implement a `Strategy` subclass with `on_bar(ctx)`.
Legacy `def run(data)` drafts are not silently converted or reported as success.
The existing engine uses floating-point amounts and its own accounting/metrics;
this integration is not an independent certification of financial correctness.

The API never starts Python or receives the Docker socket. A separate Go worker
claims rows using `FOR UPDATE SKIP LOCKED`, launches the configured image by its
resolved immutable image ID, and records this ID on the run. Input bytes, hashes,
versions, effective parameters and base engine revision are persisted. Default
random/NumPy seeds are fixed, but arbitrary strategies can use time or other
non-determinism; frozen inputs are not a guarantee that arbitrary code is deterministic.

Production jobs use `runsc`, no network, no host mounts, a read-only root filesystem,
non-root UID, no capabilities, no-new-privileges, bounded CPU/memory/PIDs/work tmpfs,
output/log limits and a worker-enforced wall deadline. No JWT, DB credentials or
Auth keys enter the job container. The worker socket is privileged: use a dedicated
execution daemon/host, not a general-purpose production Docker daemon. gVisor and
ordinary resource controls are not an absolute security guarantee.

Cancellation is polled at 300 ms and kills the job container before releasing its
claim. Terminal deletion waits for that release (409 while cleanup is pending).
Completion is fenced by the claim token, live lease and running state, so late
results cannot overwrite cancellation. A replacement worker kills expired labelled
containers and marks interrupted runs failed; it never silently re-executes code.
Keep workers supervised: after total worker failure, containers are cleaned only
when a worker returns. No out-of-process daemon-level deadline reaper is bundled.
Configuration changes can strand older execution-hash queue entries; restore their
configuration to drain them or cancel them explicitly.

## Configure and start

`LAB_CONFIG_FILE` selects strict JSON (default `config/lab.json`). Limits, runtime,
image, datasets and allowed WebSocket origins are configurable; secrets stay in
environment variables. Execution is **disabled by default**, and absence of a
fresh ready worker returns 503 instead of generating mocked results.

1. Provision an isolated Docker daemon with the `runsc` runtime externally.
2. At repository root, run `make init simulator-build` on that execution daemon.
3. Upload CSV/Parquet through Market Data, configure its column mapping and
   validate/index it. Use its ready dataset ID/version in the run. For standalone
   Lab without `MARKET_DATA_SERVICE_URL`, register operator-managed CSV files in
   Lab JSON instead; see `data/README.md`. User-controlled paths are never accepted.
4. Set `execution.enabled: true`, choose the image and configure allowed origins.
5. Set `LAB_DOCKER_SOCKET` and `LAB_DOCKER_GID` to the dedicated daemon socket/group,
   then run `docker compose --profile lab-execution up --build -d`.

The default Compose stack starts Lab API/database only. Missing runtime/image/data
fails closed; `runsc` cannot be replaced with `runc` in production configuration.
For an explicit developer-machine end-to-end run, `make up-lab-local` selects
`config/lab.local.json`, enables the worker and requires the separate
`LAB_ALLOW_LOCAL_EXECUTION=true` guard. This profile uses `runc` only locally;
the default configuration remains disabled and isolated.
Section 8 owns ingestion, metadata and normalized time-series storage. Lab keeps
an authorized snapshot so dataset deletion/remapping cannot rewrite existing runs.
The optional standalone file adapter is not used when the Market Data origin is set.

## WebSocket

Connect to `/qslate/v1/ws/backtests/{backtestId}/stream` with an allowed Origin.
Send within five seconds:

```json
{"type":"authenticate","access_token":"ACCESS_TOKEN","after_event_id":0}
```

Tokens in query strings are rejected. Nothing is sent before session and ownership
validation. Frames are `{event_id,type,data,created_at}` with `status`, `progress`,
`logs` or `telemetry`; reconnect with the last integer event ID to resume. Progress
is live; the engine buffers captured logs and strategy telemetry until completion.
Sessions/JWT expiry are rechecked every second. Ping/pong uses WebSocket protocol
control frames, not application commands. Unexpected commands/auth failures close
1008; unavailable state closes 1013; drained terminal runs close 1000. Bounds:
128 streams/replica, 100 events/page, 5-second writes, 30-second read heartbeat.
All events persist until run deletion. Use REST to read status/results when a
stream is unavailable; deleted runs return 404 and their event history cannot
be resumed or recovered through REST.

## Verify

```sh
make -C services/lab test build
TEST_DATABASE_URL=postgres://... make -C services/lab test-integration
make simulator-test test-engine
make test vet test-standalone
```

The explicit `test-engine` target uses runc **only for fixed repository-owned
fixtures**, without going through production config. It verifies real Go-to-Python
artifacts, progress and deadline cancellation. It does not certify runsc deployment.
Go unit tests cover HTTP/session/WS authorization, search, validation and pins;
PostgreSQL tests cover persistence, ownership, concurrent migrations, replay,
leases, cancellation, cleanup fences, recovery and artifact retention.
