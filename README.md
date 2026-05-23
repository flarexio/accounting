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

go run ./cmd/ledger book-run \
  --request "台北總公司以銀行存款支付中華電信辦公室電話費 NT\$3,150，含 5% 進項稅額 NT\$150。"

go run ./cmd/ledger tui
```

`seed` applies one YAML/JSON scenario file (or every `*.yaml` / `*.yml` file in a directory) to the configured repository -- the company, chart of accounts, branches, and periods.

`book-run` connects to the already-seeded ledger, runs one bookkeeping reasoning cycle against `--request`, and prints a JSON report. The reasoning engine is OpenAI-compatible; set `llm.model` and `llm.api_key` in `config.yaml` (or pass `--model`) and optionally `llm.base_url` for alternative providers.

`tui` opens the Bubble Tea terminal UI against the seeded ledger; same OpenAI requirement, no arguments.

## Configuration

The CLI reads `config.yaml` from `~/.flarex/accounting` by default. Pass `--work-dir <dir>` to use a different directory.

Start from the example:

```bash
mkdir -p ~/.flarex/accounting
cp config.example.yaml ~/.flarex/accounting/config.yaml
```

An empty config file defaults to in-memory persistence and in-process messaging, but `llm.model` must still be set before the bookkeeper can run. Set `llm.api_key` in config or export `OPENAI_API_KEY`. For OpenAI-compatible providers, set `llm.base_url`. For Postgres and NATS, use `config.example.yaml` as the shape.

## Local Infrastructure

`compose.yaml` starts local Postgres and NATS JetStream:

```bash
docker compose up -d

migrate -path persistence/postgres/migrations \
  -database "postgres://stoa:stoa@localhost:5432/accounting?sslmode=disable" up
```

The compose database uses user `stoa`, password `stoa`, and database `accounting`.

## OpenAI

The bookkeeper drives an OpenAI-compatible API. Configure `llm` in `config.yaml`:

```yaml
llm:
  model: gpt-5.4-mini
  api_key: ${OPENAI_API_KEY}  # or set OPENAI_API_KEY in the environment
  base_url: https://api.openai.com/v1  # omit for default; set for compatible providers
```

You can override `llm.model` with `--model <model>` on `book-run`. The API key can come from `llm.api_key` or the `OPENAI_API_KEY` environment variable, with config taking precedence. `llm.base_url` defaults to `$OPENAI_BASE_URL` when unset.

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
