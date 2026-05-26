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

The shared Stoa packages provide the general harness contracts: `github.com/flarexio/stoa/llm`, `github.com/flarexio/stoa/llm/openai`, `github.com/flarexio/stoa/llm/anthropic`, and `github.com/flarexio/stoa/harness/loop`.

See [docs/architecture.md](docs/architecture.md) for the ledger model, validator invariants, event-driven posting flow, and current out-of-scope boundaries.

## Install

Install the `ledger` binary into `$(go env GOBIN)` (or `$(go env GOPATH)/bin` when `GOBIN` is empty); make sure that directory is on your `PATH`.

```bash
go install github.com/flarexio/accounting/cmd/ledger@latest

# or from a clone of this repository
go install ./cmd/ledger
```

The rest of this README assumes `ledger` is on `PATH`. Substitute `go run ./cmd/ledger` for `ledger` if you prefer running from source.

## Commands

```bash
ledger seed seed/taiwan_ledger.yaml

ledger book-run \
  --request "台北總公司以銀行存款支付中華電信辦公室電話費 NT\$3,150，含 5% 進項稅額 NT\$150。"

ledger tui
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

## LLM Providers

The bookkeeper selects a provider adapter via `llm.kind` in `config.yaml`. Supported kinds are `openai` (default) and `anthropic`.

### OpenAI

```yaml
llm:
  kind: openai
  model: gpt-5.5
  api_key: ${OPENAI_API_KEY}  # or set OPENAI_API_KEY in the environment
  base_url: https://api.openai.com/v1  # omit for default; set for compatible providers
```

`llm.api_key` falls back to `OPENAI_API_KEY`; `llm.base_url` falls back to `OPENAI_BASE_URL`. Override the model per run with `--model <model>` on `book-run`.

The real API integration test is gated separately:

```bash
ACCOUNTING_RUN_OPENAI_TESTS=1 go test ./agent
```

### Anthropic

```yaml
llm:
  kind: anthropic
  model: claude-opus-4-7
  api_key: ${ANTHROPIC_API_KEY}  # or set ANTHROPIC_API_KEY in the environment
  base_url: ""                   # omit for default; set for self-hosted gateways
  max_tokens: 4096               # caps the Messages response; 0 = adapter default
```

`llm.api_key` falls back to `ANTHROPIC_API_KEY`; `llm.base_url` falls back to `ANTHROPIC_BASE_URL`. The Anthropic Messages API requires `max_tokens` — the adapter defaults to 4096 when omitted. `disable_strict_schema_with_tools` is openai-only and ignored here.

## Benchmarks

`ledger bench` runs the bookkeeper over fixed scenarios with known answers and scores each (case, model) iteration, so different models can be compared on the same task. Case files live in [`seed/bench/`](seed/bench/):

| Case | Scenario | Tests |
|---|---|---|
| `aws_bill_basic_payment` | `aws_bill` | USD 2-line credit-card payment (hq) |
| `taiwan_purchase_with_tax` | `taiwan_ledger` | 3-line purchase with 5% input VAT (hq) |
| `taiwan_sale_with_tax` | `taiwan_ledger` | 3-line sale with 5% output VAT (hq) |
| `taiwan_payroll_with_withholdings` | `taiwan_ledger` | 3-line payroll with labor/health insurance withholdings (hq) |
| `taiwan_rent_taichung` | `taiwan_ledger` | 2-line rent posting to the Taichung branch (tc) |
| `taiwan_utility_kaohsiung` | `taiwan_ledger` | 2-line utility posting to the Kaohsiung branch (ks) |
| `taiwan_closed_period_reject` | `taiwan_ledger` | request targets a closed period → reject |

Run a suite against one or more models:

```bash
ledger bench \
  --suite 'seed/bench/taiwan_*.case.yaml' \
  --model gpt-5.5 \
  --repeats 3 \
  --out bench-taiwan.json
```

`--suite` and `--model` accept repeated flags, comma-separated values, and glob patterns. The runner reuses `llm.api_key` and `llm.base_url` from `config.yaml`; pass `--no-vector-search` to skip the chromem-go account searcher.

## Tests

Run the full suite:

```bash
go test ./...
```

Do not place the Go build cache inside this repository.
