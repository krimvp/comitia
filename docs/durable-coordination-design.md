# Durable coordination design (issue 65)

C15: retain the terminal watch and its automatic acknowledgements. Add full
JSON-lines output and explicit manual acknowledgement mode. A durable consumer
uses a distinct cursor, commits business effects keyed by context/message ID,
then acknowledges. Printed output and presence never establish business completion.

C25: add an optional MessageQueryAPI for authorized context-scoped lookup, plus
reply_to (direct replies), thread_id (root and descendants) and from_session
(authenticated author) filters. SQLite uses a new indexed reply projection table,
backfilled and maintained by triggers; no exported interface gains methods.
Scoped reads require an optional store capability rather than silently widening
on an old custom store. Endpoint DTO fields are omitted when unset; new HTTP
clients probe scoped-query support before sending filters to servers that might
ignore unknown fields. Old clients continue using the existing routes.

Requests, receipts, control and certificates are versioned message schemas.
Request messages persist expected authenticated responder session IDs and an
absolute deadline. Receipt messages correlate via envelope.reply_to, distinguish
accepted/completed/rejected, and carry durable result references. The service
checks responder identity and correlation in the append transaction; a deadline
expires eligibility for new receipts, while identical deduplicated retries remain
recoverable. There is no timer, remote process launch or automatic task transition.

C26: optional exact-SHA merge certificate and merge receipt schemas support an
external gate. A pure verifier checks authenticated role assignments, request
correlation, artifact/head identity, issuance/expiry and approvals. The external
gate persists one-shot reservation before GitHub mutation, uses expected-head
preconditions, then reconciles ambiguous results against GitHub and appends a
receipt. Comitia cannot make a GitHub operation atomic with its message log.

C27: external launcher owns a durable, secret-free process registry keyed by
launch ID (host/boot ID, PID plus start identity, harness run, worktree, worker and
watcher instances, checkpoint deadline). Typed control requests and semantic
receipts establish steering acknowledgement. Presence alone never justifies
reclaiming or killing a process; watchers and workers use separate cursors.

Regression coverage will exercise output failure/crash replay, scoped lookup and
queries/authentication/migration, request correlation/deadlines/retries, certificate
invalidation and control receipts, published descriptor acceptance, old interface
compile fixtures and both HTTP upgrade orders. Launch and GitHub execution remain
external and are documented as such.
