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

Clean Architecture, organized by feature and adapter boundary. Dependencies point inward: Infrastructure → Interface Adapters → Use Cases → Domain. See [docs/architecture.md](docs/architecture.md) for the ledger model, validator invariants, event-driven posting flow, and current out-of-scope boundaries. To add a bookkeeping operation, follow [docs/use-case-recipe.md](docs/use-case-recipe.md).

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
- **Event-sourced projection.** Bookkeeping use cases publish events; subscribed projection handlers are the only writers to the `LedgerRepository`. A handler translates the delivered event into domain models and persists them through the repository's domain-typed methods (`AppendEntry`, `SetPeriodStatus`) — the port takes no event types. The broker subject and sequence ride on the context as `accounting.EventMeta` (a bus dispatch sets it once before invoking the handler), so the projection records the sequence without leaking transport types into the domain signatures.
- **Structural reversal/correction tracking.** A reversal entry is linked to its original through a `JournalRelation` (append-only, composite identity `(from_entry, to_entry)`), not a description string. Relations ride on the same `JournalPosted` event as the reversing entry and `AppendEntry` writes both in one transaction, so the projection never sees an entry without its relation. The same model carries `ClosePeriod`'s M:N period-end relationships under the `closes` discriminator (one closing entry → many original revenue/expense entries); future operations (settlement, adjustments) reuse the same table under their own discriminators rather than each spawning a new one.
- **Period-end closing is rule-driven, not LLM-driven.** `ClosePeriod` is a `bookkeeping` use case but deliberately not a `bookkeeping.Intent` — closing policy is too company-specific to delegate to a model. The trigger is an external scheduler (a `crontab` entry, systemd timer, or equivalent) invoking `ledger close --period <id>`. The use case refuses to close before `Period.End` has actually passed in `Company.TimeZone`, regardless of the scheduler's own clock.
- **Business dates are dates, not instants.** `JournalEntry.Date`, `JournalIntent.Date`, and `Period.Start/End` are `accounting.Date` (year/month/day) over Postgres `DATE`, interpreted in the company's timezone via `Company.TimeZone` (IANA name like `Asia/Taipei`). The agent prompt names the timezone so the LLM knows what zone its `YYYY-MM-DD` outputs live in. `JournalEntry.PostedAt` stays `time.Time` / `TIMESTAMPTZ` — it is a real instant. Never derive a business date from a `TIMESTAMPTZ` without an explicit location.
- **Every line carries a branch_id.** Branches are not an optional reporting tag; every journal line must reference a branch and all lines on one entry share the same branch_id. Single-location companies seed one branch (convention: `{id: main, name: ...}`). The TUI picks the operator's current branch at startup and the prompt's `OperatorBranchID` tells the LLM to default to it when the user doesn't specify; the validator still enforces the invariant regardless of where the value came from.
- **Account search is hybrid: semantic over enriched text, fused with a lexical pass.** `find_accounts` takes a natural-language description of the economic event (`AccountFilter.Query`), not a literal name substring — the tool schema says so, so the model describes the meaning instead of guessing the exact account name. `accounting.AccountEmbeddingText` indexes each account's name, optional `Description`, and `Aliases` (everyday transaction wording), and deliberately excludes the code so its digits do not dilute the semantic vector. `Aliases`/`Description` are seed-time indexing hints: the projection persists only the resulting embedding, not the raw hints, so re-embedding requires re-seeding rather than a schema column. Alongside the dense channel, a lexical channel matches exact codes and exact-or-substring names (`accounting.LexicalAccountTier`, mirrored in the Postgres SQL `CASE`), and the two are combined by reciprocal rank fusion (`accounting.FuseAccountsRRF`, k=60) so an exact code or name the embedding buries still surfaces. When the query mentions neither a code nor a name the lexical channel is empty and fusion degrades to the dense ranking. Exact single-account lookup still stays on `LedgerRepository.Account`.

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
