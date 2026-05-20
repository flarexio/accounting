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

## Model

| Concept | Type | Notes |
| --- | --- | --- |
| Company | `accounting.Company` | Legal entity that owns the ledger. |
| Chart of accounts | `accounting.Account` | Active accounts can receive new postings. |
| Accounting period | `accounting.Period` | Closed periods reject postings. |
| Journal entry | `accounting.JournalEntry` | Posted immutable entry. |
| Journal line | `accounting.JournalLine` | One debit or credit; amount is stored in minor currency units. |
| Branch dimension | `accounting.Dimensions.BranchID` | Reporting tag on a line, not a separate ledger. |
| Future dimensions | `accounting.Dimensions.Tags` | Open-ended tags for project, department, channel, or similar reporting dimensions. |

Amounts use `int64` minor units so balance checks are exact and never depend on floating-point comparison.

## Invariants

`accounting.Validator` enforces the ledger rules in code:

- currency is present
- period ID is present
- period exists and is open
- each journal entry has at least two lines
- each line amount is positive
- each line side is debit or credit
- each line references an existing active account
- branch dimensions, when present, reference a known branch
- total debit equals total credit

`Validator.Validate` joins violations so one feedback cycle can give the model enough information to correct multiple mistakes at once.

## Use Cases

`bookkeeping/` contains application operations with no LLM dependency. A CLI, test, batch job, HTTP handler, or agent can drive them through the same contracts.

| Use case | Intent | What it does |
| --- | --- | --- |
| `PostJournal` | `post_journal` | Posts a balanced double-entry journal entry. |
| `ReverseJournal` | `reverse_journal` | Creates a mirror-image reversal for an existing posted entry. |

`bookkeeping.Intent` is the typed union emitted by the agent. `bookkeeping.Registry` maps each intent kind to a validate/execute handler. Adding a bookkeeping operation means adding a route to the registry and exposing it in the prompt's intent menu.

## Posting And Immutability

A posted `JournalEntry` is immutable. Corrections are represented as new journal entries, usually through `reverse_journal`, never by editing an existing posted entry in place.

`PostJournal` derives the entry ID from the expected broker sequence, stamps `PostedAt` through its clock, publishes `JournalPosted`, and lets the projection apply the event. Repository reads return copies so callers cannot mutate stored state through returned values.

## Branches

Branches are reporting dimensions on journal lines. They share the same ledger and are validated as known dimensions. They are deliberately not separate ledger instances, which prevents branch-level shadow accounting from appearing inside the core model.

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
