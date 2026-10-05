# Inspector execution-view design record — 2026-10-05

Historical design direction, not an implemented ingestion contract.
For current runner events see [execution telemetry](../execution-telemetry.md).

## Future execution view

Keep imported execution events separate from the authoritative coordination log.
A future runner adapter can attach externally visible prompts, responses, tool
calls/results, and explicit summaries using context, session and task IDs plus a
stable run ID and per-run event sequence. The UI could then link a coordination
finding to the execution that produced it. This is a design direction, not an
implemented ingestion contract. Import requires an explicit runner integration,
access control, retention policy and handling of secrets in tool outputs.
Private internal model reasoning is not an available transcript source.
