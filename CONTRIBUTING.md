# Contributing to Comitia

This guide is for anyone changing the code, person or agent. Read
[the architecture](docs/architecture.md) for the boundaries and the
[code map](docs/code-map.md) for where things live. Report security problems as
[SECURITY.md](SECURITY.md) describes, not in a pull request.

## Prerequisites

- Go 1.24 (`go.mod`). Some tests run `go` with `GOTOOLCHAIN=go1.24.13`, so the
  first run downloads that toolchain unless it is cached.
- Python 3; the installer's TOML handling and its tests need 3.11 or newer. The
  scripts use the standard library only.
- Node 22 for the inspector, landing page and world viewer tests; Playwright with
  Chromium for the browser tests (installed per directory, see below).
- `git` and `jq` (the executable recipes in `scripts/test_docs_onboarding.py`
  skip without `jq`).

## Build

Always build to a path outside the repository root; a bare
`go build ./cmd/comitia` writes a `comitia` binary into the root:

```sh
go build -o "$HOME/bin/comitia" ./cmd/comitia
```

CI and `scripts/comitia_install.py` stamp the source revision into the build, so
`comitia version` and `server_info` can tell builds apart:

```sh
dirty=false
test -z "$(git status --porcelain --untracked-files=all)" || dirty=true
go build -ldflags "-X github.com/krimvp/comitia/pkg/comitia.Revision=$(git rev-parse HEAD) -X github.com/krimvp/comitia/pkg/comitia.Dirty=$dirty" \
  -o "$HOME/bin/comitia" ./cmd/comitia
```

`-X github.com/krimvp/comitia/pkg/comitia.Version=...` sets a release label. Code reads the
stamp through `BuildVersion` and `BuildIdentity`, never in a package-level initialiser.

## Test

CI (`.github/workflows/ci.yml`) runs these in order, the browser tests in jobs of
their own; the durations are rough, on a four-core machine:

| Step | Command | Time |
|---|---|---|
| Formatting | `test -z "$(gofmt -l .)"` | instant |
| Node tests | `node --test httpapi/ui-tests/*.test.mjs site/*.test.mjs tools/world-viewer/*.test.mjs` | 3 s |
| Vet | `go vet ./...` | seconds |
| Python tests | `python3 -m unittest scripts/test_*.py` | 1 min |
| Go tests | `go test -race -timeout 20m ./...` | 4 min |
| Repeated checks | `go test -race -count=10 -run 'TestCrashRecovery\|TestSecurity\|TestSQLite' . ./internal/service ./internal/sqlstore` | 1.5 min |
| Stress | `python3 scripts/stress_agents.py --binary "$PWD/dist/comitia" --workers 12 --messages 100`, three times | 10 s each |
| Browser tests | `node --test httpapi/ui-tests/browser/*.test.mjs`; `node --test site/browser/*.test.mjs` | 1 min; 10 s |

The repeated step fails if its pattern selects fewer than 30 tests; rename such
tests with care. Before the browser tests, install their dependencies once:

```sh
npm ci --prefix httpapi/ui-tests/browser   # likewise site/browser
httpapi/ui-tests/browser/node_modules/.bin/playwright install --with-deps chromium
```

The inspector browser tests need a server binary in `COMITIA_BINARY`, built
outside the repository. A fast local loop while you work:

```sh
gofmt -l . && go vet ./...
go test -run 'TestName' ./internal/service   # the package you changed
go test .                                    # the structure guards, seconds
```

Run the whole sequence before you ask for review.

## Layout

- `.` (root): the public API only, aliases and forwarders over `internal/`.
- `internal/model`, `internal/schema`, `internal/sqlstore`, `internal/service`,
  `internal/mergegate`, `internal/evaluation`: the implementation, in layers.
- `internal/httpserve`: HTTP logging and the serve loop of the CLI and MCP.
- `cmd/comitia`, `mcp`, `httpapi`: the adapters. `sqlite`: the driver and
  embedded stores. `storetest`: store conformance checks.
- `httpapi/ui`: the inspector. `site`: the landing page. `tools/world-viewer`:
  an experimental viewer.
- `scripts`: installers, clients and CI tooling ([scripts/README.md](scripts/README.md)).
  `deploy`: a past single-host deployment. `skills/comitia`: the agent skill.
- `docs`: [the documentation](docs/README.md). `examples`: the compiled embedded program and evaluation fixtures.

## Recipes

Each recipe lists every place the last such addition touched. Read an existing
example side by side: the one named in each heading.

### Add an MCP tool (like `fail_task`)

1. The API method: on `API` or an optional interface in `internal/model/<domain>.go`,
   implemented by `*Service` in `internal/service/<domain>*.go` and by the HTTP
   `Client`, so the tool works against a remote server too. A forwarder or
   alias in the root facade if it is new public API (see the last recipe).
2. In `mcp/tools_<domain>.go`: a `tool(name, description, schema)` entry in the
   group's list, a case in its `call<Domain>` switch and a handler that decodes
   the arguments (`mcp/args.go`) and calls the API (a shared handler may get
   its own file, as `mcp/task_outcomes.go`). Derive limits in descriptions from
   the exported constants. Its behaviour tests go in `mcp`.
3. Its operation name in `operationNames` (`internal/model/operations.go`), keyed
   by its HTTP route; `mcp/operation_names_test.go` fails without it.
4. An input for it in `seedSchemaConformance` (`mcp/schema_conformance_test.go`);
   `TestAdvertisedSchemasAcceptedArguments` fails for an advertised tool without one.
   Add the method to `fakeAPI` (`mcp/server_test.go`) if the interface grew.
5. A new capability also gets a feature in `serverFeatures`
   (`internal/model/server_info.go`), so stale MCP clients are reported.
6. Document it in its reference document and, where agents use it, the skill
   references; the docs test checks that tool names in `skills/comitia` exist.

### Add an HTTP route (like `tasks/fail`)

1. Server half: an entry in the domain's route table (`taskRoutes` in
   `httpapi/server_tasks.go`; `endpoints` in `server.go` merges the tables),
   usually through `typedEndpoint`.
2. Request and result types in `httpapi/wire.go`, with snake_case JSON names.
   Requests reject unknown members, so a new field needs the client fallback below.
3. Client half: a method in `httpapi/client_<domain>.go` calling `c.call` with
   the route. When an older server answers `not_found` or rejects a new field,
   return a dedicated `Err...Unsupported` error as `FailTask` does; an exported
   error variable also goes into `errorTexts` in `surface_test.go`.
4. The operation name in `operationNames`; `httpapi/operation_names_test.go`
   walks every route and fails on one labelled `unknown`.
5. Tests: the behaviour in `httpapi`, and the upgrade orders against the frozen
   client and handler in `httpapi/internal/legacy` (`compatibility_test.go`).
   `Bootstrap` is never routed.
6. The route in `docs/local-daemon.md` or the reference document of its domain.

### Add a CLI command (like `task-fail`)

1. Dispatch: a case in the domain's group function (`runTasks` in
   `cmd/comitia/cmd_tasks.go`), which `dispatch` reaches through
   `commandGroups` (`main.go`). A new group is added to `commandGroups`.
2. Flags: `newFlags(cmd)`, `parseFlags`, `requireFlags`; return before any API
   call when `cfg.parseOnly` is set, since `run` checks flags before opening
   storage. Commands that take instance defaults are listed in `usesInstanceDefaults`.
3. A line in `commandUsage` (`cmd/comitia/usage.go`). `scripts/test_docs_structure.py`
   reads it to check the commands and flags the skill names, and
   `positional_args_test.go` and `required_flags_test.go` walk it (classify the
   command's required flags there). Add the command to the list in `help_test.go`;
   its behaviour tests go beside it (`task_outcomes_test.go`).
4. Errors map to exit codes in `exitCode` (`exit.go`); return the root's error
   classes rather than new ones.
5. Document it in `docs/USING-COMITIA.md` or the reference document of its domain.

### Add a built-in message schema (like `comitia.stance.v1`)

1. The ID constant in `internal/model/schema_ids.go` and its re-export in the
   root `messages.go` (a public API change: update the golden).
2. The registry entry in `builtinSchemas` (`internal/schema/registry.go`): one
   `add` call with the validator, the payload JSON Schema, the body, task and
   artifact rules, requirements and examples. The validator and payload type
   live in `internal/schema`.
3. If the schema requires `reply_to`: the check in `normalizeEnvelope`
   (`internal/service/messages_envelope.go`), a `ReplyTo` case for its examples
   in the `switch id` of `builtinSchemas`, and, so a test can append its
   example, a case in `materializeProtocolExample`
   (`internal/service/coordination_protocol_test.go`) creating what it refers to.
4. Tests: `TestMessageSchemaPayloadConformance`
   (`internal/service/message_schemas_conformance_test.go`) checks the published
   schema against the validator on the examples and on samples you add to
   `messagePayloadSemanticCorpus`; every rule JSON Schema cannot express must be
   stated in the requirements. Behaviour goes in `message_schemas_test.go`.
5. If older servers reject the schema, add it to the fallback in
   `(*Client).AppendMessage` (`httpapi/client_messages.go`) and its tests in
   `httpapi/compatibility_test.go` and `messages_test.go`. The inspector previews
   known fields through `previewFields` in `httpapi/ui/model.mjs`, tested in
   `httpapi/ui-tests`.
6. The schema table in `docs/message-protocol.md`, the only place schemas are
   tabulated (the docs test enforces it), and the skill references if agents send it.

### Add a store capability or migration (like `TaskFilterStore`)

1. A capability is an optional interface in `internal/model`, aliased in the root
   `store.go` (update the golden), implemented by `*sqlStore` in `internal/sqlstore`. The service
   type-asserts it and keeps a fallback for stores without it
   (`internal/service/tasks_filters.go`); assert the implementation in a test.
2. Schema changes are idempotent DDL in the domain's constant in
   `internal/sqlstore`, listed in `schemas` (`migrations.go`). Data derived
   from history is a `backfill`: its generation is the hash of its SQL, so
   editing the SQL reruns it; list an equivalent earlier run in `legacy`. Bump
   `sqliteSchemaVersion` when older binaries must not write through the change.
3. Older binaries still write to newer files. The legacy-writer test
   (`internal/service/messages_legacy_writer_test.go`) builds the CLI with
   `internal/sqlstore/messages.go` replaced by the frozen
   `internal/service/testdata/legacy_sqlite_messages.go`: keep what other
   store files need out of `messages.go`, and add any new package directory the
   CLI imports to the patterns that test copies.
4. Semantics every external store must share go in `storetest` (today
   `storetest.Grants`), run against SQLite from the root `store_conformance_test.go`.
   Migration tests are in `internal/sqlstore` and `internal/service/sqlite_*_test.go`.

### Change the public API deliberately

1. Edit the facade file of the domain in the root (or `httpapi`, `mcp`,
   `sqlite`, `storetest`). Do not add exported methods or fields to types in
   `internal/model`: the root aliases them, so they become public API.
2. Run `go test -run TestPublicAPISurface -update .` and commit
   `testdata/api_surface.golden` in the same commit; its diff is the change
   reviewers read. A new exported error variable goes into `errorTexts` first, any
   other exported variable into `varValues` (or `assignedVars`, with the reason).

## Rules

- **Wire and database compatibility.** Either upgrade order of `/v1/` client
  and server must work: `httpapi/compatibility_test.go` and its neighbours run
  current code against the frozen `httpapi/internal/legacy`, and
  `legacy_response_test.go` reads old response names. New request members are optional, responses keep
  legacy names unless the canonical wire is negotiated, and clients ignore
  unknown members. Older binaries keep writing to upgraded databases; the
  legacy-writer and migration tests protect that. Do not edit the frozen fixtures.
- **Test at the strongest boundary:** behaviour through `*Service`, a real
  SQLite store, a real HTTP server or a CLI process, not private helpers.
- **A regression test fails before its fix.** Check that it does.
- **Few dependencies.** Two direct Go modules today: the SQLite driver and a JSON
  Schema validator. The driver is linked only through `sqlite`; the root,
  `httpapi` and `mcp` must not link it (`import_boundary_test.go`). Scripts use
  Python's standard library; Playwright is the only Node dependency. Ask before
  adding one.
- Keep documents within their word budgets (`WORD_BASELINE` in
  `scripts/test_docs_structure.py`); a new document gets a reviewed budget.
