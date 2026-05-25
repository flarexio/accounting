# Ledger seeds

Declarative seed files for the accounting ledger. Each file is an
`accounting.Scenario`: a company with its chart of accounts, branches, and
accounting periods. This is setup data: it must exist before any journal
entry is posted.

YAML and JSON are both accepted; `accounting.LoadScenarioFile` picks the
decoder by extension. `ledger seed` is the primary consumer, but tests and
benchmarks load the same files directly.

`bench/` holds `*.case.yaml` files for `ledger bench`: each pairs a scenario
in this directory with a natural-language request and a gold answer used to
score model proposals.

Apply a seed with the `ledger seed` command, which upserts the metadata into
the repository configured in `config.yaml`:

```bash
# one file
go run ./cmd/ledger seed seed/taiwan_ledger.yaml

# or every *.yaml / *.yml file in a directory
go run ./cmd/ledger seed seed/
```

Seeding is declarative and idempotent: a file describes the desired state,
and re-running `ledger seed` converges to it rather than accumulating. A seed
is one logical config; splitting it across several files in a directory is
an organisational choice, not an ordered migration.

Schema is owned separately by the out-of-band `golang-migrate` step; `seed`
owns data.
