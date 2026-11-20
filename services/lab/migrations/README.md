# Lab migrations

Lab owns its database and all `lab_*` tables. Both API and worker apply embedded,
numbered immutable migrations through the local platform runner. Advisory lock
71849127 serializes replicas; checksums reject changes to applied migrations.
Add a new numbered migration for schema evolution. No inter-service SQL access.
