# QSlate microservice template

Create a service from the repository root: `make new-service NAME=example-domain`.
The generator copies this folder, rewrites its module/imports and refuses to
overwrite an existing service. Private `.env` files, keys, AGENTS instructions
and local binaries are never copied. Configuration examples are retained.

The generated folder owns its `go.mod`, `go.sum`, `Dockerfile`, `Makefile`,
`.dockerignore`, configuration and technical helpers. It requires no root or
sibling source and builds outside the monorepo:

```bash
cd services/example-domain
make test build
make docker-build
make run
```

Targets enforce `GOWORK=off` and `-mod=readonly`. The repository workspace is
optional: `go work use ./services/example-domain` registers the service for IDE use.
The root Makefile discovers `services/*/go.mod` for tests/builds. The generator
does not implicitly change Compose or public routes.

Run the unmodified template with `make -C templates/microservice run` from the
root. It exposes `/healthz` and `/readyz` on port 8082 locally, 8080 in the image.
These probes require neither a database nor a fabricated user identity.

Add entities/interfaces to `internal/domain`, use cases to `internal/service`,
SQL to `internal/repository` and handlers to `internal/http`. The entry point
assembles dependencies. `internal/platform` contains tested local helpers for
HTTP, configuration, JWT, live sessions, PostgreSQL and migrations; use only what
the domain needs. Helpers contain no business rules or imports of another service.
Changing the template does not automatically update existing copies.

Every private business route must verify the JWT and live Auth session, then
check resource ownership in its service. The gateway never signs tokens. For
persistence, embed service-owned migrations and use a distinct advisory-lock
namespace. Auth consumers receive only the public key, never the signing key.

For deployment, add the OpenAPI contract and a root Compose service with
`build.context: ./services/example-domain`, its own networks/credentials, then
explicit routes in `gateway/config/routes.json`. Never publish an endpoint
before its implementation and authorization tests exist.

Keep standalone unit tests and, when SQL is used, integration tests in
`internal/repository`. Cross-service tests stay at the root. `make test-standalone`
checks builds outside the repository; `make test` iterates over every module with
its own dependencies. Keep generated documentation in English without parallel
French-language copies.
