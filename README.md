# Accounting

Accounting is the ledger bounded context for FlareX. It models companies, charts of accounts, accounting periods, branches, journal entries, validation, and event-backed posting/reversal workflows.

It is also a Stoa-style agent harness: domain rules decide what counts as valid, use cases orchestrate the work, and LLM adapters stay at the edge. The bookkeeper can reason in natural language, but posting to the ledger only happens through typed intents, validators, and explicit execution.

The runnable command is `ledger`, under `cmd/ledger`. It is a small operator CLI for seeding ledger metadata and exercising the bookkeeping workflow.

## Philosophy

This repository follows the Stoa idea that an agent is knowing meeting doing. A model response is not enough; the agent must produce a verifiable action.

For accounting, that means:

- domain models come before prompts
- journal entries are validated by code, not vibes
- prompts carry judgment, while validators carry contracts
- LLM providers are infrastructure, not domain logic
- feedback from validation and execution is structured and fed back into the next reasoning turn

The deterministic parts are deliberately boring: types, repositories, validators, event buses, and error handling. The probabilistic part is bounded inside the reasoning engine.

## Architecture

The main loop is:

```text
bookkeeping request
-> reasoning engine proposes bookkeeping.Intent
-> ledger validator checks accounting rules
-> executor posts or reverses journal entries
-> validation/execution feedback becomes the next turn's context
```

The packages keep dependencies flowing inward:

- root package: ledger domain models, scenario loading, validation, repository contracts
- `bookkeeping`: typed intents, posting/reversal use cases, event bus contracts
- `agent`: bookkeeper orchestration, tools, and prompt rendering
- `persistence/*`: repository adapters
- `messaging/*`: event bus adapters
- `cmd/ledger`: CLI composition

The shared Stoa packages provide the general harness contracts: `github.com/flarexio/stoa/llm`, `github.com/flarexio/stoa/llm/openai`, and `github.com/flarexio/stoa/harness/loop`.

See [docs/architecture.md](docs/architecture.md) for the ledger model, validator invariants, event-driven posting flow, and current out-of-scope boundaries.

## Commands

Run commands from the repository root with `go run ./cmd/ledger`:

```bash
go run ./cmd/ledger seed seed/taiwan_ledger.yaml

go run ./cmd/ledger book-run testdata/aws_bill.json \
  --request "Paid AWS bill 100 USD using company credit card"

go run ./cmd/ledger tui testdata/aws_bill.json
```

`seed` applies one YAML seed file, or every `*.yaml` / `*.yml` file in a directory, to the configured repository.

`book-run` loads an accounting scenario JSON file, seeds the configured repository, runs the bookkeeping reasoning loop, and prints a JSON report.

`tui` opens a Bubble Tea terminal UI over one or more accounting scenario JSON files. The TUI expects the ledger to have already been seeded.

## Configuration

The CLI reads `config.yaml` from `~/.flarex/accounting` by default. Pass `--work-dir <dir>` to use a different directory.

Start from the example:

```bash
mkdir -p ~/.flarex/accounting
cp config.example.yaml ~/.flarex/accounting/config.yaml
```

An empty config file is valid and defaults to in-memory persistence, in-process messaging, and the deterministic scripted reasoning engine. For Postgres and NATS, use `config.example.yaml` as the shape.

## Local Infrastructure

`compose.yaml` starts local Postgres and NATS JetStream:

```bash
docker compose up -d

migrate -path persistence/postgres/migrations \
  -database "postgres://stoa:stoa@localhost:5432/accounting?sslmode=disable" up
```

The compose database uses user `stoa`, password `stoa`, and database `accounting`.

## OpenAI Engine

The default scripted engine is offline and deterministic. To use the OpenAI engine, set `llm.engine: openai` and `llm.model` in `config.yaml`, or pass `--engine openai --model <model>`, and set:

```bash
export OPENAI_API_KEY=...
```

The real API integration test is gated separately:

```bash
ACCOUNTING_RUN_OPENAI_TESTS=1 go test ./agent
```

## Tests

Run the full suite:

```bash
go test ./...
```

Do not place the Go build cache inside this repository.
