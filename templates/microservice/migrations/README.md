# Service-owned migrations

If the service uses a database, add numbered SQL migrations and an embedded
runner using the local `internal/platform/migrate` helper. The Auth migration
package is a repository example of transactions, advisory locks and checksums,
not a runtime dependency to import. Choose a distinct service lock namespace.
Never edit applied migrations; add the next numbered file. Tables belong to
this service: never query another service's database directly.
