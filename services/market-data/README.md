# Market Data - section 8

Issue [#76](https://github.com/QSlate/QSlate-Back/issues/76). Independent Go API
and indexing worker, service-owned TimescaleDB/PostgreSQL storage, and a private
resource-limited CSV/Parquet parser. No imports or SQL access across services.

## API and ingestion lifecycle

Prefix `/qslate/v1`; every REST operation verifies JWT, live Auth session and
resource ownership. An inaccessible dataset/version is indistinguishable from
an unknown one (404). The private service omits the public prefix.

| Method | Path | Behavior |
| --- | --- | --- |
| GET | `/data/symbols` | Ready private dataset symbols plus configured authorized source symbols |
| GET | `/data/history` | Bounded OHLCV query with decimal-string values |
| POST | `/data/custom/upload` | Multipart CSV/Parquet upload and schema inspection; 201 |
| GET | `/data/custom` | ID-descending owner-scoped metadata pages |
| GET | `/data/custom/{datasetId}` | Current version, status, columns, bounded preview and validation error |
| POST | `/data/custom/{datasetId}/mapping` | Optimistic column mapping; 200 with a new version |
| POST | `/data/custom/{datasetId}/validate` | Durable validation/indexing admission; 202 |
| DELETE | `/data/custom/{datasetId}` | Delete owned raw data, versions, points and validation jobs; 204 |

The default catalogue also exposes frozen, repository-owned BTC/USDT, ETH/USDT
and AAPL datasets at `1h` and derived `1d` resolutions. Their stable `public-*`
dataset IDs and version IDs can be passed directly to Lab, so a new account does
not need to upload a file. Public bytes are checksum-verified at service startup;
provenance and regeneration instructions are in [data/README.md](data/README.md).

Reconstructed paths/payloads are explicit implementation conventions, not complete
paths recovered from the truncated PDF. [OpenAPI](openapi.yaml) references the
root canonical contract.

1. Upload multipart fields `name` and `file`, exactly once each. Only `.csv` and
   `.parquet` basenames are accepted; MIME must match CSV, Parquet or binary.
   No user filename is opened and no temporary host upload file is created.
2. The separate parser inspects actual decoded contents, not only extensions.
   Upload returns `id`, initial `version_id`, `status: uploaded`, columns and a
   preview. This does not yet make the dataset queryable for backtests.
3. POST mapping using `base_version_id` from detail, `timestamp_column`,
   `timezone`, `timestamp_unit` (`iso8601`, `s`, `ms`, `us`, `ns`) and `columns`
   mapping `open/high/low/close`, with optional `volume`. Stale base versions
   return 409; a valid change creates another `version_id` with status `mapped`.
4. POST validate with `Idempotency-Key` (16-128 non-whitespace ASCII characters).
   A persistent job is returned with dataset/version IDs. Poll dataset detail:
   `indexing` becomes `ready` or `failed`. Repeating the key for the same dataset
   returns its original job; changing the dataset with that key returns 409.
5. Use the ready dataset/version IDs in Lab. No source file paths enter Lab's API.

Preview values are untrusted text; clients must escape them when rendering.
History/results are not available before successful validation. Mapping never
overwrites old ready versions. New current mapping versions can hide a symbol
from the ready catalogue until indexed; old ready versions remain explicitly
queryable with `version_id` until the whole dataset is deleted.

## Temporal storage and validation

The API stores bounded raw bytes and metadata in its own database. A separate Go
worker claims SQL jobs with `FOR UPDATE SKIP LOCKED`, then calls the parser for
normalization. Points, canonical CSV/checksum and ready status commit together.
Claims are fenced by token/lease. Interrupted validation fails on recovery instead
of silently replaying stale work; an explicit new validation request can retry it.
Deletion cascades jobs/versions/points, so a late worker cannot restore deleted data.

The supplied deployment requires TimescaleDB and converts `md_points` to a real
hypertable. `require_timescale:false` supports ordinary PostgreSQL tests, not a
claim that the extension is installed there. Migrations are local and immutable.

Timestamps must be strictly increasing and unique at microsecond precision.
Ambiguous/nonexistent naive local times are rejected; supply an explicit offset.
Timezone names are IANA names. Dates are restricted to years 1970-2200. OHLC prices
must be finite, positive and consistent; volume is nonnegative, or explicitly
filled with zero when the mapping omits it. Decimal precision is bounded to the
storage format (20 integer and 18 fractional digits); no NaN/Infinity is accepted.

CSV is UTF-8, comma-delimited with unique headers. Parquet is decoded by pinned
Apache Arrow with bounded batches/metadata and extension decoding disabled.
Nested/unsupported schemas are rejected. A one-shot child does parsing, never
imports submitted modules or executes user code. CPU/wall/address-space limits
combine with a read-only, non-root, capability-free parser container with no
Auth/DB secrets, no external network, 768 MiB memory and 32 PIDs. Parser tests
are built into a test image; they do not bind-mount host test directories or
require SELinux policy changes.

Default limits in `config/market-data.json`: 8 MiB upload, 20000 decoded rows,
20 datasets/64 MiB raw bytes per owner, five metadata versions per dataset,
4 MiB normalized snapshot, 64 columns and 4096-byte cells. The initial uploaded
version counts toward the version limit. Source/metadata limits are separate
from decoded CPU/memory limits; a compressed file can fail decoding resource bounds.
No automatic storage TTL or distributed request-rate limiter is bundled.

## History and Lab integration

An imported dataset's symbol is `custom:<datasetId>`; bundled symbols use their
catalogue value such as `BTC/USDT@1h`. History requires `symbol`,
`start`, `end`, and `resolution`. The time range is `[start,end)`, at most 366 days.
Resolutions are `raw`, `1m`, `5m`, `15m`, `1h`, `1d`; aggregates use first open,
maximum high, minimum low, last close and summed volume. Partial buckets can
start before the requested lower bound. Optional `version_id` pins an older ready
version. Pages default to 100, maximum 1000; pass the returned cursor unchanged.

Lab's optional `MARKET_DATA_SERVICE_URL` is set by default in Compose. Lab forwards
only its verified Bearer token to private `POST /catalogue/dataset-snapshot`, which
checks ownership and exact ready version. The normalized CSV/SHA-256 is verified
and frozen in Lab's own queue. The private endpoint is never exposed by the gateway.
Neither service reads the other's database. Deleting/remapping a dataset does not
erase or change already authorized Lab snapshots; Lab's retention is separate.

## Actual live data, not historical replay

`/qslate/v1/ws/data/ticks` authenticates in its first JSON frame within five
seconds: `{"type":"authenticate","access_token":"..."}`. Then send
`{"type":"subscribe","symbols":["YOUR_CONFIGURED_SYMBOL"]}` or `unsubscribe`.
Tokens in URLs are rejected. Session/expiry checks repeat every second; default
limits are ten symbols and 32 streams per API replica. Protocol ping/pong and
application `ping` are supported. Close 1008 rejects authentication/commands or
subscriptions; 1013 signals unavailable/out-of-order live data. No durable tick
replay is claimed. Imported OHLCV files are never relabelled as live ticks.

Live transport is disabled and the configured symbol list is empty by default.
To use a real trusted source, enable `live`, configure its symbol/name/owner list
(`owner_id:0` means shared), and inject the named origin/token variables using
ignored `.env.market-data`. HTTPS is required unless explicitly opted into local
HTTP. Redirects and caller-selected URLs/paths are rejected. User JWTs never reach
the source; only its configured service credential is used.

The source adapter contract is POST `/ticks` with `{symbols,after}` returning
`{items:[{id,symbol,time,price,quantity}]}` with stable per-symbol IDs and increasing
times. Empty cursors mean a fresh live subscription, not a history dump. POST
`/history` accepts `{symbol,resolution,start,end,cursor,limit}` and returns the
canonical history shape. Feed failures return 503/close 1013, never generated
prices. Provider-specific Binance/IBKR BYOK connectors remain separate issues
#25/#26. An HTTP test source verifies transport only, not an active exchange.

## Development and checks

Export `.env.example` explicitly for standalone processes. `MARKET_DATA_CONFIG_FILE`
defaults to `config/market-data.json`, and `MARKET_DATA_PARSER_URL` names the trusted
private parser origin. Compose exposes no API/database/parser host ports. API and
worker own independent binaries; only the API receives Auth's public key.

Compose assigns the five Market Data networks explicit /28 subnets to avoid
exhausting Docker's automatic address pools. Their first three IPv4 octets default
to `10.249.76`; set `MARKET_DATA_NETWORK_PREFIX` in the root `.env` to a free prefix
if that range conflicts with another network. Existing service networks are unchanged.

```sh
make -C services/market-data test build
make market-parser-test
TEST_DATABASE_URL=postgres://... make -C services/market-data test-integration
# Add TEST_TIMESCALE=true and TEST_PARSER_URL=http://... for real extension/decoder checks.
API_BASE_URL=http://localhost:8080 MAILPIT_URL=http://localhost:8025 make test-e2e
```

Production still needs workload authentication/TLS, centralized admission quotas,
backup/retention policies, monitoring and parser dependency maintenance. This
ingestion parser does not replace Lab's separate user-code execution sandbox.
