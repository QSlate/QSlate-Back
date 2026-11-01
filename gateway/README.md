# API gateway

Independent Go module, Docker image and configuration. Build/test this folder
alone with `make test build` and `make docker-build`; all use `GOWORK=off`.
Its technical helpers are local to `internal/platform`, not imports of Auth or
another service. There is no application database or private signing key here.

`ROUTES_FILE` defaults to `config/routes.json` from this folder and `/app/routes.json`
in the image. Each method/path is explicitly mapped to an environment-configured
upstream; unknown/unimplemented endpoints are not forwarded. The shipped file
publishes 44 operations across Auth/OAuth, Workspace, Widgets and Strategy.

For local execution export `.env.example` with the public key path and reachable
upstream API origins, then `make run` (default 8080). Go does not load `.env` itself.
Root `make up` orchestrates the development services and injects their origins.
HTTP upstream dependencies do not prevent independent compilation or deployment
with a different route file. Readiness checks the configured upstreams.

The gateway verifies private-route JWT signatures/issuer/audience/expiry and
strips client identity/forwarded headers. Services still validate live sessions
and resource ownership themselves. CORS and sensitive-Auth rate limits remain
enforced; per-instance in-memory limits are not distributed production quotas.
Do not publish internal catalogue/analysis APIs through route configuration.
