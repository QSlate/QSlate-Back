# Auth service

Independent Go module for the 14 account/session/profile operations and three
file-configured OAuth2 operations. This service alone owns account data, private
Ed25519 signing keys and transactional email state. Public API contracts are
documented in `../../docs/auth.openapi.yaml`; clients use the gateway prefix.

```bash
make test build                  # from this folder; GOWORK=off
make docker-build                # this folder is the entire build context
```

Copy/export `.env.example` values for local execution, supply your own reachable
Auth-owned PostgreSQL `DATABASE_URL`, matching signing key and SMTP settings,
then `make run` (default 8081). Go does not load dotenv files automatically.
For the complete development stack use `make up` from the repository root;
it creates ignored development credentials without rotating existing values.

`OAUTH_PROVIDERS_FILE` defaults to `config/providers.json` from this folder and
`/app/providers.json` in the image. Provider URLs/scopes/claim mappings and names
of credential environment variables are file-configured. Providers are disabled
until explicitly configured. Root Compose optionally injects ignored `.env.oauth`.
Password and OAuth flows issue the same active sessions and refresh cookies.

The module owns its HTTP/crypto/SQL helpers under `internal/platform`. There are
no imports of sibling services, root runtime libraries or local `replace` paths.
Updating another service/template does not rebuild Auth implicitly. Signing keys
are injected at runtime and never copied into images or source control.

Migrations preserve the existing schema/checksums and advisory-lock namespace.
Never edit applied SQL; add the next migration. Tokens enforce issuer, audience,
expiry and Ed25519 signatures. Refresh rotation/replay checks, logout/reset,
encrypted email outbox, OAuth PKCE/state and private profile/API-key scopes are
unchanged by the module split. Readiness checks Auth's own dependencies.

```bash
TEST_DATABASE_URL='postgres://user:password@localhost:5432/test_db?sslmode=disable' make test-integration
```

Use development databases only; integration tests create/drop their own schemas.
Root E2E tests verify Auth with its HTTP consumers rather than sharing Go models
or SQL. MFA, explicit account linking and Runner API-key admission remain separate work.
