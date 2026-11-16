# Market Data migrations

This service alone owns `md_*` tables. Lock 71849128 serializes immutable embedded
SQL migrations; lock 71849129 serializes optional TimescaleDB extension/hypertable
setup. Applied checksums must not change; add the next numbered migration.
Ready normalized versions reject updates. Explicit dataset deletion cascades all
its points, versions and jobs; already frozen Lab data belongs to Lab separately.
Integration tests use only isolated `market_data_test_*` schemas.
