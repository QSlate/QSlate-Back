# Strategy service - section 5

Issue [#68](https://github.com/QSlate/QSlate-Back/issues/68), branch
`68-implement-strategy-service-and-mini-ide-endpoints`, based on the current
Workspace/Widgets foundation. This service stores Python drafts, not running bots.

## Public API

Prefix `/qslate/v1`; the private service omits this prefix.

| Method | Suffix | Success |
| --- | --- | --- |
| GET / POST | `/strategies` | 200 / 201 |
| GET / PUT / DELETE | `/strategies/{strategyId}` | 200 / 200 / 204 |
| POST | `/strategies/import` | 201 |
| GET | `/strategies/{strategyId}/versions` | 200 |
| POST | `/strategies/{strategyId}/restore` | 200 |
| POST | `/strategies/lint` | 200, or 503 if analysis is unavailable |

[Public OpenAPI](../../docs/strategy.openapi.yaml) references the canonical
contract. The PDF truncates most suffixes; detailed paths/payloads are explicit
implementation conventions to validate with the frontend, not recovered PDF text.

## Drafts, revision history and concurrent editing

POST accepts `{name,source,parameters?}`. Names are trimmed and contain 1-150
printable Unicode characters. Source must be nonblank UTF-8 without NUL, at most
100000 bytes. Parameters are an opaque JSON object, at most 4096 UTF-8 bytes;
omission becomes `{}`, while `null` is rejected. Numeric precision is preserved.
Saving incomplete or syntactically invalid drafts is allowed: lint is independent.

PUT additionally **requires `base_version_id`**, the current version last read by
the editor. A row lock checks it before inserting a fresh immutable snapshot and
advancing `revision`. Concurrent saves with the same base yield one success and
one 409 `version_conflict`; stale writes never insert an orphan revision.
Clients should reload/merge and explicitly resubmit, not blindly retry a stale save.

Restore accepts `{version_id,base_version_id}`. It copies the selected snapshot's
name, code and parameters into a **new** revision/ID, recording `restored_from`.
It never rewinds the current pointer or edits historical content. PostgreSQL
rejects UPDATE/DELETE of version rows and enforces same-strategy version links.

The list returns source-free `{items,next_cursor?}` summaries ordered by opaque
ID descending (default 100, maximum 1000). History returns full private snapshots
ordered by monotonic revision descending, with an exclusive decimal revision
cursor (default 10, maximum 20), bounding response size even for large source.
DELETE archives the strategy: detail, history and mutation routes return 404,
but snapshots are retained for future run pinning. This is not a GDPR erasure API.
There is no run/export snapshot API yet; add a narrowly scoped owner contract when
those consumers are implemented, not cross-service SQL.

All nine operations verify the Ed25519 JWT and live Auth session. Owners come
only from verified identity, never JSON or identity headers. Inaccessible and
unknown IDs both return 404. Auth outages fail closed with 503; logout/reset
invalidate Strategy access immediately. The database, credentials and storage
network belong solely to Strategy; no tables or domain models are shared.

## Direct Python import

The request is multipart/form-data with **exactly one `name` field and one `file`
part**. The filename must be a `.py` basename (no path separators), at most 255
UTF-8 bytes. Source is bounded to 100000 bytes; an optional UTF-8 BOM is removed.
Missing/duplicate/unexpected fields, invalid UTF-8, NUL, empty source, non-.py
files and encoded multipart parts are rejected. Parameter defaults are `{}`.

Upload data is streamed through capped in-memory buffers; no user filename is
opened and no temporary upload files are created. Size errors return 413, media
type errors 415, and malformed fields 400. Source is **never imported or executed**.

## Real syntax diagnostics, isolated from the Go API

POST `/strategies/lint` accepts `{source,language?:"python"}`. Empty drafts are
valid. The Go API forwards only source/language to the configured analyzer,
never the user JWT, owner, SQL credentials or API keys. Transport timeouts,
redirects, malformed/oversized responses or analyzer failures return sanitized
503 `analysis_unavailable`, never a fabricated successful result.

Compose ships a separate Python **3.13** analyzer. Each request starts a disposable
isolated interpreter (`-I -S -B`) running only the trusted compiler wrapper.
[`compile`](https://docs.python.org/3.13/library/functions.html#compile) produces
a code object that is immediately discarded: no `eval`, `exec`,
imports of submitted modules, decorators or top-level user statements run. Unlike
AST construction alone, the compiler also catches `return`/`break` outside scope.
The child has CPU/address-space/output-file/open-file limits and a wall deadline.
The analyzer container is non-root, read-only, capability-free, PID/CPU/memory
bounded, on one internal network, without database connections or secret mounts.

Diagnostics return `{valid,diagnostics:[{line,column,end_line,end_column,severity,message}]}`.
Positions are **1-based with UTF-16 columns**, matching Monaco's indexing even
for non-BMP characters. No raw source/traceback is logged or returned by transport
errors. Tests compile malicious-looking text with writes/raises/infinite loops
and verify it is not executed. Unit tests also cover real syntax/scope errors;
E2E tests call the real Python container rather than an HTTP mock.

This is a **syntax/scope checker**, not Ruff/Pylint style analysis, type checking,
an undefined-name checker or an execution security policy. A program containing
dangerous imports may be syntactically valid. Section 10 AST allowlisting and
gVisor execution isolation remain separate work; never use `valid:true` as
permission to run strategy source. HTTP deployment here is local/internal;
production workload authentication/mTLS and distributed admission quotas remain
deployment concerns. No user execution sandbox is claimed by this slice.

## File configuration and development

`STRATEGY_CONFIG_FILE` defaults to `config/strategy.json` from the service folder
(image `/app/strategy.json`). It configures
source/parameter/request caps, history page bounds and analysis transport:
`analysis.enabled`, `url_env`, `timeout_seconds`, `allow_insecure_http`.
The default URL variable is `STRATEGY_ANALYZER_URL`. Only an origin is accepted,
not userinfo/query/fragment/path; no redirects. HTTP is explicitly allowed for
Compose's isolated local network; set `allow_insecure_http=false` and configure
HTTPS at the trust boundary in production. Analyzer transport has no API secrets.

`analyzer/config.json` configures compiler source/body limits, memory/CPU/wall
limits and socket deadline, mounted read-only in Compose. Default child limits:
128 MiB address space, 2 CPU seconds and 2 wall seconds; container limits are
256 MiB, 1 CPU and 32 PIDs. Restart services after configuration changes. Keep the
analyzer source/body limits compatible with the Go service. No limits are accepted
from an end-user request. Invalid settings stop startup. Readiness checks DB,
Auth and the enabled analyzer; disabling analysis leaves CRUD available and
makes lint return 503.

```bash
make up
make -C services/strategy test build
make -C services/strategy docker-build
python3 -B -m unittest discover -s services/strategy/analyzer -p 'test_*.py'
TEST_DATABASE_URL='postgres://user:password@localhost:5432/test_db?sslmode=disable' make test-integration
API_BASE_URL=http://localhost:8080 MAILPIT_URL=http://localhost:8025 make test-e2e
```

Outside Docker, inject a Strategy-owned `DATABASE_URL`, `AUTH_SERVICE_URL`, public
key and `STRATEGY_ANALYZER_URL`, then run `make -C services/strategy run`
(default port 8084). Run the analyzer with Python 3.13 on Linux using
`HTTP_HOST=127.0.0.1 HTTP_PORT=8085 python3 -I -B services/strategy/analyzer/server.py`.
Compose exposes neither service nor its DB on the host. `make init` appends an
independent Strategy database password without rotating existing secrets.

The E2E helper reuses the Workspace flow's two accounts to respect the existing
Auth-sensitive-operation rate limit. Integration tests create/drop only their
own `strategy_test_*` schema and check concurrent migrations, ownership, save
conflicts, restore lineage, trigger immutability, persistence and checksum drift.

## Build independence

Like AREA, this folder owns `go.mod`, `go.sum`, Dockerfile, Makefile, configuration,
SQL and technical helpers. `make test build` from this folder uses `GOWORK=off`;
it also works after copying the folder outside the repository. There is no
root runtime dependency, local module replacement or import of another service.
The analyzer image builds solely from `analyzer/`; it does not receive Go code,
the other services' configuration or secrets. Its filesystem/resource/network
isolation remains unchanged. Root Compose only coordinates local dependencies.
