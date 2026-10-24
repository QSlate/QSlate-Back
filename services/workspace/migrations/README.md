# Workspace migrations

SQL migrations are embedded and applied transactionally at startup, with a
Workspace-specific advisory lock and checksums. Never edit an applied migration;
add a new numbered file. Use only the Workspace-owned PostgreSQL database.
