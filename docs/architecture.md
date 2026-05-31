# Accounting Architecture

Accounting is the ledger bounded context and the bookkeeping conscience of the agent. The root package owns the ledger model and validation rules that any model-proposed journal entry must satisfy before it can be posted.

The domain is intentionally pure: no LLM SDK, no harness package, no CLI dependency. Bookkeeping operations live in `bookkeeping/` and can be called without a model. The `agent/` package drives those operations through the Stoa harness by asking a reasoning engine for a typed `bookkeeping.Intent`, validating the intent, executing it, and feeding structured failures back for correction.

## Flow

```text
bookkeeping request
-> agent.Bookkeeper asks the reasoning engine for bookkeeping.Intent
-> bookkeeping.Registry routes the intent to the matching use case
-> accounting.Validator enforces ledger invariants
-> validated execution publishes JournalPosted
-> event bus subscriber applies JournalPosted to LedgerRepository
-> validation or execution errors become feedback for the next reasoning turn
```

The bookkeeping layer does not mutate the projection directly. Producers validate and publish events; the subscribed `Apply` handler is the single writer to the `LedgerRepository` projection. This keeps command handling, event publication, and projection state separate enough to test each part directly.

## Domain Model

The conceptual shape of the domain: what references what, which aggregates exist, where the value objects live. Persistence (tables, columns, indexes) is a separate concern handled per adapter and lives outside this section.

```text
                  ┌──────────────────────┐
                  │       Company        │   « Singleton Aggregate »
                  └──────────────────────┘


Reference data (seeded; read by the validator)
┌──────────┐      ┌──────────┐      ┌──────────┐
│ Account  │      │  Period  │      │  Branch  │
└──────────┘      └──────────┘      └──────────┘
     ▲                  ▲                  ▲
     │ account_code     │ period_id        │ branch_id
     │                  │                  │
┌────┴──────────────────┴──────────────────┴─────┐
│              JournalEntry                      │   « Aggregate Root »
│   id, date, period_id, currency, posted_at     │   immutable on post,
│   └── JournalLine [1..*] « VO »                │   append-only
│        └── Dimensions « VO »                   │
└────────────────────────────────────────────────┘
                      ▲   ▲
                      │   │
                 from │   │ to
                      │   │
   ┌──────────────────┴───┴───────────────────────┐
   │              JournalRelation                 │   « Aggregate Root »
   │   (from_entry, to_entry) composite identity  │   M:N, append-only
   │   type, reason, note                         │
   └──────────────────────────────────────────────┘


Transient (lives only between validation and Apply)
┌──────────────────┐
│  JournalIntent   │  ── Validator + Apply ──►  becomes a JournalEntry
└──────────────────┘


Integration event
« Domain Event »  JournalPosted
  ├── entry      : JournalEntry
  └── relations  : [JournalRelation]  0..*

  ── Apply (single writer) ──►  LedgerRepository projection
```

**Aggregates.** `Company` is a singleton aggregate. `JournalEntry` is the posting aggregate root and seals its `JournalLine` value objects on post. `JournalRelation` is its own aggregate that references two `JournalEntry` roots without belonging to either — this is what lets one entry be referenced by many later entries (M:N).

**Reference data.** `Account`, `Period`, `Branch` are seeded entities the validator reads from. A journal line references an account code and a branch id rather than embedding them, which keeps the journal aggregate independent of chart-of-accounts maintenance.

**Value objects.** `JournalLine` and `Dimensions` have no identity of their own; they live and die with their parent `JournalEntry` and are compared by value. Amounts are `int64` minor units.

**Intent vs entry.** `JournalIntent` is a proposed transaction with no identity; it lives only long enough to be validated and either become a `JournalEntry` (after `Apply`) or be rejected.

**Integration event.** `JournalPosted` is the only path from a use case to the projection. It carries the new `JournalEntry` and any `JournalRelation`s created with it; the subscribed `Apply` handler writes both in one transaction so the projection never observes an entry without its relations.

## Concepts

| Concept | Type | Notes |
| --- | --- | --- |
| Company | `accounting.Company` | Legal entity that owns the ledger; carries an IANA `TimeZone` (e.g. `Asia/Taipei`) used to interpret business dates. |
| Chart of accounts | `accounting.Account` | Active accounts can receive new postings. `Aliases` and `Description` are optional semantic-search hints. |
| Accounting period | `accounting.Period` | Closed periods reject postings. `Start` and `End` are calendar dates (inclusive) in the company's timezone. |
| Journal entry | `accounting.JournalEntry` | Posted immutable entry. `Date` is the business date (`accounting.Date`, `YYYY-MM-DD`); `PostedAt` is the UTC instant it was written. |
| Journal line | `accounting.JournalLine` | One debit or credit; amount is stored in minor currency units. |
| Branch dimension | `accounting.Dimensions.BranchID` | Reporting tag on a line, not a separate ledger. Required on every line; single-location companies seed one called `main`. |
| Future dimensions | `accounting.Dimensions.Tags` | Open-ended tags for project, department, channel, or similar reporting dimensions. |
| Journal relation | `accounting.JournalRelation` | Directional, typed link between two posted entries (e.g. a reversal pointing at its original). Append-only; composite identity `(from_entry, to_entry)`. |

Amounts use `int64` minor units so balance checks are exact and never depend on floating-point comparison.

Dates and instants are different types on purpose. `accounting.Date` (year/month/day) maps to Postgres `DATE` for `JournalEntry.Date`, `JournalIntent.Date`, and `Period.Start/End` — these are calendar dates whose meaning depends on the company's timezone, not absolute moments. `time.Time` over `TIMESTAMPTZ` is reserved for real instants (`PostedAt`). Crossing the boundary requires an explicit `*time.Location`, available from `Company.Location()`.

Account search is hybrid, not substring. The `find_accounts` tool sets `AccountFilter.Query` to a natural-language description of the economic event, and the adapter blends two ranked channels:

- **Dense.** Cosine similarity over `accounting.AccountEmbeddingText` — each account's name, `Description`, and `Aliases` (everyday transaction wording), with the code excluded so its digits do not dilute the semantic vector. The Postgres adapter ranks with pgvector; the in-memory adapter delegates to a chromem-go searcher.
- **Lexical.** Exact code, exact name, name↔query substring, or code-in-query, ordered by match strength (`accounting.LexicalAccountTier`, mirrored in the Postgres SQL `CASE`). This catches the "I know it's 6104" or exact-name query the embedding might bury.

The channels are combined by reciprocal rank fusion (`accounting.FuseAccountsRRF`, k=60): each account's score is the sum of `1/(k + rank)` across the channels that ranked it, so agreement between channels wins and a query that mentions neither a code nor a name simply falls back to the dense ranking. `Aliases` and `Description` are seed-time indexing inputs — the projection persists only the resulting embedding, so changing them requires re-seeding. Exact single-account lookup stays on `LedgerRepository.Account`.

An optional **reranking** stage refines the fused list. `accounting.NewRerankedRepository` decorates any `LedgerRepository`: when `FindAccounts` carries a `Query` and returns more than one candidate, it reorders them through an `accounting.AccountReranker` before returning. The `reranking/openai` adapter implements that port with one chat-completion call that returns the candidate codes in relevance order; unknown codes are ignored and omitted ones are appended in place, so reranking reorders without ever dropping a candidate. It is off unless `rerank.model` is configured, and because it is a transparent decorator neither the memory nor the Postgres adapter knows it exists.

## Invariants

`accounting.Validator` enforces the ledger rules in code:

- currency is present
- period ID is present
- period exists and is open
- each journal entry has at least two lines
- each line amount is positive
- each line side is debit or credit
- each line references an existing active account
- each line carries a branch_id that references a known branch
- all lines on one entry share the same branch_id
- total debit equals total credit

`Validator.Validate` joins violations so one feedback cycle can give the model enough information to correct multiple mistakes at once.

`Validator.ValidateRelation` enforces the relation rules:

- both `from_entry` and `to_entry` exist in the ledger
- `from_entry` is not equal to `to_entry`
- `from_entry` was posted no earlier than `to_entry`
- `type` is one of the known relation kinds
- for `type = reverses`, the from entry's lines are the mirror image of the to entry's — sides swapped, with amounts, accounts, and branches preserved

Currency precision is deliberately not validated: both `3150` and `315000` are legal `int64` values that balance, so code cannot disambiguate the intended scale from the line alone. The ISO 4217 minor-unit mapping (TWD = exponent 0, USD = 2, BHD = 3, ...) lives in the bookkeeper prompt as judgment, not in the validator as a contract. If a model picks the wrong scale, the entry still posts; this is the documented trade-off behind storing amounts as `int64` minor units.

## Use Cases

`bookkeeping/` contains application operations with no LLM dependency. A CLI, test, batch job, HTTP handler, or agent can drive them through the same contracts.

| Use case | Intent | What it does |
| --- | --- | --- |
| `PostJournal` | `post_journal` | Posts a balanced double-entry journal entry. |
| `ReverseJournal` | `reverse_journal` | Creates a mirror-image reversal of an existing posted entry and links it back through a `JournalRelation` of type `reverses`; entry and relation are applied atomically. |
| `ClosePeriod` | — (CLI, no LLM intent) | For each branch with revenue or expense activity in the period, posts one balanced closing entry that drains every contributing account into the company's Retained Earnings account, with one `JournalRelation` of type `closes` per contributing source entry. Then flips `Period.Status` to `closed`. A second invocation against an already-closed period is a no-op. |

`bookkeeping.Intent` is the typed union emitted by the agent. `bookkeeping.Registry` maps each intent kind to a validate/execute handler. Adding a bookkeeping operation means adding a route to the registry and exposing it in the prompt's intent menu. `ClosePeriod` is deliberately not a `bookkeeping.Intent` — period-end closing is rule-driven, not judgment-driven, and is triggered by an external scheduler (a `crontab` entry or equivalent) invoking `ledger close --period <id>`.

`ClosePeriod` is the first M:N consumer of `JournalRelation`: one closing entry references many original revenue/expense entries through `closes` relations, exercising the same shape reserved for future operations (settlement, adjustments) under different `type` discriminators.

## Posting And Immutability

A posted `JournalEntry` is immutable. Corrections are represented as new journal entries, usually through `reverse_journal`, never by editing an existing posted entry in place. The new entry is linked back to the original through a `JournalRelation`, which is itself append-only — a wrong relation is corrected by appending another relation, not by editing the row.

`PostJournal` derives the entry ID from the expected broker sequence, stamps `PostedAt` through its clock, publishes `JournalPosted`, and lets the projection apply the event. `ReverseJournal` builds the mirror entry and the relation as one bundle and drives the same publish path, so both land in one `Apply` transaction. Repository reads return copies so callers cannot mutate stored state through returned values.

## Branches

Branches are reporting dimensions on journal lines. They share the same ledger and are validated as known dimensions. They are deliberately not separate ledger instances, which prevents branch-level shadow accounting from appearing inside the core model.

Every journal line must carry a `branch_id`, and all lines on one entry must share it. A scenario with zero branches is rejected at seed time; single-location companies are expected to seed one branch with `id: main`. The TUI picks the operator's current branch at startup (one `Option` per branch) and threads it into `PromptRenderer.OperatorBranchID`, which adds a "default to this branch" hint to the system prompt; the validator still enforces the invariant regardless of where the value came from.

## Stoa Harness Boundary

The shared Stoa contracts provide the agent harness vocabulary:

- `llm.ReasoningEngine[TIntent]` returns typed reasoning results, intents, or tool calls
- `llm.PromptRenderer` turns typed input into provider-neutral messages
- `harness/loop` streams typed cycle events for model output, validation errors, execution errors, observations, and tool results
- provider adapters such as `llm/openai` translate SDK calls and raw model output, but do not own accounting rules

The prompt can explain accounting judgment, but every accounting invariant must also be enforced by Go validators.

## Out Of Scope

This foundation intentionally does not include:

- AR/AP, invoicing, or payments
- payroll, tax filing, or a tax engine
- bank reconciliation
- inventory
- reporting engine
- separate branch accounting services
- multi-currency conversion

These are not banned forever; they are outside the initial ledger and bookkeeping foundation.
