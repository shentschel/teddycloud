# PI-08/A2 baseline-adoption checkpoint

Status: implementation accepted as a bounded checkpoint; PI-08/A2 remains open.

Finding: `PI08-A2-F02` showed that existing version rows could be queried as
unmanaged baseline data but had no explicit guarded transition into catalog
management. Ordinary Review/Order commands correctly failed closed and were not
an adoption mechanism.

## Delivered boundary

Commit [`ff27c6bf1e0a4c203158a4f44dc59c316adf5e9a`](https://github.com/shentschel/teddycloud/commit/ff27c6bf1e0a4c203158a4f44dc59c316adf5e9a)
adds an explicit baseline-adoption command, domain snapshot, application port
and SQLite transaction. Adoption requires a current core capability, exact
baseline digest and aggregate revision. It initializes unreviewed managed state
without accepting facts or changing existing Content, Tag, version, binding or
receipt rows. Replay is exact; stale data, wrong identity/capability, revocation,
cancellation, rollback and concurrent adoption fail closed.

Commit [`6d5c758b79469b6dffb4ae4de96bbc263466f18c`](https://github.com/shentschel/teddycloud/commit/6d5c758b79469b6dffb4ae4de96bbc263466f18c)
binds persisted review/order state only to the current trusted source,
reviewer and namespace before subsequent commands. The acceptance flow proves
adoption, explicit Review acceptance and Order mutation without granting
authority from stored bytes.

## Evidence

- `TestCatalogBaselineAdoption` proves guarded adoption, unchanged legacy rows,
  managed-unreviewed state, exact replay, restart and FK integrity.
- Rejection tests cover stale revisions/facts/versions, missing or foreign
  baselines, wrong/revoked capabilities, cancellation, rollback revocation,
  changed/foreign replay, corruption and two-owner races.
- The catalog suite and the final adoption–Review–Order flow passed ten
  repetitions under the pinned Go 1.27.1 Docker toolchain.
- [Next CI 38955557089](https://github.com/shentschel/teddycloud/actions/runs/38955557089)
  completed all four jobs successfully, including persistence/race,
  deterministic build/smoke, reproducibility and advisory checks.

## Remaining A2 gate

A2 is not yet accepted. The plan's exact `TestCatalogCorruptRelations` evidence
name still needs a narrow audit against the already distributed corruption
coverage; it must not be satisfied by an empty wrapper. The known intermittent
cancellation-test cause and non-software hardware evidence remain explicit.
