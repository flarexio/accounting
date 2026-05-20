# Ledger seeds

Declarative YAML seed files for the accounting ledger. Each file is an
`accounting.Scenario`: a company with its chart of accounts, branches, and
accounting periods. This is setup data: it must exist before any journal
entry is posted.

Apply a seed with the `accounting seed` command, which upserts the metadata into
the repository configured in `config.yaml`:

```bash
# one file
go run ./cmd/bookkeeping seed seed/taiwan_ledger.yaml

# or every *.yaml / *.yml file in a directory
go run ./cmd/bookkeeping seed seed/
```

Seeding is declarative and idempotent: a file describes the desired state,
and re-running `accounting seed` converges to it rather than accumulating. A seed
is one logical config; splitting it across several files in a directory is
an organisational choice, not an ordered migration.

Schema is owned separately by the out-of-band `golang-migrate` step; `seed`
owns data.
