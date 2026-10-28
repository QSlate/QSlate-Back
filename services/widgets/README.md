# Widgets service - section 4

Issue [#67](https://github.com/QSlate/QSlate-Back/issues/67), branch
`67-implement-widgets-service-and-custom-widget-endpoints`, based on section 3
(#65). No user source code is executed or compiled in this service.

## Public API

Prefix `/qslate/v1`; the internal API omits this prefix.

| Method | Suffix | Success |
| --- | --- | --- |
| GET | `/widgets/installed` | 200 |
| GET / POST | `/widgets/custom` | 200 / 201 |
| GET / PUT / DELETE | `/widgets/custom/{widgetId}` | 200 / 200 / 204 |
| POST | `/widgets/custom/{widgetId}/preview` | 202 when the controller accepts; otherwise 503 |

See [OpenAPI](../../docs/widgets.openapi.yaml). The PDF truncates route names;
suffixes and payload details are explicit implementation conventions.

Lists return `{items,next_cursor?}` with lightweight summaries, never source.
ID-descending keyset pagination defaults to 100 and accepts 1-1000 items.
`installed` means globally configured installed definitions **plus the user's
own custom definitions**, not marketplace purchases or per-account entitlements.
The default installed catalogue is empty, not fabricated sample widgets.

Create/replace accepts `{name,language,source,configuration?}`. Languages are
`python` and `rust`; names are trimmed, 1-150 printable characters. Configuration
is opaque UI metadata, not a JSON Schema for dashboard instance parameters.
Missing configuration becomes `{}`; `null` is invalid. Limits are in UTF-8 bytes:
100000 for source, 4096 for configuration by default. Strict JSON bodies allow
1 MiB to accommodate escaped source. Auth and Workspace retain their 16 KiB caps.
NUL and numeric exponents unsupported by storage are rejected before SQL.

Every create/save generates a version ID. PUT inserts an immutable version then
atomically changes the current pointer; concurrent saves retain both versions,
with the last commit becoming current. PostgreSQL also rejects UPDATE/DELETE of
version rows. Existing dashboard instances stay pinned to their original version.
DELETE archives a definition: it disappears from listings, detail and new
instantiation, while owner-scoped existing references can still resolve it.
This retention policy is not an erasure/GDPR purge implementation.

## Identity and service boundary

Every business route verifies the public Ed25519 signature and active Auth
session. Owner IDs come only from the verified token, not JSON or headers.
Inaccessible and unknown widgets/versions both return 404. Logout/password reset
invalidate access here as well. Authority outages fail closed with 503.

Widgets owns its database, credentials, volume and storage network. Auth and
Workspace never query its tables. Workspace calls private `POST /catalogue/resolve`
with the already verified user Bearer token. This endpoint is **not published by
the gateway**. It checks the widget/version relationship and visibility before
new instances are stored, and preserves archived versions for already pinned
instances. Source is never returned by resolution. This is a per-request check,
not a distributed transaction or catalogue reservation; retained versions prevent
source loss if a definition is archived concurrently. Widget-specific parameter
schemas, marketplace entitlement policies and OPA remain separate work.

## Configuration

`WIDGETS_CONFIG_FILE` defaults to `services/widgets/config/catalogue.json` locally,
`/app/catalogue.json` in the image. Compose mounts it read-only. Restart Widgets
after edits. Invalid settings stop startup.

`installed` accepts `{id,version_id,name,language,source,configuration}` entries.
Definitions are synchronized transactionally. Changing an existing version's
content stops startup: assign a new version ID. Removing an entry archives it,
without destroying versions retained by dashboard instances. Configurations are
operator-managed and are not writable through user endpoints.

## Preview is a dispatch boundary, not a shipped sandbox

Preview is **disabled by default**, returning 503 after authorization/input
validation. Enabling it requires a separately deployed trusted controller with
gVisor. No controller, runtime installation or Rust/Python execution worker is
provided in this slice; tests of successful dispatch use an HTTP test double,
not a claim that sandbox execution has been validated.

Set `preview.enabled=true`, then inject the environment variables named by
`preview.endpoint_env` and `preview.token_env`. Defaults are `WIDGET_PREVIEW_URL`
and `WIDGET_PREVIEW_TOKEN`; store secrets in ignored `.env.preview` or a secret
manager, not in the settings file. HTTPS is required unless
`allow_insecure_http=true` is explicitly enabled for local tests. No redirects
are followed. Admission timeout is configurable (1-5 seconds).

The controller receives one JSON POST with `job_id`, authenticated `owner_id`,
`widget_id`, `widget_version_id`, immutable `name/language/source/configuration`,
`dataset_id` and `parameters`. A service credential is sent only to the trusted
controller, **never injected into user code**. The optional `Idempotency-Key`
(16-128 identifier characters) is bound to owner, immutable version and normalized
request; exact retries generate the same job identity. Missing keys generate
fresh identities. The controller must durably deduplicate admission and respond
202 with exactly `{job_id:<same identity>,status:"queued"}`. Errors, redirects,
unexpected identities/statuses or invalid responses become a sanitized 503.

Before using a real controller, its operator must implement and verify:

- dataset ownership and retrieval through the owning API, not user-controlled SQL;
- gVisor isolation, no API secrets/database access in jobs, network policy and
  CPU/memory/PID/time/output limits, configured on the controller side;
- durable admission, replay semantics, cancellation, cleanup and owner-scoped
  job status/result access (not added to the seven PDF operations here).

There is no in-process `exec`, shell compilation or fallback to the existing
Python backtest runner. Readiness checks Auth/database, not the optional disabled
controller. Production workload authentication/mTLS is still a deployment concern.

## Development

```bash
make up
go test -race ./services/widgets/... ./services/workspace/...
TEST_DATABASE_URL='postgres://user:password@localhost:5432/test_db?sslmode=disable' make test-integration
API_BASE_URL=http://localhost:8080 MAILPIT_URL=http://localhost:8025 make test-e2e
```

Outside Docker inject a Widgets-owned `DATABASE_URL`, `AUTH_SERVICE_URL` and the
public key, then `go run ./services/widgets/cmd/service` (port 8083). Workspace
also requires `WIDGETS_SERVICE_URL`; the gateway requires it for route expansion.
`make init` adds an independent Widgets password without rotating existing values.
Integration tests create/remove only their own `widgets_test_*` schemas.
