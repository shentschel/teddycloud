# PI-08/A1.3b query contract checkpoint

Status: accepted internal contract refinement; PI-08/A and the milestone remain incomplete.

Implementation: `649773238bd90c1114fea9234a044ca212792c48`

## Delivered

The internal application query record now carries stored
`domain/catalog.ContentFacts` as a baseline separate from reviewed
`CatalogField` projections. A typed internal review state distinguishes
unmanaged baseline, managed-but-unreviewed content and reviewed content without
creating public V3 vocabulary or changing the domain model.

Result validation rejects impossible state/projection combinations, malformed
stored facts, invalid support sets, unsorted alternatives and content/version
shape mismatches. Baseline title, model and article values remain visible
without becoming accepted projections; reviewed corrections and conflicts do
not overwrite stored facts.

Changed code scope:
- `next/backend/internal/application/catalog/queries.go`
- `next/backend/internal/application/catalog/queries_test.go`

## Verification

Next CI [38564201950](https://github.com/shentschel/teddycloud/actions/runs/38564201950)
passed all four jobs for the implementation SHA, including gofmt, go vet,
application/catalog tests, deterministic workspace checks, SQLite repetitions,
persistence/domain race evidence, qualified filesystem checks, reproducibility,
browser smoke and advisory checks.

The local process launcher remains unavailable, so no local clean-worktree or
local test claim is made.

## Deferred

A2.3 still requires the read-only catalog repository/transaction adapter,
durable result materialization, bounded catalog-only queries and revision-bound
cursor behavior. No public API, schema change, production migration or milestone
acceptance is included.
