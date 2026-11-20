# v1 contracts and implementation conventions

`qslate.openapi.json` is the canonical REST contract. Service OpenAPI views
reference it; JSON is valid YAML. `streams.asyncapi.json` describes the implemented
backtest and market-tick WebSockets. Contracts explicitly mark
operations as `x-implementation: implemented` or `planned`.

The gateway registers 74 of 98 REST operations: 17 Auth/OAuth2, 11 Workspace,
7 Widgets, 9 Strategy, 11 Lab, 11 Marketplace and 8 Market Data. Widget preview remains admission
to an external controller, disabled by default; it is not a user-code runtime
embedded in the API.

The source PDF contains 95 REST operations and two WS streams. Three OAuth2
extensions bring the REST total to 98. Truncated paths are reconstructed
implementation proposals, identified by `x-path-origin`. Payloads not specified
by the PDF are also conventions to validate with the frontend, not verbatim
requirements recovered from the document.

## Domain ownership

`x-owner` identifies a contract domain, not necessarily an additional container.
Implemented services have independent modules and data; planned domains retain
explicit boundaries even before deployment.

| Contract domain | Responsibility / target deployment boundary |
| --- | --- |
| `auth` | Identity, sessions, account preferences and API keys |
| `workspace` | Private dashboards and layouts |
| `widgets` | Widget catalogue and versions; independent service/database |
| `strategy` | Source/versions, import and isolated syntax diagnostics; no user execution |
| `market-data` | Catalogue, imports, datasets and ticks |
| `lab` | Runs, asynchronous orchestration and results |
| `ai` | Builder generation/explanation with isolated AI dependencies |
| `marketplace` | Publications, shared catalogue and installation rights |
| `security` | Execution policy, AST checks, sandbox state and audit; not Auth |
| `usage` | Event-based consumption/cost/carbon aggregation |
| `runner` | Agents, configurations and dedicated API-key admission |
| `exports` | Export workers using owner-authorized APIs, artifacts and jobs |
| `system` | Lightweight gateway composition for health/version, no business data |

Backtest and tick streams belong to Lab and Market Data respectively. Each
implemented Go service owns its module, technical helpers and image. The root
workspace/contracts are development tools, not shared runtime libraries.
HTTP calls to Auth, catalogues and analyzers are explicit functional dependencies,
not cross-service Go imports.

Planned routes stay out of `gateway/config/routes.json` until implemented and
authorization-tested. Services use IDs, authorized API calls and events instead
of querying another service's SQL tables.

## Shared conventions

- Public prefix: `/qslate/v1`; no implicit `/api/auth` alias.
- Ed25519 JWTs enforce issuer, audience and expiration, with `sid` bound to a
  live Auth session. Signature verification alone does not authorize private data.
- Resource ownership is checked by the owning service. Business IDs are opaque;
  the existing account ID remains an integer.
- Dates use RFC3339/UTC. Financial amounts use decimal strings at relevant API
  boundaries; the imported simulator still calculates with floating-point values.
  Memory limits use bytes and CPU-time limits use seconds where specified.
- JSON errors use `{"error":{"code":"...","message":"..."}}`.
- Collections use `limit`/`cursor`. The account API-key list is a small
  `items` collection without a cursor. Cursor meaning is endpoint-specific.
- Long Lab commands return `202` and persistent IDs. Run/optimization require
  `Idempotency-Key`; cancellation also requires the header but is idempotent
  by run ID. States are `queued/running/completed/failed/cancelled`.
- Runs freeze strategy/dataset versions and bytes. Restoring a strategy creates
  a new revision; layout saves require an optimistic revision.
- Downloads are binary and source-file imports are multipart, not JSON file paths.

## Section 2: Auth

The 14 PDF operations are:

| Method | Path after `/qslate/v1` |
| --- | --- |
| POST | `/auth/register` |
| POST | `/auth/login` |
| POST | `/auth/logout` |
| POST | `/auth/refresh` |
| POST | `/auth/forgot-password` |
| POST | `/auth/reset-password` |
| POST | `/auth/verify-email` |
| GET / PATCH | `/users/me` |
| GET / PATCH | `/users/me/preferences` |
| GET / POST | `/users/me/api-keys` |
| DELETE | `/users/me/api-keys/{keyId}` |

Profile PATCH supports `username` and `display_name`, not email changes.
Preferences are `theme` (`dark/light/system`) and `colorblind_mode`.
Unverified accounts may log in; profile responses expose verification state for
future product policies.

Refresh tokens are opaque, hashed and carried only by an HttpOnly, SameSite=Lax,
host-only cookie with `Path=/qslate/v1/auth`. Secure defaults to true; local
Compose disables it for HTTP. Frontends use `credentials: include`;
refresh/logout require an explicitly allowed Origin. Production assumes a
same-site frontend/API deployment.

Access-token lifetime defaults to 15 minutes; absolute session lifetime to
30 days, both configurable. Refresh consumes its token; replay revokes the whole
family. Clients must serialize refresh requests. Logout/reset invalidate the
affected sessions across all implemented private business services through live
Auth checks. Future services must preserve that rule.

Forgot-password responds identically for existing and unknown accounts. Reset
and verification tokens are single-use, valid for one hour and 24 hours respectively.
`verify-email` accepts either `{token}` (204) or `{email}` to resend (202), never
both; resending replaces the previous token.

Email and token writes are transactional. Outbox payloads use AES-GCM with a
domain-separated key derived from Auth's private key. Messages are purged after
delivery/expiration and retried up to ten times. Delivery is at least once, so
duplicates are possible. Key rotation must handle pending mail and OAuth state
before retiring the old key.

Runner API keys have a name, bounded scopes and a lifetime of 1–365 days
(90 by default). The secret is returned only at creation; only its digest remains.
A Runner key cannot authenticate account-management routes. Key admission on
the planned Runner API is not implemented.

## File-configured OAuth2

Auth loads `OAUTH_PROVIDERS_FILE` at startup, defaulting to
`config/providers.json` from its directory. Edit the file and restart Auth.
The supplied provider is disabled; placeholder credentials are never treated
as an operational provider connection.

Each provider configures authorize/token/userinfo URLs, scopes, claim mappings,
token-endpoint authentication, authorize parameters and client credential
environment-variable names. Claim mappings accept dotted JSON paths. No code
branch selects behavior based on a provider name.

To add a provider:

1. Add its JSON entry and `client_id_env/client_secret_env` names.
2. Supply secrets in ignored `.env.oauth` for Compose or the deployment secret manager.
3. Register the exact callback URL with the provider, then enable the entry.
4. Restart Auth; no gateway/image code change is required.

Three additional routes are `GET /auth/providers`,
`GET /auth/oauth/{provider}/authorize` and
`GET /auth/oauth/{provider}/callback`. Authorization Code + PKCE S256 uses
single-use state bound to a browser cookie and provider. Identity comes from
authenticated userinfo, not full OIDC ID-token validation. Providers need a stable
subject, email and, by default, a verified-email boolean. A different userinfo
protocol requires a dedicated adapter, not bypassed identity checks.

External identities use `(provider, subject)`. Matching an existing email never
automatically merges accounts: the callback returns 409 until an explicit linking
flow exists. OAuth-only accounts have no usable local password; the email-verified
forgot/reset flow can establish one.

On success, the callback sets the session cookie and redirects to the fixed
`PUBLIC_APP_URL/auth/callback?status=success`. The frontend then calls refresh
to obtain an access token. Tokens never appear in the redirect URL. Password
and OAuth2 flows share session issuance.

## Section 3: Workspace

All 11 dashboard/layout/instance operations run in an independent Workspace
binary and PostgreSQL database. The [Workspace view](workspace.openapi.yaml)
references the canonical contract; the [service guide](../services/workspace/README.md)
documents full validation and retention rules.

Each request validates the JWT and live session through Auth's `GET /users/me`.
Authority outages return 503, never signature-only authorization. Owner-scoped
SQL does not read Auth's database.

Row locks serialize mutations and increment revisions. Layout save requires the
current revision (otherwise 409) and atomically replaces every position: one per
instance, no overlaps and no configured-boundary violations. Responses expose
`layout`, `widgets`, `revision` and timestamps; lists are ID-paginated summaries.

Duplication regenerates IDs and preserves a consistent snapshot. Adding/deleting
a widget updates its rectangle in the same transaction. PATCH replaces the entire
configuration object without changing the pinned version. Reset retains instances
and uses dimensions from `WORKSPACE_LAYOUT_FILE`, normally `config/layout.json`.

Widget/version references resolve through Widgets' private catalogue API under
the owner's active session. Archived versions remain accessible to existing
instances, not new placements. Widget-specific parameter schemas remain future
work. Grid limits and detailed payloads are explicit conventions where the PDF
does not specify them.

## Section 4: Widgets and preview admission

The [seven Widgets operations](widgets.openapi.yaml) own immutable versions and
a file-configured installed catalogue. `installed` includes globally available
definitions and the user's own custom definitions; the global catalogue defaults
to empty. When configured, the Marketplace adapter also adds that user's pinned
widget/indicator installations. Paid purchase entitlements are not implemented.

PUT creates a new version without changing existing instance pins. DELETE archives
definitions, retaining immutable history. Each business request validates JWT,
session and ownership. Lists omit source. Configuration is opaque UI metadata,
not a JSON Schema. Default source/configuration limits are 100000/4096 UTF-8 bytes;
Widgets' request limit is 1 MiB, while Auth/Workspace retain 16 KiB limits.

Preview dispatches the current immutable snapshot to a configured trusted sandbox
controller. A matching 202 `{job_id,status:"queued"}` acknowledgment is required;
failures return 503. Widgets neither compiles/executes the code nor guarantees
dataset existence. The controller must check dataset rights, durable deduplication,
gVisor isolation, quotas and owner-scoped result access. It remains external,
disabled by default and unvalidated here. Dispatch tests use an HTTP test server,
not a claimed working sandbox. See the [preview protocol](../services/widgets/README.md).

## Section 5: Strategy and Mini IDE

The [nine Strategy routes](strategy.openapi.yaml) have a private binary/database.
POST creates Python drafts with opaque JSON parameters. PUT requires
`base_version_id` and inserts an immutable snapshot; concurrent edits return
409. Restore requires both the current and selected historical version, copying
name/source/parameters into a new revision without rewriting history.
Lists omit source; history includes snapshots with revision cursors, default
page size ten and maximum twenty. DELETE archives, not erases source.

Multipart import accepts one name and one UTF-8 `.py` file, no paths or temporary
upload files. Incomplete drafts are allowed; lint is separate. Defaults: 100000
source bytes, 4096 parameter bytes, 1 MiB JSON/import bodies. Limits and analysis
transport are file-configured.

Lint calls the separate Python 3.13 analyzer, without secrets/database access,
on an internal network with CPU/memory/PID/time bounds. Each request compiles
without executing the code object. Diagnostics use 1-based positions and UTF-16
columns for Monaco. Unavailability returns 503, never a fabricated valid result.

This is not type/style linting or section 10's execution policy. Dangerous code
can be syntactically valid; `valid:true` never authorizes execution. All nine
operations verify JWT, session and owner. E2E tests use the real analyzer container.
Frontend Monaco integration is not included.

Private `POST /catalogue/snapshot` serves owned publication snapshots.
`POST /catalogue/execution-snapshot` additionally resolves actively installed,
version-pinned Marketplace strategies for Lab. Neither route is public at the
gateway; no cross-service SQL is used. See the [Strategy guide](../services/strategy/README.md).

## Section 6: Lab and actual Python execution

The [11 Lab operations](lab.openapi.yaml) cover run/cancel/status/history,
summary/trades/equity/logs, terminal deletion, comparison and bounded optimization.
The API owns its PostgreSQL queue, frozen inputs, artifacts and stream events.
It never executes submitted Python or receives the execution-daemon socket.

Run/optimization require an owner/operation-scoped `Idempotency-Key` of 16–128
non-whitespace ASCII characters. Payload changes conflict (409); exact replays
return the same admission receipt. Batch admission and quota checks are atomic.
Deletion removes run artifacts/events but retains the receipt, so replay does
not recreate deleted work.

Runs freeze exact strategy and dataset versions, source/CSV bytes, hashes and
effective parameters. Lab obtains exact ready owned dataset versions through
Market Data's private authenticated snapshot endpoint and verifies the checksum.
Without a configured Market Data origin, standalone Lab can use an operator-managed
CSV catalogue. A remote error never falls back to local data. Only the cloud
execution target is implemented; the local target requires section 12 Runner.

A separate Go worker calls the real simulator imported from branch #18 through
stdin/NDJSON inside a runsc job container. The worker records the immutable image
ID and enforces cancellation, resource/output bounds and a deadline. Fenced leases
reject late results; expired claims are cleaned and failed, never automatically
re-executed. Execution is disabled by default; missing isolation/image/ready worker
or unavailable configured data fails closed.

Comparison requires 2–20 distinct owned completed runs. Grid search must fit
entirely within the configured trial cap; Monte Carlo samples unique combinations
with a recorded seed. Optimization returns `{optimization_id,seed,backtests}`;
poll its child run IDs through normal status/history routes. Result metrics come
from the actual engine, including nested custom metrics. Non-finite values become
null. Floating-point engine calculations are not a financial-correctness guarantee.
See [Lab configuration and operational limits](../services/lab/README.md).

## Section 7: Marketplace

The [11 Marketplace operations](marketplace.openapi.yaml) have an independent Go
module/database. Five reads are public. Publication, modification, unpublication,
installation/uninstallation and reviews verify JWT and live Auth sessions.
Reconstructed paths/payloads remain conventions to validate with the frontend.

Authors publish an immutable version of an owned widget/indicator/strategy,
verified by HTTP through the source service. Source never appears in public
metadata. Installation returns a private pinned snapshot; publishing a new version
does not update existing installations. Existing installations survive unpublication.

PATCH requires `base_revision` and rejects stale edits (409). `unlisted` means
accessible by link but absent from search/featured results, not private. Reviews
require an active installation, allow one per account/item and reject self-review.
Metrics count actual active installations and reviews, not simulated financial returns.

Categories, limits and featured IDs are file-configured. Featured also includes
items with active installations, in ID order; it is not time-windowed trend analysis.
Uninstall removes active access for new placements/executions. Historical
per-user/version grants remain usable for already pinned dashboard instances,
not new Lab runs. Internal snapshot/installation routes are not gateway routes.
See [Marketplace rules and limitations](../services/marketplace/README.md).

## Section 8: Market Data

The [eight Market Data operations](market-data.openapi.yaml) cover symbols,
paginated OHLCV history, multipart CSV/Parquet upload, dataset listing/detail,
deletion, mapping and asynchronous validation. All operations require JWT, live
Auth session and owner authorization. Limits and trusted feed settings live in
`services/market-data/config/market-data.json`.

Upload returns an `uploaded` dataset with detected columns and an untrusted text
preview. Mapping requires `base_version_id`; stale revisions conflict (409).
Validation requires a 16–128 character `Idempotency-Key` and returns a durable
job with 202. Poll dataset detail until its version is `ready` or `failed`.
Ready versions are immutable; remapping creates a new version without changing
older ready data. Deletion cascades only within Market Data; frozen Lab runs
remain intact. The API never accepts host file paths or executes uploaded code.

History accepts an owned `custom:<datasetId>` symbol, optional exact ready
`version_id`, UTC `start`/`end`, resolution, limit and timestamp cursor. The range
is half-open `[start,end)` and bounded to 366 days. Values are decimal strings;
aggregation uses TimescaleDB points, not fabricated candles. A partial first
bucket can start before `start`. Live/provider history requires a configured
trusted feed adapter; imported CSV is not replayed as live ticks.

Private `POST /catalogue/dataset-snapshot` accepts an exact ready owned version
and returns canonical CSV plus SHA-256 for Lab. It is absent from gateway routes.
See the [Market Data guide](../services/market-data/README.md) for quotas, parser
isolation, worker recovery, feed contracts and operational limitations.

## WebSocket

`GET /qslate/v1/ws/backtests/{backtestId}/stream` is implemented. Within five
seconds the client sends:

```json
{"type":"authenticate","access_token":"ACCESS_TOKEN","after_event_id":0}
```

Tokens in URL query strings are rejected. No data is sent before JWT, session
and resource authorization. Persisted frames are `{event_id,type,data,created_at}`
with `status`, `progress`, `logs` or `telemetry`. Reconnect with the last integer
event ID to resume. JWT/session checks repeat every second. Progress is live;
the engine buffers captured logs and strategy telemetry until completion.

Lab uses WebSocket protocol ping/pong control frames. Unexpected application
messages/auth failures close 1008; unavailable persisted state closes 1013;
drained terminal runs close 1000. Its channel documents bounds in AsyncAPI.
`GET /qslate/v1/ws/data/ticks` also authenticates in its first frame within five
seconds, then accepts `subscribe`, `unsubscribe` and `ping` commands. It repeats
JWT/session checks every second and emits actual `market.tick.v1` events only
for configured authorized symbols. There is no durable replay. A disabled or
unavailable feed closes 1013; invalid authentication or commands close 1008.
Its frames and limits are specified separately in AsyncAPI. The legacy generic
versioned-event policy remains a reference, not the Lab protocol. Upgrade is
HTTP GET; `WS` is not a new REST method.

## Operational configuration

`SMTP_ADDR`, `SMTP_FROM`, `SMTP_USERNAME`, `SMTP_PASSWORD`,
`SMTP_TLS_MODE` (`starttls` default, `tls` or local `plain`) and
`PUBLIC_APP_URL` configure email. SMTP credentials require TLS; Compose plain mode
is for development Mailpit only at `http://localhost:8025`.

`COOKIE_SECURE`, `ALLOWED_ORIGINS`, `REFRESH_TOKEN_TTL` and
`ACCESS_TOKEN_TTL` configure sessions. JWT issuer/audience must match across
consumers. Lab limits, datasets, execution and WS origins are in `LAB_CONFIG_FILE`;
worker socket settings are deployment configuration, never user input.
