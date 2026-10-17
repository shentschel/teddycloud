# PI-08/B1.1 offline import parser checkpoint

Status: bounded parser/report checkpoint accepted; B1 remains open.

Commit [`2be033f575013f29debc3ddecfaf98aefd61dcb4`](https://github.com/shentschel/teddycloud/commit/2be033f575013f29debc3ddecfaf98aefd61dcb4)
adds a generated-fixture offline JSON envelope parser and complete disposition
report under `internal/adapters/catalogimport`.

The parser performs no persistence, identity allocation, authority grant or
external I/O. It retains explicit pairs, preserves raw legacy-array candidates
as ambiguous, reports every rejected/unsupported field, rejects sensitive
batches before retention or hashing, and applies exact byte/count/depth/string
bounds. Custom/public collisions remain explicit and URI/image values are never
dereferenced.

The five B1 named test families cover dispositions, array ambiguity, absence of
authority, unsupported fields and limits. Pinned Go 1.27.1 Docker verification,
ten repetitions, race, full backend tests, vet and architecture checks passed.
[Next CI 38989029879](https://github.com/shentschel/teddycloud/actions/runs/38989029879)
completed all four jobs successfully.

B1.2 remains required: bind a trusted core source, enforce staging budget,
persist artifact/report atomically, prove exact batch replay and rollback, and
verify that import never accepts catalog facts.
