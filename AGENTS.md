# Instructions for agents working on Comitia

Read [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/architecture.md](docs/architecture.md) first.

- Never add exported methods or fields to types in `internal/model`: `pkg/comitia` aliases them, so they become public API.
- Never build the binary into the repository root: use `go build -o <path outside the repository> ./cmd/comitia`.
- Keep the SQLite driver out of `pkg/comitia`, `pkg/httpapi` and `pkg/mcp`; only `pkg/sqlite` links it.
- Do not edit the frozen fixtures: `pkg/httpapi/internal/legacy/` and `internal/service/testdata/legacy_sqlite_messages.go`.
- Change the public API only on purpose, with `pkg/comitia/testdata/api_surface.golden` updated in the same commit.
