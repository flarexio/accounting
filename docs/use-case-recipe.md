# Use-Case Recipe

A closed, repeatable checklist for adding a bookkeeping operation. The goal is
go-kit-level reflex: stop deciding *where things go* and just follow the steps.

**go-kit translation.** A use-case struct here is a go-kit `Service` (the
business core). `Validate`/`Execute`/`Handle` are its methods. The event bus
(`Publisher`/`Subscriber`/`Router` + `inproc`/`nats` adapters) is the transport.
There is **no `Endpoint` layer** and no request/response boxing — methods take
concrete domain types. Don't reach for an endpoint/middleware layer until there
is a real external API to serve.

---

## Step 0 — Pick the flavor

| | **A. Agent-driven** (Intent) | **B. Operator-driven** (non-Intent) |
|---|---|---|
| Example | `PostJournal`, `ReverseJournal` | `ClosePeriod` |
| Trigger | the LLM emits an `Intent` | a human/scheduler runs a CLI command |
| Use when | the model decides *whether and how* | policy is too company-specific to delegate to a model |
| Routed by | `bookkeeping.Registry` | a `cmd/ledger` command, directly |

If unsure: a thing the agent should choose to do → A; a rule-driven operation a
person/cron triggers → B.

---

## The shared spine (both flavors)

Create `bookkeeping/<name>.go`:

```go
type DoThing struct {
    Repo      accounting.LedgerRepository
    Publisher Publisher
    Clock     Clock   // omit if the op stamps no instant
    Subject   string  // the event subject; default in Execute
}

// Validate: pure, no side effects, domain invariants only.
func (uc DoThing) Validate(ctx context.Context, in InType) error { ... }

// Execute: the write. Does NOT re-validate.
func (uc DoThing) Execute(ctx context.Context, in InType) (OutType, error) { ... }

// Handle: Validate then Execute — the safe entry point for unvalidated callers.
func (uc DoThing) Handle(ctx context.Context, in InType) (OutType, error) {
    if err := uc.Validate(ctx, in); err != nil { return zero, err }
    return uc.Execute(ctx, in)
}
```

Rules that make it consistent:

- **Deps are struct fields**, defaulted inside `Execute` (`Subject` → its
  `accounting.Subject*` const; `Clock` → `time.Now().UTC`). Nil `Repo`/`Publisher`
  is an error, not a panic.
- **`Validate` runs no side effect.** Reuse `accounting.Validator{Repo}` for the
  standard invariants; add operation-specific checks alongside.
- **`Execute` never writes the projection directly. It publishes.** The shape is
  always:
  1. `lastSeq, _ := uc.Repo.LastSequence(ctx, subject)` — optimistic-concurrency
     token *and* the new entry's id basis (`accounting.FormatEntryID(lastSeq+1)`).
  2. Build the domain value(s) from `in`.
  3. `dispatched, err := uc.Publisher.Publish(ctx, <Event>{...}, accounting.ExpectedSequence{Subject: subject, LastSeq: lastSeq})`.
  4. Type-assert `dispatched` back to the concrete event; return the domain result.
- **`Handle` = Validate + Execute.** Errors are plain Go errors; on the agent
  path the loop turns them into typed cycle events fed into the next reasoning turn.

> The repository is written only by the **projection handler** that consumes the
> event (see "New event type" below), never by the use case.

---

## Flavor A — register an Intent

1. **`bookkeeping/intent.go`**
   - add the `IntentKind` const (e.g. `IntentDoThing IntentKind = "do_thing"`),
   - add a payload pointer field on `Intent` (`DoThing *DoThingIntent`),
   - add the payload type + its `…ArgsShape` JSON skeleton,
   - add a descriptor in `Intents()`. This is the single source of the agent's
     vocabulary; the prompt and `IntentSchema()` follow from it.
2. **`bookkeeping/registry.go`** — add a route in `NewBookkeepingRegistry`:
   construct your use case from the shared `repo`/`pub`/`clock`/`subject`, then a
   `{validate, execute}` pair that unwraps `intent.DoThing` (missing payload →
   `missingPayloadErr`) and calls `uc.Validate` / `uc.Execute`.
3. The agent loop drives it via `Registry.Validate`/`Registry.Execute` — no CLI
   wiring needed.

## Flavor B — wire a CLI command

1. Give the use case its own `…Intent` and `…Result` types (not part of the
   agent's `Intent` union).
2. **`cmd/ledger/<name>.go`** — a `cli.Command` whose action:
   `loadBookConfig` → `buildRepository` → `buildMessaging(repo)` → construct the
   use case with `Repo`/`Publisher: bus` → `uc.Handle(ctx, intent)` → JSON-encode
   the result to stdout.
3. Register it in `cmd/ledger/main.go`'s `Commands` slice.

---

## New event type (only if the op emits a kind the bus doesn't carry yet)

1. **`event.go`** — `Subject…` const, the event struct, and `EventSubject()`.
2. **`bookkeeping/apply<x>.go`** — an `EventHandler` that type-asserts the event,
   translates it to domain model(s), and calls a **domain-typed** repo method
   (`AppendEntry`, `SetPeriodStatus`, `Put*`). The transport sequence is read from
   the context (`accounting.EventMetaFrom`) by the repo — the handler does not
   thread it.
3. **`cmd/ledger/compose.go`** — register the handler on the Router in
   `buildMessaging`: `.On(accounting.Subject…, &bookkeeping.ApplyX{Repo: repo})`.
4. **`messaging/nats/accounting.go`** — add the subject to `supportedSubjects`
   and a case in `encodeEvent` / `decodeMsg`.

> Add a repository method only when the projection needs a new write shape; keep
> it domain-typed and let it read `EventMeta` from the context for the sequence.

---

## Test it

- Use-case unit test: a fake `Publisher` capturing the published event; assert
  `Validate` rejects bad input and `Execute` publishes the right domain value
  with the right `ExpectedSequence`. Inject a fake `Clock`.
- Projection test: drive the handler (or publish through `inproc`) and assert the
  repository reflects it; `LastSequence` advanced.
- `go build ./...`, `go vet ./...`, `go test ./...`, gofmt — all green.
