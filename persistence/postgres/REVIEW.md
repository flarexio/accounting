# Review Guidelines: `persistence/postgres/`

The Postgres-backed `accounting.LedgerRepository` adapter. Apply these on
top of the root `REVIEW.md`. Most of the rules below are sqlc / JetStream
traps that are not visible from the Go code alone.

## Hard rules

- **`pgstore/` is sqlc-generated. Never hand-edit it.** After changing the
  schema or `sqlc/queries.sql`, run `cd persistence/postgres && sqlc
  generate` and commit the regenerated output in the same change.
- **Schema changes go through a new migration pair.** Add
  `migrations/NNNN_name.up.sql` + `.down.sql`; never edit a migration that
  has already been applied. sqlc reads the migrations as its schema source,
  so the migration and the generated code must move together.
- **`Apply` must stay idempotent.** NATS JetStream redelivers a message
  when a consumer crashes between the DB commit and the ack. The entry and
  line inserts use `ON CONFLICT DO NOTHING` and the offset write uses
  `GREATEST`; a new write added to `Apply` that is not idempotent will loop
  forever as Nak'd unique-key violations.
- **`Apply` is one transaction.** Entry, lines, and the subject offset are
  written together so a concurrent `LastSequence` reader can never observe
  an entry without its offset. Keep them in the same `tx`.

## What to flag

- Hand-edited files under `pgstore/`.
- A query added to `sqlc/queries.sql` with no matching regenerated
  `pgstore/` output in the same PR.
- Raw SQL assembled by string concatenation instead of a sqlc query.
- `Apply` losing its single-transaction or idempotency property.
- The concrete `accountingRepository` type being exported, or a second
  constructor appearing beside `NewAccountingRepository` — the factory
  returning the port interface is the only entry point.
