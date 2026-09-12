# Manual audit notes

These notes do not change the fixed scoring rules or feed extra information back to model sessions.

## First clean single-agent trial (replicate 0)

- Architecture draft recommends restoring only the ledger after enqueue failure. The integrated service corrects this and snapshots/restores both ledger and outbox.
- Isolated ledger/outbox draft diagnostic checks pass (reference companion modules, diagnostic only).
- Integration passes 11 behavioral check groups; its handoff fails the word limit (297 words by the fixed grader).
- Integration handoff incorrectly says both ingest and dispatch preserve atomicity and roll back on any failed step. Its own service implementation and later handoff paragraph correctly leave dispatch outside the ingest transaction. This contradiction is a qualitative communication defect beyond the automated topic/length rubric. Check whether the final reviewer removes it.
- Final review brings the handoff inside the word limit and preserves all behavior checks, but still says “Failures roll back both components” immediately after describing both ingest and dispatch. This remains ambiguous/incorrect for dispatch, despite a later paragraph correctly stating its nontransactional boundary. A 12/12 automated pass is therefore not a claim of flawless documentation.
- Final artifact has 415 Python lines including comments/docstrings across three modules; the workflow consumed 90,781 reported input/output tokens and $0.362241 list-price equivalent over 601.755 seconds.

## Clean multi-agent calibration trial (replicate 0)

- Completed normally under the original time limit (501.483 adapter seconds), scoring 10/12 after review, up from 9/12 at integration.
- Genuine rollback bug: Service.ingest calls ledger.apply before taking the rollback snapshot. On an outbox collision it restores the already-mutated ledger, leaving the order/event applied. The final reviewer preserved this bug.
- Reviewer corrected event validation, but the handoff remained overlong (233 words, down from 359).
- This trial finished before the old scheduler was stopped at the user's request for a longer time budget. No model call was cancelled during that transition.
