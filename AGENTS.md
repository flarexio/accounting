# AGENTS.md

Source of truth for AI agents (Gemini CLI, Claude Code, Codex, etc.) working in this repo. Subagent guides may extend it; they may not override it.

## Project Overview

Accounting is the ledger bounded context for FlareX — companies, charts of accounts, accounting periods, branches, journal entries, validation, and event-backed posting/reversal workflows.

It is also a [Stoa](https://github.com/flarexio/stoa)-style agent harness: domain rules decide what counts as valid, use cases orchestrate the work, and LLM adapters stay at the edge. The bookkeeper can reason in natural language, but posting to the ledger only happens through typed intents, validators, and verified execution.

### Feature domains

- **Ledger domain** (`./`): companies, accounts, periods, branches, journal entries, scenarios, repository contracts, and validators.
- **Bookkeeping use case** (`bookkeeping/`): typed intents, posting/reversal handlers, entry-relation tracking, registry, and event bus contracts.
- **Bookkeeper agent** (`agent/`): LLM-driven loop, tools, and prompt rendering on top of the shared Stoa harness.
- **Adapters** (`persistence/*`, `messaging/*`): Postgres/memory repositories and NATS/in-process event buses.

### CLI surface (`cmd/ledger/`)

- `ledger seed <seed.yaml | seed-directory>` — apply declarative YAML ledger setup data.
- `ledger book-run <scenario.{json,yaml}> --request <text>` — run one bookkeeping reasoning cycle and emit a JSON report.
- `ledger bench --suite <glob> --model <names>` — run the bookkeeping agent over a case suite against one or more models, scored against gold answers; emits a JSON report.
- `ledger close --period <id>` — close an accounting period by posting closing entries into Retained Earnings and flipping `Period.Status`; emits a JSON report. Intended to be invoked by a scheduler.
- `ledger tui <scenario.{json,yaml}> [...]` — Bubble Tea terminal UI over pre-seeded ledger scenarios.

## Architecture

Clean Architecture, organized by feature and adapter boundary. Dependencies point inward: Infrastructure → Interface Adapters → Use Cases → Domain. See [docs/architecture.md](docs/architecture.md) for the ledger model, validator invariants, event-driven posting flow, and current out-of-scope boundaries.

The shared Stoa packages provide the general harness contracts: `github.com/flarexio/stoa/llm`, `github.com/flarexio/stoa/llm/openai`, and `github.com/flarexio/stoa/harness/loop`.

## Critical Rules

- **LLM is infrastructure, not domain.** Business logic never imports an SDK.
- **Prompts hold judgment; code holds contracts.** If a rule can be a validator, it must not be only a prompt instruction.
- **Agents communicate through typed handoff objects**, never free-form text as a contract.
- **Errors feed context back to the LLM** for self-correction rather than blind retries.
- **Provider adapters only translate.** Prompt rendering, output decoding, and provider calls must stay separate from domain validation.
- **Domain and agent are separate packages.** The root accounting package holds ledger entities, validators, scenarios, and ports. The `bookkeeping` package holds use cases and intents. The `agent` package holds the LLM-driven bookkeeper loop, tools, and prompt rendering. Imports point inward only: the domain depends on neither the agent nor any LLM code, and only the agent package depends on `llm`.

## The Stoa Pattern (Intent-Validator-Execution)

To keep knowing and doing unified, every agent follows this cycle:

1. **Reasoning with evidence**: the agent explains the facts and rationale before choosing an intent.
2. **Structured intent**: the agent outputs a strictly typed `bookkeeping.Intent`. If it needs more facts first, it may return tool calls; the harness runs them and feeds back typed tool results before the next cycle.
3. **Domain validation**: `accounting.Validator` enforces ledger invariants in pure Go.
4. **Verified execution**: only validated intents post or reverse journal entries through `bookkeeping.Registry`.
5. **Environment feedback**: validation or execution failures become structured events for the next reasoning turn.

## Design Decisions

- **Go-first.** Implicit interfaces and small packages with explicit dependencies; generics parameterize the harness loop and LLM contract over the feature's `Intent` type.
- **No heavy frameworks** (LangChain, LangGraph). Keep the agent loop short, inspectable, and owned by this repository.
- **Validation and feedback are mandatory.** Never rely on prompt instructions to enforce accounting invariants; domain errors flow back into the next reasoning cycle as typed events.
- **Ledger is the CLI name, accounting is the bounded context.** Keep the Go module as `github.com/flarexio/accounting`; keep the runnable command under `cmd/ledger`.
- **Event-sourced projection.** Use cases publish events; subscribed handlers are the only writers to `LedgerRepository`, translating each event to domain models and persisting via domain-typed methods (`AppendEntry`, `SetPeriodStatus`) — the port takes no event types. Transport subject+sequence ride on the context as `accounting.EventMeta`, never on the domain signatures.
- **Optimistic concurrency is per-subject, enforced at the broker.** A producer reads `LedgerRepository.LastSequence(subject)` from the projection, then publishes with that value as `ExpectedSequence`; the NATS adapter passes it as `Nats-Expected-Last-Subject-Sequence` (`WithExpectLastSequencePerSubject`) and JetStream — not postgres — rejects a stale publish with `WrongLastSequence`, serializing concurrent writers on that subject. Producers read `LastSequence` *before* any count (e.g. `EntryCount`), so projection lag can only make the hint stale and get the publish rejected, never let a stale read slip through. The hint is concurrency control only, never an entry number.
- **Reference data is event-sourced too.** `ledger seed` publishes one event per entity (`CompanyConfigured`/`AccountAdded`/`BranchAdded`/`PeriodAdded`); handlers upsert them, so the chart lives in the event log and is recoverable. `Scenario.Seed(repo)` is the direct projection used in tests. On NATS the projection is async, so `EventBus.CatchUp` blocks until the consumer drains to the stream head (in `buildMessaging`, and again after `seed` publishes); inproc dispatches synchronously, so CatchUp is a no-op.
- **Structural reversal/correction tracking.** A reversal links to its original via a `JournalRelation` (append-only, composite key `(from_entry, to_entry)`), not a description string; it rides the same `JournalPosted` event so `AppendEntry` writes both atomically. The same table carries `ClosePeriod`'s M:N `closes` relations and future discriminators (settlement, adjustments) rather than spawning new ones.
- **Period-end closing is rule-driven, not LLM-driven.** `ClosePeriod` is a use case but deliberately not a `bookkeeping.Intent` — closing policy is too company-specific to delegate. Triggered by an external scheduler running `ledger close --period <id>`; it refuses to close before `Period.End` has passed in `Company.TimeZone`.
- **Business dates are dates, not instants.** `JournalEntry.Date`, `JournalIntent.Date`, `Period.Start/End` are `accounting.Date` over Postgres `DATE`, interpreted in `Company.TimeZone`. `PostedAt` stays `TIMESTAMPTZ` (a real instant). Never derive a business date from a `TIMESTAMPTZ` without an explicit location.
- **Every line carries a branch_id.** Every journal line references a branch, and all lines on one entry share it. Single-location companies seed `{id: main, ...}`. The TUI/prompt default to the operator's branch; the validator enforces the invariant regardless of source.
- **Cross-turn recall is retrieval, not accumulation.** A TUI session carries a bounded `RecentEntries` (last N posted entries); the agent self-decides when it needs context — a self-contained request (date + amounts + description) acts directly, a referential one ("redo that entry") calls the `recent_entries`/`get_entry` tools to recover detail from the ledger. The transcript is never replayed into the prompt, so prompt growth is O(N) regardless of conversation length. One-shot `book-run`/`bench` carry no memory and omit the recall tools and guidance. The agent must never invent a missing amount — unresolvable references `reject`.
- **Account search is hybrid.** `find_accounts` takes a natural-language description, not a name substring. A dense channel (cosine over `AccountEmbeddingText` = name + `Description` + `Aliases`, code excluded) fuses with a lexical channel (exact code/name/substring, `LexicalAccountTier`) by reciprocal rank fusion (`FuseAccountsRRF`, k=60); a query naming neither degrades to dense. `Aliases`/`Description` are seed-time hints baked into the embedding (re-embed = re-seed). See [docs/architecture.md](docs/architecture.md).
- **Company policy is event-sourced judgment, kept out of seed.** `ledger policy set/edit/get` publishes `PolicySet` (subject `accounting.company.policy`); `ApplyPolicy` projects it to a `policy` column via `SetPolicy`, and `PromptRenderer` injects it verbatim into the agent prompt. It is operator-authored free-text (markdown convention) — sparse, high-consequence account-disambiguation rules (judgment), distinct from account `Description`/`Aliases` (retrieval-only facts in the embedding). It has its own write path, so `SetCompany`/re-seed never clobbers it (`UpsertCompany` omits the column; `Company.Policy` is `yaml:"-"`).

## Code Style

- **Few comments; godoc only.** Every exported symbol gets one concise godoc line — enough for an LLM or `go doc` reader to know what it is without seeing the body. Omit the doc entirely when the name and signature already say it.
- **No essays in source.** Multi-paragraph rationale belongs in `docs/`, in a PR description, or in a commit message — not above a function. Inline comments are for non-obvious "why" only, never to restate what the next line does.
- **Test names carry the description.** Don't write `// TestX does Y` above `func TestX`; the name is the doc. Keep only comments that explain non-obvious test mechanics (fixture invariants the assertions rely on, scaffolding rationale).
- **No section dividers** (`// --- point reads ---`). Code organization shows itself.
- **Treat comment churn as code churn.** Comments that drift out of sync are worse than no comment. If you change a function's contract, update or delete its doc in the same change.

## Go Tooling

- **For external packages, prefer `go doc` to reading source.** Stoa, the OpenAI SDK, and any dependency outside this repo: use `go doc github.com/flarexio/stoa/llm`, `go doc <pkg>.<Symbol>`, or `go doc -src <pkg>.<Symbol>` — much cheaper than scanning an unfamiliar tree with `find` + `Read`. For this repo's own source, read files directly; `go doc` adds no value on code you can already navigate.
- **Use the default `GOCACHE`.** Run `go test`, `go build`, etc. without prefixing `GOCACHE=` — the default external cache works. Never place the cache inside this repo (no `GOCACHE=.gocache`, no `.gocache/`). Only override with an outside-the-repo path (e.g. `/tmp/go-build-accounting`) when the default cache is actually broken locally.

## Release Workflow

If an AI agent performs a release, preserve the agent attribution in the commit metadata as a `Co-Author`. Some tools add this automatically; for tools that do not, the agent must add it explicitly instead of omitting it.

## Current LLM Contract

- `llm.ReasoningEngine[TIntent]` returns `llm.ReasoningResult[TIntent]` with evidence, rationale, and either a typed intent or tool calls.
- A turn may return `llm.ToolCall` values instead of a final intent; `harness/loop` runs the matching tool handler and feeds the result back as a typed `tool_result` event before the next turn.
- `llm.PromptRenderer` converts typed reasoning input into provider-neutral messages.
- `llm.Decoder[TIntent]` converts raw model output into typed reasoning results. JSON is only the default decoder, not an architecture requirement.
- OpenAI code must stay provider-specific: SDK calls, message translation, response-format selection, and provider error wrapping only.
