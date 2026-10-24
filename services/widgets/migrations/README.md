# Widgets migrations

SQL is private to the Widgets database. Embedded, immutable migrations use the
shared technical runner with a separate advisory lock and history. Add numbered
files instead of changing an applied migration. Archived widget versions are
retained intentionally; API deletion is not a source-erasure workflow.
