# Execution telemetry

Runner integrations can append `comitia.execution-event.v1` messages to record
evaluation execution without adding a separate ingestion API. The surrounding
message supplies context and authenticated session provenance. `task_id` links
the event to evaluated work and is required. The context message sequence is the
durable ingestion order; `run_id` plus `event_sequence` supplies source order.

Use a deterministic deduplication key such as
`execution/<run_id>/<event_sequence>` when retrying ingestion. Comitia validates
each event but does not enforce that a run's event sequences are contiguous or
unique across sessions. Consumers should group by context, task, session and run
ID rather than assuming run IDs are globally unique.

```json
{
  "schema": "comitia.execution-event.v1",
  "task_id": "TASK_ID",
  "payload": {
    "run_id": "eval-17/candidate-a",
    "event_sequence": 2,
    "kind": "model_usage",
    "occurred_at": "2026-09-11T08:30:00.123Z",
    "provider": "example",
    "model": "candidate-a",
    "input_tokens": 1200,
    "output_tokens": 85,
    "cached_input_tokens": 400,
    "duration_ms": 712,
    "cost_micros": 1450,
    "currency": "USD",
    "cost_source": "estimated",
    "status": "succeeded",
    "dimensions": {"suite": "reasoning-v2", "case": "case-004"}
  }
}
```

`kind` is `run_started`, `model_usage`, `tool_usage`, or `run_finished`.
Model-usage events require `model` and at least one token, duration, or cost
measurement. Tool-usage events require `tool_name`. Finished events require a
`status` of `succeeded`, `failed`, or `cancelled`. Counts, durations and costs
are nonnegative integers. `occurred_at`, when present, is RFC 3339; the message's
`CreatedAt` remains the server ingestion time.

Costs use millionths of a currency unit, paired with an uppercase three-letter
currency code. `cost_source` distinguishes `provider_reported`, `estimated`, and
`configured` amounts so reports do not silently mix invoices with estimates.
Token categories are recorded separately because provider accounting differs.
Consumers decide how to aggregate cached and reasoning tokens.

`dimensions` permits up to 32 nonblank string pairs for bounded evaluation tags.
Payloads reject unknown fields. Do not place prompts, model responses, internal
reasoning, credentials, or tool output in telemetry; those can contain secrets
and require a separate retention and access-control design.

## Send an event

Save the complete envelope above as `execution-event.json`, replacing `TASK_ID`,
then send it through the same CLI path as any structured message:

```sh
comitia send --context-id CONTEXT_ID \
  --dedupe-key 'execution/eval-17/candidate-a/2' \
  --body 'Recorded candidate-a model usage.' \
  --envelope-file execution-event.json
```

The generic HTTP `messages/append`, MCP `send_message`, and Go
`API.AppendMessage` interfaces accept the same envelope. The stock service
registers both the execution-event and evaluation-result validators, so custom
schema configuration is unnecessary.

## Compatibility and rollout

Execution events use the existing message-envelope storage and require no new
database migration. Plain-text and previously stored typed messages retain their
behavior. Servers built before this schema was added reject new execution events
as an unknown schema, so upgrade the server or embedded library before enabling
producers. Existing schema-agnostic clients can send the envelope to an upgraded
server and can replay it as ordinary message data; consumers that do not
recognize the schema should leave its payload uninterpreted.
