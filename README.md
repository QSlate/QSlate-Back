# QSlate-Back

QSlate's quantitative research backend. The current stack includes a Go gateway,
seven independent Go services (Auth, Workspace, Widgets, Strategy, Marketplace,
Lab and Market Data), a Python syntax analyzer, a bounded dataset parser and the
candle-by-candle simulator used by Lab. The original FastAPI prototype is available
in Git history; the current stack uses the Go gateway.

See the [architecture](docs/architecture.md) and
[REST/stream conventions](docs/contracts.md) for implemented boundaries and
remaining work.

## Repository layout

```text
gateway/                  explicit routing, JWT verification and CORS
services/auth/            accounts, sessions, OAuth2, email and API keys
services/workspace/       private dashboards, layouts and widget instances
services/widgets/         catalogue, immutable versions and preview admission
services/strategy/        Python drafts, history, restore and IDE diagnostics
services/marketplace/     publications, pinned installations and reviews
services/lab/             durable backtests, results and a separate Go worker
services/market-data/     versioned imports, TimescaleDB, indexing worker and ticks
simulator/                Python engine and the Lab stdin/NDJSON adapter
templates/microservice/   neutral, runnable service template
cmd/                      development initialization and service generation
docs/                     architecture, OpenAPI and AsyncAPI contracts
go.work                   optional development workspace, not a build dependency
```

Like AREA, each Go service and the gateway own their module, checksums, Dockerfile,
Makefile, configuration and local `internal/platform` helpers. There are no Go
imports between services or local module replacements. Each component builds
outside the monorepo with `GOWORK=off`; the root module contains development tools
and cross-service tests only.

Build independence does not remove functional dependencies: services validate
sessions through Auth's API, Workspace resolves widget versions through Widgets,
Strategy calls its syntax analyzer, and Lab resolves execution snapshots through
Strategy and frozen datasets through Market Data. Services never share application
tables or domain packages.

## Start the development stack

Requirements: Go 1.26 or newer, Docker Compose and Make.

```bash
make up
curl http://localhost:8080/readyz
```

`make up` runs `make init`, creating ignored `.env` credentials and Ed25519
keys under `.secrets/` once. Missing independent database passwords for Workspace,
Widgets, Strategy, Marketplace, Lab and Market Data are appended without rotating existing
values. The gateway binds to `127.0.0.1:8080`; the development Mailpit inbox binds
to `127.0.0.1:8025`. Business services and PostgreSQL have no host ports.

Auth has outbound connectivity for OAuth2/SMTP. Credentials are excluded from
Git and Docker build contexts. Each database uses a private storage network;
Auth alone receives the private signing key. Containers use non-root users,
read-only application filesystems and dropped capabilities.

Lab execution and widget preview are disabled by default. The default stack can
serve their APIs, but it does not silently execute user code without the required
isolation and data configuration.

### Register, log in and refresh

```bash
curl -c /tmp/qslate-cookies -X POST http://localhost:8080/qslate/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"trader@example.com","username":"trader","password":"correct-horse-battery-staple"}'

curl -c /tmp/qslate-cookies -X POST http://localhost:8080/qslate/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"identifier":"trader","password":"correct-horse-battery-staple"}'

curl http://localhost:8080/qslate/v1/users/me \
  -H 'Authorization: Bearer <access_token>'

curl -b /tmp/qslate-cookies -c /tmp/qslate-cookies \
  -X POST http://localhost:8080/qslate/v1/auth/refresh \
  -H 'Origin: http://localhost:3000'
```

Login/register return `user`, `access_token`, `token_type` and `expires_in`
(900 seconds by default). Passwords use bcrypt; hashes are never serialized.
Emails and usernames are normalized to lowercase. Access tokens enforce Ed25519
signatures, issuer, audience and expiration. Private business operations also
validate the live Auth session and resource ownership.

Refresh tokens are opaque, hashed in Auth's database, rotated and stored in an
HttpOnly cookie. Default absolute session lifetime is 30 days. Replay revokes the
session; the frontend must serialize refresh requests and use
`credentials: include`. Application errors use
`{"error":{"code":"...","message":"..."}}`.

### Implemented endpoints

The gateway publishes 74 of the 98 REST operations in the canonical contract,
plus the authenticated backtest and market-tick WebSockets.

| PDF section | Service | Implemented operations |
| --- | --- | --- |
| 2 | [Auth](services/auth/README.md) | 14 account/session/profile/API-key operations and 3 additional OAuth2 routes |
| 3 | [Workspace](services/workspace/README.md) | 11 dashboard, layout and widget-instance operations |
| 4 | [Widgets](services/widgets/README.md) | 7 catalogue/version/preview-admission operations |
| 5 | [Strategy](services/strategy/README.md) | 9 draft, import, history, restore and syntax-diagnostic operations |
| 6 | [Lab](services/lab/README.md) | 11 backtest/result/comparison/optimization operations and one WebSocket |
| 7 | [Marketplace](services/marketplace/README.md) | 11 discovery, publication, installation and review operations |
| 8 | [Market Data](services/market-data/README.md) | 8 catalogue, OHLCV, import, mapping, validation and deletion operations, plus tick subscriptions |

Widget preview dispatches to an external sandbox controller, which is not bundled
or validated here. Strategy's separate Python 3.13 analyzer compiles submitted
text without executing it; a successful syntax check is not permission to run it.

Lab connects the Python simulator imported from branch #18 through a separate Go
worker and durable PostgreSQL queue. Strategy versions, dataset bytes and effective
parameters are frozen before enqueueing. Execution requires a configured
`runsc` daemon, simulator image, ready worker and a ready owned dataset version
from Market Data. Standalone Lab can use an operator-managed CSV catalogue when
no Market Data origin is configured. See
[Lab setup and limitations](services/lab/README.md#configure-and-start).

Market Data stores uploaded CSV/Parquet bytes and immutable normalized versions
in its own database. Its worker validates mappings and indexes OHLCV points into
TimescaleDB. Live ticks and provider history require a configured trusted feed
adapter; the default stack does not fabricate market prices. See the
[Market Data guide](services/market-data/README.md) for import and feed setup.

Other contract domains remain planned. Frontend
integration, MFA, explicit account linking and Runner API-key admission are not
implemented. Proposed paths/payloads reconstructed from the PDF are identified
in the contracts rather than presented as verbatim PDF requirements.

### Configure OAuth2 without changing code

Edit [providers.json](services/auth/config/providers.json): provider URLs, scopes,
claim mapping, callback and names of credential environment variables. Copy
`.env.oauth.example` to ignored `.env.oauth`, supply secrets, enable the provider
and restart Auth:

```bash
docker compose up -d --force-recreate auth
```

Compose mounts the settings read-only; outside Docker, use
`OAUTH_PROVIDERS_FILE`. Providers are disabled by default. Authorization Code +
PKCE S256 uses single-use state and an authenticated userinfo response. Tokens
never appear in redirect URLs; the frontend calls refresh after the callback.
Matching email addresses never trigger automatic account linking. Providers with
a different identity protocol require an explicit adapter.

## Development and verification

Run root targets from the repository root:

```bash
make test                       # race-enabled tests across all Go modules and analyzer tests
make vet
make build                      # binaries in each component's bin/ directory
make test-standalone             # independent module builds outside the repository
make simulator-test             # fixed, synthetic Python engine fixtures in Docker
make test-engine                # real Go-to-Python transport with trusted fixtures
make market-parser-test         # real CSV/Parquet parser fixtures in a test image
API_BASE_URL=http://localhost:8080 MAILPIT_URL=http://localhost:8025 make test-e2e
make new-service NAME=example-domain
make -C services/market-data test build
make logs
make down                       # stop the stack, retaining PostgreSQL volumes
```

A root `go test ./...` alone does not include child modules. The root Makefile
iterates over them explicitly. `simulator-test` and `test-engine` are opt-in Docker
checks using only repository-owned fixtures; they do not validate a production
runsc deployment.

`make test` invokes `python3` for the syntax analyzer fixtures. Use Python 3.13
to match the analyzer's shipped container image and resource limits.

Follow the [template guide](templates/microservice/README.md) when adding a domain,
use cases, adapters, contracts and public routes. Handlers do not query SQL
directly; services never share repositories or domain entities.

From an individual component directory:

```bash
cd services/strategy
make test build                 # GOWORK=off and service-owned dependencies
make docker-build               # Docker context limited to this directory
```

Security fixes to copied helpers must be propagated and tested explicitly in each
affected module. Updating the template does not silently update existing services.

PostgreSQL integration tests require an accessible development database:

```bash
TEST_DATABASE_URL='postgres://user:password@localhost:5432/database?sslmode=disable' make test-integration
```

Tests create/drop only their own `auth_test_*`, `workspace_test_*`,
`widgets_test_*`, `strategy_test_*`, `marketplace_test_*`, `lab_test_*` or `market_data_test_*` schemas.
Never point them at a production database. SQL migrations are embedded and applied
at startup under a transaction lock, with checksum verification. Add numbered
migrations instead of editing applied files.

Market Data repository tests can also exercise the actual parser and Timescale
hypertable: set `TEST_PARSER_URL` to a private test parser and
`TEST_TIMESCALE=true` with a Timescale-enabled test database. The standalone parser
test image requires no host test-folder mounts.

For local Go processes, export each component's `.env.example` settings and
provide its own reachable database and key paths. Go does not load dotenv files
automatically. Run `make run` from that component's directory; default configuration
paths are relative to it. HTTP dependencies remain explicit, not shared Go code.

### Configuration reference

| Variable | Component | Default / requirement |
| --- | --- | --- |
| `HTTP_ADDR` | Go HTTP components | Gateway 8080, Auth 8081, Workspace 8082, Widgets 8083, Strategy 8084, Marketplace 8086, Lab 8087, Market Data 8088 outside Docker; 8080 in Compose |
| `AUTH_SERVICE_URL` | Gateway and business services | Required for routing or live-session validation |
| `WORKSPACE_SERVICE_URL` | Gateway | Required by the shipped route file |
| `WIDGETS_SERVICE_URL` | Gateway, Workspace, Marketplace | Catalogue routing, version resolution or publication snapshots |
| `STRATEGY_SERVICE_URL` | Gateway, Marketplace, Lab | Strategy routing, publication or execution snapshots |
| `MARKETPLACE_SERVICE_URL` | Gateway; optional in Widgets/Strategy | HTTP(S) origin; localhost:8086 outside Docker |
| `LAB_SERVICE_URL` | Gateway | HTTP(S) origin; localhost:8087 outside Docker |
| `MARKET_DATA_SERVICE_URL` | Gateway, Lab | Market Data origin; localhost:8088 outside Docker; enables owner-authorized dataset snapshots in Lab |
| `MARKET_DATA_CONFIG_FILE` | Market Data API/worker | `config/market-data.json`; limits, origins, Timescale requirement and trusted feed settings |
| `MARKET_DATA_PARSER_URL` | Market Data API/worker | Private parser origin; localhost:8089 outside Docker |
| `MARKET_DATA_FEED_URL` / `MARKET_DATA_FEED_TOKEN` | Market Data API | Optional trusted feed adapter; copy `.env.market-data.example` to ignored `.env.market-data` |
| `MARKET_DATA_NETWORK_PREFIX` | Compose | First three IPv4 octets for five /28 networks; default `10.249.76`; choose a free prefix if needed |
| `ROUTES_FILE` | Gateway | `config/routes.json` locally; `/app/routes.json` in the image |
| `WORKSPACE_LAYOUT_FILE` | Workspace | `config/layout.json` locally; `/app/layout.json` in the image |
| `WIDGETS_CONFIG_FILE` | Widgets | `config/catalogue.json` locally; `/app/catalogue.json` in the image |
| `STRATEGY_CONFIG_FILE` | Strategy | `config/strategy.json` locally; `/app/strategy.json` in the image |
| `STRATEGY_ANALYZER_URL` | Strategy | Analyzer origin; HTTP explicitly enabled only for the local private network |
| `MARKETPLACE_CONFIG_FILE` | Marketplace | `config/marketplace.json` locally; `/app/marketplace.json` in the image |
| `LAB_CONFIG_FILE` | Lab API/worker | `config/lab.json` locally; `/app/config/lab.json` in Compose |
| `LAB_DOCKER_SOCKET` / `LAB_DOCKER_GID` | Opt-in Lab worker | Dedicated execution-daemon socket path and access group; never mounted into the API |
| `*_DATABASE_PASSWORD` | Compose | Independent per-service credentials, added by `make init` without rotation |
| `DATABASE_URL` | Each stateful service; Lab worker | Service-owned database; choose environment-appropriate TLS settings |
| `JWT_PRIVATE_KEY_FILE` | Auth only | `.secrets/auth-private.pem`; inject the actual runtime path |
| `JWT_PUBLIC_KEY_FILE` | Gateway and business APIs | `.secrets/auth-public.pem`; inject the matching public-key path |
| `JWT_ISSUER` / `JWT_AUDIENCE` | JWT issuers/verifiers | `qslate-auth` / `qslate-api` |
| `ACCESS_TOKEN_TTL` | Auth | `15m`, between `1m` and `1h` |
| `REFRESH_TOKEN_TTL` | Auth | `720h`, longer than the access token, at most `2160h` |
| `COOKIE_SECURE` | Auth | `true`; disabled only for local HTTP in Compose |
| `ALLOWED_ORIGINS` | Gateway/Auth | Comma-separated list; default `http://localhost:3000`; Lab origins are in its JSON config |
| `OAUTH_PROVIDERS_FILE` | Auth | `config/providers.json` locally; `/app/providers.json` in the image |
| `PUBLIC_APP_URL` | Auth | `http://localhost:3000`; fixed base for email links and frontend callback |
| `SMTP_ADDR` / `SMTP_FROM` | Auth | `localhost:1025` / `noreply@qslate.local`; Mailpit in Compose |
| `SMTP_TLS_MODE` | Auth | `starttls`; alternatives `tls` or local-only `plain` |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | Auth | Optional together; require TLS when supplied |
| `BCRYPT_COST` | Auth | `12`, between `10` and `14` |

Development key mounts use the UID/GID generated in `.env`. Deployments must inject
keys with permissions for the container user and manage their lifecycle through
a secret manager.

## Historical prototype

The removed root FastAPI/runner files can be inspected in Git history. They are
not part of the current stack. Use the [Lab guide](services/lab/README.md) and
[simulator guide](simulator/README.md) for supported backtesting commands.
