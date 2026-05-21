# AGENTS.md

Source of truth for AI agents (Gemini CLI, Claude Code, Codex, etc.) working in this repo. Subagent guides may extend it; they may not override it.

## Project Overview

Accounting is the ledger bounded context for FlareX — companies, charts of accounts, accounting periods, branches, journal entries, validation, and event-backed posting/reversal workflows.

It is also a [Stoa](https://github.com/flarexio/stoa)-style agent harness: domain rules decide what counts as valid, use cases orchestrate the work, and LLM adapters stay at the edge. The bookkeeper can reason in natural language, but posting to the ledger only happens through typed intents, validators, and verified execution.

### Feature domains

- **Ledger domain** (`./`): companies, accounts, periods, branches, journal entries, scenarios, repository contracts, and validators.
- **Bookkeeping use case** (`bookkeeping/`): typed intents, posting/reversal handlers, registry, and event bus contracts.
- **Bookkeeper agent** (`agent/`): LLM-driven loop, tools, and prompt rendering on top of the shared Stoa harness.
- **Adapters** (`persistence/*`, `messaging/*`): Postgres/memory repositories and NATS/in-process event buses.

### CLI surface (`cmd/ledger/`)

- `ledger seed <seed.yaml | seed-directory>` — apply declarative YAML ledger setup data.
- `ledger book-run <scenario.{json,yaml}> --request <text>` — run one bookkeeping reasoning cycle and emit a JSON report.
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
- **Event-sourced projection.** Bookkeeping use cases publish events; a single subscribed `Apply` handler is the only writer to the `LedgerRepository` projection.

## Code Style

- **Few comments; godoc only.** Every exported symbol gets one concise godoc line — enough for an LLM or `go doc` reader to know what it is without seeing the body. Omit the doc entirely when the name and signature already say it.
- **No essays in source.** Multi-paragraph rationale belongs in `docs/`, in a PR description, or in a commit message — not above a function. Inline comments are for non-obvious "why" only, never to restate what the next line does.
- **Test names carry the description.** Don't write `// TestX does Y` above `func TestX`; the name is the doc. Keep only comments that explain non-obvious test mechanics (fixture invariants the assertions rely on, scaffolding rationale).
- **No section dividers** (`// --- point reads ---`). Code organization shows itself.
- **Treat comment churn as code churn.** Comments that drift out of sync are worse than no comment. If you change a function's contract, update or delete its doc in the same change.

## Go Tooling

- **Prefer `go doc` over reading full source files** when exploring a package's API. `go doc github.com/flarexio/stoa/llm` is cheaper than reading every file under `llm/`. Use `go doc -all <pkg>` for the full godoc dump, `go doc <pkg>.<Symbol>` for one type or function, and `go doc -src <pkg>.<Symbol>` when you also need the source body. Drop to full-file reads only when godoc is not enough — implementation details, unexported helpers, or behavior spanning multiple functions.
- **Never place the Go build cache inside this repository.** Do not use `GOCACHE=.gocache` or create `.gocache/`. If the default cache fails locally, use a cache outside the repo, such as `/tmp/go-build-accounting`.

## Release Workflow

If an AI agent performs a release, preserve the agent attribution in the commit metadata as a `Co-Author`. Some tools add this automatically; for tools that do not, the agent must add it explicitly instead of omitting it.

## Current LLM Contract

- `llm.ReasoningEngine[TIntent]` returns `llm.ReasoningResult[TIntent]` with evidence, rationale, and either a typed intent or tool calls.
- A turn may return `llm.ToolCall` values instead of a final intent; `harness/loop` runs the matching tool handler and feeds the result back as a typed `tool_result` event before the next turn.
- `llm.PromptRenderer` converts typed reasoning input into provider-neutral messages.
- `llm.Decoder[TIntent]` converts raw model output into typed reasoning results. JSON is only the default decoder, not an architecture requirement.
- OpenAI code must stay provider-specific: SDK calls, message translation, response-format selection, and provider error wrapping only.
