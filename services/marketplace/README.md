# Marketplace service — section 7

Issue [#71](https://github.com/QSlate/QSlate-Back/issues/71). This independent Go
module owns its database, migrations, configuration, HTTP adapters and image.
Section 6 remains deferred; publication/install never runs submitted code.

## Public API

Prefix `/qslate/v1`; private service routes omit this prefix.

| Method | Path | Access |
| --- | --- | --- |
| GET / POST | `/marketplace/items` | Public read / active author |
| GET | `/marketplace/featured` | Public |
| GET | `/marketplace/categories` | Public |
| GET / PATCH / DELETE | `/marketplace/items/{itemId}` | Public read / owner mutations |
| POST | `/marketplace/items/{itemId}/install` | Active user |
| DELETE | `/marketplace/items/{itemId}/installation` | Own installation |
| GET / POST | `/marketplace/items/{itemId}/reviews` | Public read / active installer |

[OpenAPI](../../docs/marketplace.openapi.yaml) defines the payloads. Exact suffixes
and detailed payloads are implementation conventions reconstructed from truncated
PDF labels, not recovered full paths. All private operations verify JWT and live
Auth sessions; a session authority outage fails closed with 503.

## Publication and version pinning

POST accepts `{kind,resource_id,version_id,name,category,description?,visibility?}`.
Kinds are `widget`, `indicator` (a Widgets artifact) and `strategy`. The service
forwards only the verified user's Bearer token to an explicit owner-scoped
snapshot contract. It never trusts an owner ID supplied in JSON or headers.
The snapshot is copied into Marketplace-owned immutable storage: no SQL joins
across services and no dependency on the author's live source after publication.

One listing exists per author/kind/resource. PATCH requires `base_revision`
and at least one change. Concurrent edits conflict (409), including new version
publication. A version update captures another owned immutable version; source
cannot be replaced under an already published version ID. DELETE unpublishes;
PATCH with explicit visibility can republish the same listing. Public detail,
reviews, lists and new installs hide unpublished items. `unlisted` items remain
accessible/installable by link, but absent from catalogue and featured results.

Install takes `{version_id}` and returns its **private source snapshot** plus
`catalog_id:market-<itemId>`. This is an actual persisted per-user installation,
not execution or a backtest. Repeating an active exact install preserves its
timestamp and content, even after unpublication. Requesting another published
version explicitly upgrades that installation; no automatic upgrades occur.

Widgets merges installed widget/indicator summaries into `/widgets/installed`.
Workspace can place them through the existing private catalogue resolver.
Strategy merges installed strategies into its list and exposes detail plus a
one-entry read-only installed history. An editor can fork a new private draft;
it cannot mutate the author's source. Both integrations are optional outside
Compose (`MARKETPLACE_SERVICE_URL`), use local HTTP adapters and keep standalone
builds free of root/sibling imports. Failures return 503, not partial catalogues.

Uninstall hides the active installation and prevents new placements. Retained
per-user/version grants allow already pinned dashboard instances to keep working,
including after upgrade/unpublication. Historical grants are not public and are
never accepted for another account. This retention is not a GDPR erasure API.

## Discovery, reviews and limits

Lists support literal case-insensitive `q` (200 characters), `kind`, `category`
and exclusive ID-descending pagination (default 100, maximum 1000). No source or
private resource ID is returned publicly. Categories come from file configuration,
with their own ID cursor. Featured combines configured IDs and items with active
installations, in ID order. It does not claim time-windowed trending analytics.

Names are 1–150 printable Unicode characters; descriptions at most 10000 and
review comments at most 4000. Source limits remain owned by Widgets/Strategy
(100000 UTF-8 bytes, 4096-byte configuration). JSON requests are capped at 64 KiB
by default. Unknown/duplicate query fields and unsupported JSON fields fail.

A review requires an active installation, rating 1–5 and a different author.
One review per account/item is enforced transactionally (duplicates return 409).
Review text is plain text; clients must escape it when rendering. Public metrics
are active installation count, review count and mean rating, not invented backtest
returns. Paid purchases, moderation/abuse tooling, licensing policy, time-windowed
trends and financial performance aggregation are separate product work.

## Configuration and verification

`config/marketplace.json` owns categories, featured IDs, body/text/page limits and
artifact timeout. `MARKETPLACE_CONFIG_FILE` selects it. Inject `DATABASE_URL`,
`AUTH_SERVICE_URL`, `WIDGETS_SERVICE_URL`, `STRATEGY_SERVICE_URL` and the JWT public
key; credentials never belong in JSON. Origins reject credentials, paths, query
strings and redirects. Internal Compose HTTP is development transport; production
workload authentication/TLS and distributed admission quotas remain deployment
requirements. Readiness checks the owned database, not publication dependencies.

```bash
make -C services/marketplace vet test build
make -C services/marketplace docker-build
make up
TEST_DATABASE_URL='postgres://user:password@localhost:5432/test_db?sslmode=disable' make test-integration
API_BASE_URL=http://localhost:8080 MAILPIT_URL=http://localhost:8025 make test-e2e
```

`make init` adds an independent Marketplace database credential without rotating
existing secrets. Compose publishes no Marketplace/DB host port. Fresh migrations
use a dedicated advisory lock and checksum validation; never edit applied SQL.
Integration tests use isolated `marketplace_test_*` schemas, checking concurrent
writes, immutable snapshots, pins, reviews and retention. Gateway E2E tests use
real Auth/Widgets/Strategy/Workspace services and reuse two accounts.
