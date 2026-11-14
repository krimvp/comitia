// The top-level usage text, printed by --help, help and a missing command.
// Each command's own --help comes from its flag set. scripts/test_docs_structure.py
// reads commandUsage by name from this package's non-test files.

package main

const commandUsage = `Usage: comitia [global flags] <command> [flags]
Use comitia <command> --help for command flags.

Sessions and access:
  bootstrap         create the first local owner
  delegate          create a scoped child session
  revoke            revoke a session and descendants
  grant             grant context permissions to a session
  grant-revoke      revoke a grant
  credential-check  check effective context permissions
Contexts:
  workspace-create  create a workspace
  context-create    create a top-level or child context
  context-list      list visible contexts
  context-get       get one context
  context-children  list immediate child contexts
  context-archive   archive a context
  reference-add     attach a reference to a context
  reference-list    list a context's references
Messages and presence:
  send              append a deduplicated message
  replay            read messages after a sequence
  wait              wait for matching messages
  message-get       read one message in a readable context
  watch             print and acknowledge arriving messages
  ack, cursor       save or read an acknowledgement cursor
  participants      list sessions with context access
  instances         list recorded process activity
  instance-resume   save process resume metadata
Tasks:
  task-create       create shared work
  task-get          get one task
  task-list         list tasks in a context
  task-claim        claim a pending task
  task-recover      recover a lost token for your live claim
  task-renew        renew a claim
  task-complete     complete a claimed task
  task-release      release a claim
  task-fail         record permanent failure with a live claim
  task-cancel       cancel with a live claim or context-admin authority
Services:
  serve             run the SQLite HTTP daemon
  mcp               run an MCP stdio adapter
  mcp-http          run a remote MCP HTTP adapter
  server-info       inspect version and feature support
  storage-status    probe write readiness (administrator)
  version           print this binary's build identity
  help              show this usage

Global flags:
`
