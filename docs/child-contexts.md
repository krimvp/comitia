# Child contexts

A session holding the `create` permission on a context can open a private child
context for its own sub-team, for example one task of a larger epic:

```sh
go build -o bin/comitia ./cmd/comitia
./bin/comitia context-create --parent-id "$epic_id" --task-id "$task_id" --name task-15
./bin/comitia context-children --parent-id "$epic_id"
```

The creator gets `read,write,claim,grant,create` on the child. That grant hangs
off the creator's grant on the parent and expires no later than it, so revoking
the parent grant (or the creator's session) also revokes the creator's and its
delegates' access to the child. Children are private by default: members with
`read` on the parent see only metadata (ID, name, creator session, `TaskID`) via
`context-children` or `POST /v1/contexts/children`, and need their own grant to
read messages or tasks. Pass `--parent-readable` to let parent readers also read
(never write) the child; such readers can ack and keep their own cursor, but
cannot be addressed in `recipient_session_ids`, which needs a direct grant on
the child. Children readable only through the parent appear in
`context-children`, not in `context-list`. The creator's child permissions do
not depend on its parent permissions: `read,create` on the parent is enough to
get the full creator set on the child. Only administrators create top-level contexts with
`--workspace-id`. `create` is part of `all`; delegate it explicitly with
`--permissions read,write,create`. Grants stored as `all` before `create`
existed do not include it. Nothing is relayed between contexts automatically.

Top-level contexts have depth **0**. The default maximum depth is **3** (three
child levels), and each creating grant can create at most **20** children.
Archived children still count toward that grant's lifetime quota. Configure the
server with `comitia serve --max-context-depth 3 --max-child-contexts-per-grant 20`,
or set `Config.MaxContextDepth` and `Config.MaxChildContextsPerGrant` in embedded
applications. These flags apply only to `serve`; embedded CLI commands and MCP
use the defaults, even against the same database. Remote clients use their
server's limits. Config zero values use the defaults; negative values and
nonpositive serve flags are invalid. Limits are checked atomically at creation;
reducing them does not remove existing contexts or prevent reading deeper trees.
Creation uses the longest-lived live `create` grant with remaining quota. A holder
of `grant,create` can delegate `create`, giving each new grant its own budget;
the count bounds sprawl per grant, rather than per coordinator or delegation tree.

A holder of `grant` on a context, or an administrator, can archive it:

```sh
./bin/comitia context-archive --context-id "$context_id"
```

The library method is `ArchiveContext(ctx, credential, contextID)` and the HTTP
route is `POST /v1/contexts/archive` with `{"context_id":"..."}`. Archiving is
idempotent and preserves its original timestamp. Archived contexts reject
messages, task creation/claim/renewal/completion, references, grants (including
delegated grants), and new children with `ErrInvalid` / HTTP 400 and a clear
reason. Context metadata exposes `Archived` and nullable `ArchivedAt` through
get, list, and child listing. Replay, task reads, listing, and reader acknowledgement
cursors keep working for sessions that still have access. Administrator status
alone does not grant read access, even after archiving.

A child also counts as archived as soon as its **original creator grant** becomes
ineffective, whether that grant or a parent grant is revoked/expired, or a grant
owner/issuer session is revoked/expired. This state is computed on access, so no
background job is needed; `ArchivedAt` is the first time access ended. Another
grant to the creator does not extend that original lifetime. Independently
granted members can still read; members whose access hangs off the creator's
revoked/expired grant lose access as usual. Explicitly archiving a parent does
not archive its children or revoke existing grants. Archival cannot be undone.

Custom stores must also implement the context lifecycle methods on
`ContextReadStore` and `ContextWriteStore`, in addition to
`ChildContexts(parentID, afterID, limit)`.
