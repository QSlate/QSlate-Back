# Strategy-owned migrations

SQL files are embedded and applied transactionally with the service-local checksum/
advisory-lock runner, using Strategy's own database and lock namespace 71849125.
Never edit an applied migration; add the next numbered file instead.

`001_strategies.sql` creates private strategy metadata, immutable version snapshots,
same-strategy/current-revision foreign keys and an update/delete rejection trigger.
DELETE is archival; historical source retention is intentional, not an erasure API.
No Auth, Workspace, Widgets or future Lab SQL queries belong in this schema.
