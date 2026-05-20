# AGENTS.md

Source of truth for AI agents (Codex, Claude Code, Gemini CLI, etc.) working in this repo. Subagent guides may extend it; they may not override it.

## Project Overview

Accounting is the ledger bounded context for FlareX. It models companies, charts of accounts, accounting periods, branches, journal entries, validation, and event-backed posting/reversal workflows.

This repository follows the Stoa pattern: typed reasoning, domain validation, verified execution, and structured feedback. It is not a general framework; it is a concrete accounting implementation that uses shared Stoa harness contracts.

### Feature domains

- **Ledger domain** (`./`): companies, accounts, periods, branches, journal entries, scenarios, repository contracts, and validators.
- **Bookkeeping use case** (`bookkeeping/`, `agent/`): converts a natural-language bookkeeping request into typed intents, validates them against ledger rules, and posts or reverses journal entries.
- **Adapters** (`persistence/*`, `messaging/*`): Postgres/memory repositories and NATS/in-process event buses.

### CLI surface (`cmd/ledger/`)

- `ledger seed <seed.yaml | seed-directory>`: apply declarative YAML ledger setup data.
- `ledger book-run <scenario.json> --request <text>`: run one bookkeeping reasoning workflow and emit a JSON report.
- `ledger tui <scenario.json> [scenario.json ...]`: run the terminal UI over pre-seeded ledger scenarios.

## Architecture

Clean Architecture, organized by feature and adapter boundary. Dependencies point inward: infrastructure and provider SDKs stay outside, use cases orchestrate work, and domain packages own business rules.

The shared Stoa packages provide the general harness contracts: `github.com/flarexio/stoa/llm`, `github.com/flarexio/stoa/llm/openai`, and `github.com/flarexio/stoa/harness/loop`.

## Critical Rules

- **LLM is infrastructure, not domain.** Business logic never imports an SDK.
- **Prompts hold judgment; code holds contracts.** If a rule can be a validator, it must not be only a prompt instruction.
- **Agents communicate through typed objects**, never free-form text as a contract.
- **Errors feed context back to the LLM** for self-correction rather than blind retries.
- **Provider adapters only translate.** Prompt rendering, output decoding, and provider calls must stay separate from domain validation.
- **Domain and agent are separate packages.** The root accounting package holds ledger entities, validators, scenarios, and ports. The `agent` package holds the LLM-driven bookkeeper loop, tools, and prompt rendering.

## The Stoa Pattern (Intent-Validator-Execution)

To keep knowing and doing unified, every agent follows this cycle:

1. Reasoning with evidence: the agent explains the facts and rationale before choosing an intent.
2. Structured intent: the agent outputs a typed `bookkeeping.Intent`. If it needs facts first, it may return tool calls; the harness runs them and feeds back typed tool results.
3. Domain validation: ledger rules validate the intent with pure Go business logic.
4. Verified execution: only validated intents post or reverse journal entries.
5. Environment feedback: validation or execution failures become structured events for the next reasoning turn.

## Design Decisions

- **Go-first.** Use implicit interfaces and small packages with explicit dependencies.
- **No heavy frameworks.** Keep the agent loop short, inspectable, and owned by this repository.
- **Validation and feedback are mandatory.** Never rely on prompt instructions to enforce accounting invariants.
- **Ledger is the CLI name, accounting is the bounded context.** Keep the Go module as `github.com/flarexio/accounting`; keep the runnable command under `cmd/ledger`.

## Code Style

- **Few comments; godoc only when useful.** Every exported symbol should have a concise godoc line when the name and signature are not enough.
- **No essays in source.** Multi-paragraph rationale belongs in docs, PR descriptions, or commit messages.
- **Test names carry the description.** Avoid comments that merely restate `TestX`.
- **No section dividers.** Code organization should show itself.
- **Treat comment churn as code churn.** Update or remove comments when contracts change.

## Git Commits

- When creating git commits, always include this trailer in the commit message: `Co-authored-by: Codex <codex@openai.com>`

## Go Commands

- Never place Go build cache inside this repository.
- Do not use `GOCACHE=.gocache` or create `.gocache/`.
- If the default Go build cache fails locally, use a cache outside the repo, such as `/tmp/go-build-accounting`.

## Current LLM Contract

- `llm.ReasoningEngine[TIntent]` returns `llm.ReasoningResult[TIntent]` with evidence, rationale, and either a typed intent or tool calls.
- A turn may return `llm.ToolCall` values instead of a final intent; the harness runs the matching tool handler and feeds the result back as a typed `tool_result` event before the next turn.
- `llm.PromptRenderer` converts typed reasoning input into provider-neutral messages.
- `llm.Decoder[TIntent]` converts raw model output into typed reasoning results. JSON is only the default decoder, not an architecture requirement.
- OpenAI code must stay provider-specific: SDK calls, message translation, response-format selection, and provider error wrapping only.
