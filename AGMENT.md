# Project Context

This repository is built on top of two upstream/reference projects:

- `CLIProxyAPI/`
- `sub2api/`

These two directories are reference projects used to compare behavior, reuse ideas,
and track upstream implementation changes. They should be treated as external
reference code rather than primary source code for this repository.

## Maintenance Notes

- Periodically pull the latest code for both `CLIProxyAPI/` and `sub2api/`.
- When upstream behavior changes, compare the relevant implementation with this
  project before porting changes.
- Keep local changes in the main project scoped and intentional.
- Avoid mixing unrelated updates from the reference projects into normal feature
  work unless the task is specifically about syncing or adapting upstream changes.

## Working Assumption

The main project in this repository depends on concepts and implementation
patterns from `CLIProxyAPI/` and `sub2api/`, but it remains its own codebase.
Use those projects as references when investigating compatibility, behavior,
protocol handling, or implementation details.
