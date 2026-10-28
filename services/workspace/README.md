# Workspace service — section 3

Workspace owns private dashboards, grid layouts and version-pinned widget
instances. It neither executes widgets nor implements their catalogue (section 4),
strategies or backtests. All 11 section-3 operations are implemented and routed.

## HTTP contract

Public prefix: `/qslate/v1`. Internal requests omit this prefix.

| Method | Path after the public prefix | Success |
| --- | --- | --- |
| GET / POST | `/dashboards` | 200 / 201 |
| GET / PUT / DELETE | `/dashboards/{dashboardId}` | 200 / 200 / 204 |
| POST | `/dashboards/{dashboardId}/duplicate` | 201 |
| PUT | `/dashboards/{dashboardId}/layout` | 200 |
| POST | `/dashboards/{dashboardId}/widgets` | 201 |
| PATCH / DELETE | `/dashboards/{dashboardId}/widgets/{instanceId}` | 200 / 204 |
| POST | `/dashboards/{dashboardId}/layout/reset` | 200 |

See [canonical REST](../../docs/qslate.openapi.json) and the
[public Workspace view](../../docs/workspace.openapi.yaml). PDF-truncated paths
and unspecified payload shapes remain explicit implementation conventions, not
verbatim requirements recovered from the PDF.

- Dashboard responses contain metadata, revision, timestamps, `widgets` and
  `layout`. Collection pages contain lightweight summaries and `next_cursor`
  only when another page exists. Pass it as `cursor`; ID descending order,
  default `limit=100`, maximum 1000, never creation-date order.
- IDs are random 43-character base64url values. Owner IDs come exclusively from
  verified access tokens. Every query/transaction includes the owner. Other
  owners' resources and unknown resources both return 404.
- Signature verification uses only Auth's public Ed25519 key. Every business
  request also calls Auth's protected `GET /users/me`, without a session cache.
  Logout/reset/replay revocation applies on the next request. Auth unavailability
  fails closed with 503; revoked credentials return 401. No SQL crosses services.
- Creation starts at revision 1. Every successful metadata/widget/layout mutation
  increments it once. Owner-scoped `SELECT FOR UPDATE` serializes mutations.
  Failed validation and stale writes roll back the entire transaction.
- `PUT` metadata replaces title/description/tags; omitted description/tags are
  cleared. Title is trimmed (1–150 characters); description ≤2000, tags ≤20,
  each 1–40, trimmed/lowercased/deduplicated. Unsupported properties are rejected.
- `PUT .../layout` expects `{ "revision": 3, "items": [...] }`. Each item needs
  `instance_id,x,y,width,height`. All existing instances must occur exactly once;
  coordinates are nonnegative, dimensions positive, rectangles inside the
  configured grid and nonoverlapping. A stale revision returns 409: reload then
  reconcile, not blind retry. No partial write is persisted.
- Adding an instance pins `widget_id` and `widget_version_id`, creates its ID and
  a free default-sized rectangle atomically. Configuration is a JSON object
  (≤4096 UTF-8 bytes), defaults to `{}` and preserves large JSON integers.
  NUL characters and numeric exponents outside −10000…10000 are rejected before
  PostgreSQL storage; JSON string escapes are normalized.
  PATCH **replaces**, rather than merges, the whole configuration; `{}` clears it,
  while missing/null/array configurations are rejected. Deletion also removes
  its rectangle. Widget creation/PATCH responses return the instance; GET the
  dashboard to obtain its new revision before saving a grid.
- Duplication copies a consistent snapshot, regenerates dashboard and instance
  IDs and remaps positions. Metadata/configuration are preserved, revision resets
  to 1. Copies are independent; the title is preserved, not automatically renamed.
- Reset retains instances and configurations, recomputing row-major positions
  from the configuration file. It does not delete widgets or restore metadata.

Widget/version references are now resolved against the private Widgets catalogue
API using the verified active user session. New instances require active,
owner-visible definitions and matching immutable versions. Existing instances
and duplication can retain archived versions, still scoped to their owner.
Catalogue outages fail closed with 503 before mutation. No cross-service SQL is
used. Widget-specific parameter schemas remain future work. The check is not a
distributed transaction; immutable versions are retained across archival.

## Configuration and deployment

`WORKSPACE_LAYOUT_FILE` defaults to `services/workspace/config/layout.json` outside
Docker, `/app/layout.json` inside. Compose mounts the file read-only. Restart
Workspace after changing it. The supplied file configures 12 columns, 1000 rows,
128 instances and default 6×4 widgets. No widget-specific defaults are hardcoded.
Invalid settings fail startup. Reducing bounds does not silently rewrite existing
dashboards: explicit layout save/reset validates against the new settings.

Workspace has its own database, credentials, volume and private storage network.
Only the gateway exposes its routes. Workspace can reach Auth's API, not Auth's
database, and receives no signing key or OAuth secrets. Readiness checks both
PostgreSQL, Auth and Widgets; liveness checks the process. HTTP bodies are limited to 16 KiB,
request contexts to 10 seconds, Auth calls to 2 seconds. Internal service identity
(mTLS/workload identities) is a production hardening step; private networks alone
are not claimed to authenticate workloads.

```bash
make up
go test -race ./services/workspace/... ./internal/platform/session
TEST_DATABASE_URL='postgres://user:password@localhost:5432/test_db?sslmode=disable' \
  make test-integration
API_BASE_URL=http://localhost:8080 MAILPIT_URL=http://localhost:8025 make test-e2e
```

`make init` safely adds an independent Workspace password to an existing `.env`
without rotating existing credentials. For a local Go process supply the
Workspace-owned `DATABASE_URL`, `AUTH_SERVICE_URL=http://127.0.0.1:8081`,
`WIDGETS_SERVICE_URL=http://127.0.0.1:8083` and the
public key path, then run `go run ./services/workspace/cmd/service` (port 8082).
Integration tests create/remove only their own `workspace_test_*` schemas.
Migrations use shared technical runner `internal/platform/migrate`, but SQL and
migration histories remain private to each service.

Tracked by [issue #65](https://github.com/QSlate/QSlate-Back/issues/65), branch
`65-implement-workspace-service-and-dashboard-layout-endpoints`, based on the
Auth/Gateway foundation in [PR #64](https://github.com/QSlate/QSlate-Back/pull/64).
Rebase onto main once that foundation is merged, before preparing a Workspace PR.
