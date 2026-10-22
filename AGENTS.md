# Repository collaboration preferences

## Commits and pushes

- Commit and push only when the user requests it; do not publish implementation work automatically.
- Write commit subjects and bodies in English. Prefer conventional commit subjects.
- Split work into focused, coherent commits by responsibility, including related tests and configuration. Do not create a single catch-all commit for independent changes.
- Order commits by dependency and keep each intermediate revision buildable and testable where practical.
- For batches of commits, the user prefers explicitly spaced, chronological author and committer timestamps. Keep dates after the parent commit and no later than the present; use the requested date range when provided, otherwise a same-day interval. Do not rewrite published history without explicit authorization.
- Adjusted commit dates are metadata, not evidence of when work occurred. The remote server's push time is independent and cannot be changed through Git commit dates.
- Before pushing, check the branch, remote, staged file list, tests, and absence of secrets. Never force-push unless explicitly authorized.
