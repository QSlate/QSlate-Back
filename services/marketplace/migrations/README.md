# Marketplace migrations

`001_marketplace.sql` owns listings, immutable source snapshots, active
installations, retained per-user pins and reviews. No external service tables
or credentials are used. Migration lock: `71849126`.

Checksums are verified under the migration lock. Add a new numbered migration
instead of changing applied SQL. Soft unpublication/uninstall retains snapshots;
this is not an erasure policy. Tests create/drop only `marketplace_test_*` schemas.
