Ledger validates order events, tracks balances, and deduplicates by event id. Outbox stores notification payloads and tracks delivery ownership. Service connects both components and restores their snapshots together. These modules use only in-memory state; persist a complete snapshot when handing work to another process.

Retry an event with its original id and payload. A conflicting payload is rejected. A claimed notification stays unavailable until its lease expires. Reclaim increments its version, and completion rejects a stale owner or version. Keep these checks when adapting the code to another backend.

There is a crash window after the sink accepts a notification but before the queue records completion. A resumed worker can deliver the notification again. The sink therefore needs durable idempotency keyed by event id; queue ownership does not guarantee exactly-once external effects.

Ingest rolls back both components on enqueue failure, but this is only an in-memory transaction. Production persistence needs a database transaction covering the ledger and outbox together. Snapshotting alone cannot protect an interrupted write to separate stores. Run the behavioral checks before changing these boundaries.
