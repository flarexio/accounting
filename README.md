# Accounting

Accounting is the ledger bounded context for FlareX. It models companies, charts of accounts, accounting periods, branches, journal entries, validation, and event-backed posting/reversal workflows.

It is also a Stoa-style agent harness: domain rules decide what counts as valid, use cases orchestrate the work, and LLM adapters stay at the edge. The bookkeeper can reason in natural language, but posting to the ledger only happens through typed intents, validators, and explicit execution.

The runnable command is `ledger`, under `cmd/ledger`. It is a small operator CLI for seeding ledger metadata and exercising the bookkeeping workflow.

## Demos

> Generated from the VHS tapes in [`docs/demos/`](docs/demos/) — see that folder to record or re-record them.

**Posting a journal entry in the TUI.** Natural-language request → model reasoning → validated entry preview.

![TUI posting](docs/demos/posting.gif)

**Company policy steering account choice.** The same client-gift transaction lands in 交際費 (6115); after `ledger policy set`, it lands in 廣告費 (6108) and the memo cites the policy.

![Policy flip](docs/demos/policy.gif)

**Correcting a mistaken entry.** First `reverse_journal` mirrors the original and links it with a `reverses` `JournalRelation`; then a follow-up "re-post with the correct amount" is resolved by recent-context recall — the agent looks up what it just reversed and posts the fix, without the request restating the transaction.

![Reversal](docs/demos/reverse.gif)

**Refusing an invalid request.** Asking for a deactivated account is rejected by the validator; the agent does not silently substitute an active one.

![Rejection](docs/demos/reject.gif)

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

ledger close --period 2026-05

ledger policy set --file policy.md

ledger tui
```

`seed` applies one YAML/JSON scenario file (or every `*.yaml` / `*.yml` file in a directory) to the configured repository -- the company, chart of accounts, branches, and periods.

`book-run` connects to the already-seeded ledger, runs one bookkeeping reasoning cycle against `--request`, and prints a JSON report. The reasoning engine is OpenAI-compatible; set `llm.model` and `llm.api_key` in `config.yaml` (or pass `--model`) and optionally `llm.base_url` for alternative providers.

`close` closes an accounting period. For each branch with revenue or expense activity in the period it posts one balanced closing entry that drains every contributing account into the company's Retained Earnings account, links the closing entry back to each source entry through `JournalRelation` rows of type `closes`, then flips `Period.Status` to `closed`. Re-invoking against an already-closed period is a no-op. The use case refuses to close before `Period.End` has actually passed in `Company.TimeZone`, and refuses when no revenue or expense activity exists. The seed must set `company.retained_earnings_code` to the equity account the net income gets plugged into; see [`seed/taiwan_ledger.yaml`](seed/taiwan_ledger.yaml) for an example.

`policy` reads or writes the company bookkeeping policy — operator-authored free-text (sparse bulleted markdown) the agent reads verbatim when choosing accounts, for high-consequence disambiguation rules (entertainment vs advertising expense, travel vs local transportation, repairs vs capitalized fixed asset). `policy set` reads the document from `--file` or stdin; `policy edit` round-trips it through `$EDITOR`; `policy get` prints it. It is event-sourced (`PolicySet`) and deliberately separate from `seed`: re-seeding never clobbers it, so bootstrap a starter policy with `ledger policy set` after `seed`. Distinct from an account's `description`/`aliases`, which are retrieval-only facts baked into the search embedding.

`tui` opens the Bubble Tea terminal UI against the seeded ledger; same OpenAI requirement, no arguments.

### Scheduling closings

`ledger close` is rule-driven and meant to be invoked by an external scheduler once a period has ended in the company's timezone. The use case itself is idempotent, so safe retries are part of the design.

Example `crontab` entry that closes the previous calendar month at 02:00 on the first of each month, with stdout/stderr captured to a log:

```cron
# m h dom mon dow  command
  0 2  1   *   *   /usr/local/bin/ledger close --period "$(date -d 'yesterday' +\%Y-\%m)" >> /var/log/ledger-close.log 2>&1
```

A few notes:

- The cron daemon runs in its own timezone (often UTC or the system local zone). `ledger close` reads `Company.TimeZone` from the seeded company and refuses to close until `Period.End` has actually passed in *that* zone — schedule a few hours after midnight in the company's zone to be safe.
- `Period.ID` is supplied by the cron line (here derived from `date -d 'yesterday'`); `ledger close` does not infer it from the wall clock.
- The user running cron needs the same `~/.flarex/accounting/config.yaml` as interactive runs. For system-wide scheduling, put the config under that user's home or pass `--work-dir <dir>`.

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
  model: gpt-5.5
  api_key: ${OPENAI_API_KEY}  # or set OPENAI_API_KEY in the environment
  base_url: https://api.openai.com/v1  # omit for default; set for compatible providers
```

You can override `llm.model` with `--model <model>` on `book-run`. The API key can come from `llm.api_key` or the `OPENAI_API_KEY` environment variable, with config taking precedence. `llm.base_url` defaults to `$OPENAI_BASE_URL` when unset.

The real API integration test is gated separately:

```bash
ACCOUNTING_RUN_OPENAI_TESTS=1 go test ./agent
```

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

## Distillation Dataset

The TUI can capture each bookkeeping run as a training example, so a strong teacher model's reasoning can later be distilled into a smaller local student (e.g. a quantized Qwen). Capture is opt-in: set `llm.dataset_path` to a JSONL file.

```yaml
llm:
  model: gpt-5.5                                  # the teacher recorded as provenance
  dataset_path: /home/me/.flarex/accounting/corpus.jsonl
```

Then use the TUI as usual; every clean run appends one record:

```bash
ledger tui
# each successful request -> one JSON line in corpus.jsonl
```

Only clean runs are kept — a run whose final intent validated and committed, or that cleanly rejected. Runs that hit an error abort and are dropped, so the domain validator and double-entry balance act as a free rejection-sampling filter on the corpus. The path's directory must already exist (the recorder creates the file, not parent directories); point it outside the repo to keep training data out of version control.

Each line is one `dataset.Record`:

```jsonc
{
  "schema_version": "1",
  "recorded_at": "2026-06-20T08:30:00Z",
  "provenance": { "teacher_model": "gpt-5.5", "prompt_version": "v1" },
  "request":    "付這個月房租 35000，從銀行轉帳",
  "trajectory": [ /* the reason -> tool -> observe llm.CycleEvents */ ],
  "intent":     { /* the final bookkeeping.Intent the teacher committed */ },
  "entry_ids":  [ "JE-0042" ],
  "turns": 2
}
```

`trajectory` keeps the full loop (including tool calls and their results), so the same corpus can be formatted downstream as either a tool-calling or an intent-only training set. `provenance` is curation metadata, not a model input: filter on it so records from a changed prompt or model never mix, then drop it before formatting. Bump `agent.PromptVersion` whenever the prompt or `Intent` schema changes.

This repository only *captures* the corpus. Fine-tuning is out of band: convert the records to a chat `messages` JSONL (reconstruct the teacher's system context per `prompt_version`) and train with the usual tools (Hugging Face `datasets`, Unsloth, QLoRA).

## Tests

Run the full suite:

```bash
go test ./...
```

Do not place the Go build cache inside this repository.
