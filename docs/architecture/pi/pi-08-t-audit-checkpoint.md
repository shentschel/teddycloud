# PI-08/T independent audit checkpoint

Status: **checkpoint published; T and the milestone are not accepted**.

The independent audit at
[`a9909431`](https://github.com/shentschel/teddycloud/commit/a99094313a6d1dce53000406b9856768c7dc130c)
added the required policy-permutation, parallel-array, migration and bounded
fuzz evidence without changing production code. Policy permutations, importer
envelope fuzzing, decision-sequence fuzzing and the real v4 migration audit pass.

The initially failing `TestCatalogMilestoneQueryable` was isolated in
[`ab09e3e9`](https://github.com/shentschel/teddycloud/commit/ab09e3e99ace0a1ad8d98841f4d1972445e47bc2).
Its shared recovery fixture intentionally contains both a NUL-bearing title and
an empty non-NULL model number. Catalog queries validate the complete snapshot,
so this malformed nonmatching row correctly fails closed. The test now proves
that each malformed field is independently rejected, repairs only its local
fixture, and then verifies two distinct catalog-only versions, ambiguity,
restart, restore and unchanged history. No production query defect or production
change was found.

Parent review reran `TestCatalogMilestoneQueryable` ten times. The worker also
passed the full backend, vet, architecture and workspace checks. Exact-head
[Next CI 39667376764](https://github.com/shentschel/teddycloud/actions/runs/39667376764)
completed all four jobs successfully, including SQLite repetition, race and
qualified-filesystem evidence.

At that audit checkpoint, two substantive policy gaps were open:

- [PI08-T-F01 #27](https://github.com/shentschel/teddycloud/issues/27): guarded
  `RegisterContent` and `RegisterCatalogVersion` authoring is absent; SQL-seeded
  fixtures cannot satisfy the milestone.
- [PI08-T-F02 #28](https://github.com/shentschel/teddycloud/issues/28): immutable
  version supersession with retained support and cycle/cross-content rejection
  is absent.

Both stable IDs were searched before publication and are linked from the shared
backlog. They blocked PI-08/T and milestone acceptance; accepted R/A/B evidence
is unchanged.

## F02 refinement checkpoint, 2026-10-24

Status: **F02 refined/implementation-ready**. F02 implementation, independent
acceptance, PI-08/T and the milestone remain open.

The parent reports F01-D independently accepted at
`abe0c956e8f38c20aaae966faafe4115feb409e4`, with exact-head
[Next CI 40056183655](https://github.com/shentschel/teddycloud/actions/runs/40056183655)
fully green. Local inspection confirms the registration implementation and
migration 8 at that base; this documentation refinement does not repeat or
replace the parent's F01 acceptance. The earlier absent-registration finding
above is retained as historical audit evidence.

The [F02 supersession contract](pi-08-f02-supersession-contract.md) fixes command,
authority/revision fences, linear immutable chains, canonical bytes/bounds,
retained support, query/reconstruction, unchanged assignments, migration 9,
accounting/recovery and ordered implementation/test gates. Existing order-command
`Supersedes` remains separate. No production code or GitHub issue was changed by
request `pi08-t-f02-refinement-20261024`; #28 remains the existing delivery issue.
Its proposed replacement body is included in the contract for parent review.
F02 and final independent T acceptance still block the milestone; no hardware,
deployment or production migration evidence is claimed.
