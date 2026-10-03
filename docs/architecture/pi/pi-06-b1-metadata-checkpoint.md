# PI-06 B1 reviewed Tag metadata checkpoint

Status: accepted B1 checkpoint

Commit `b186337d7a33fdf37a5b6b1ac2e2397b14e6c97f` persists bounded,
reviewed metadata history independently from Tag identity.

## Delivered behavior

- `protocol_valid`, `claimed`, `cloud_auth` and `owned` remain independent
  unknown/true/false/conflict facts.
- Immutable observations and explicit support decisions retain dissent and
  superseded decisions. Confidence, time and import order never choose a winner.
- Exact replay is a no-op; partial, changed, cross-Tag and stale commands return
  typed conflicts without partial writes.
- Revision-fenced SQLite writes persist observations, decisions and support in
  one callback transaction. Savepoint and injected-failure tests prove rollback.
- Migration 3 is additive. Persisted reads enforce count, scalar and exact-byte
  preflight before bounded materialization; corrupt or oversized rows fail closed.
- Same-revision updates across separate SQLite connections produce one winner,
  a typed loser and no lost evidence.

## Evidence

The implementation and acceptance agents ran Go 1.27.1 formatting, full backend
tests, `go vet`, focused ten-fold repetitions and the named metadata tests on
Linux. Parent reviewed the complete production/test diff and corrected support
membership validation so it stays allocation-free before byte admission.

[Next CI run 37121744095](https://github.com/shentschel/teddycloud/actions/runs/37121744095)
passed all four jobs, including focused SQLite/Tag regression and race evidence.

## Boundaries

B1 does not establish lifecycle recovery or FK-bearing snapshot acceptance.
Those remain B2 work gated by F-PERSIST-01 (#18). No authentication execution,
assignment, blob, HTTP, legacy import or production migration is included.
