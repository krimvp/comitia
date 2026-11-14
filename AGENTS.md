# Instructions for agents working on Comitia

Read [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/architecture.md](docs/architecture.md) first.

- Never add exported methods or fields to types in `internal/model`: the root aliases them, so they become public API.
- Never build the binary into the repository root: use `go build -o <path outside the repository> ./cmd/comitia`.
- Keep the SQLite driver out of the root, `httpapi` and `mcp`; only `sqlite` links it.
- Do not edit the frozen fixtures: `httpapi/internal/legacy/` and `internal/service/testdata/legacy_sqlite_messages.go`.
- Change the public API only on purpose, with `testdata/api_surface.golden` updated in the same commit.
