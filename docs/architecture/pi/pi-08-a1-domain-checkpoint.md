# PI-08/A1.1 catalog domain foundation checkpoint

Status: accepted bounded checkpoint; PI-08/A remains incomplete.
Issue: [PI-08/A #26](https://github.com/shentschel/teddycloud/issues/26).

## Delivered scope

Commits `f9cadca4cbabb71d303d7d1990fcc7113ecf4845` and
`6f52c01294a0a2655801f2f45942d77831c4dd6b` add only the catalog-domain
foundation in `next/backend/internal/domain/catalog`:

- core-created opaque source, issuer, registration and capability types;
- immutable artifact, record-coordinate and catalog-observation provenance;
- confidence represented independently from review or mutation authority;
- bounded text, record, observation, snapshot and nesting limits;
- value-free prohibited-field rejection;
- tests for spoofing, boundaries, changed same-ID claims, source/subject
  mismatches and deterministic permutations.

A1.2 decisions, replay, revocation, protected-custom and effective-order behavior,
A1.3 application services, and all persistence/import work remain open.

## Verification

Next CI run [38360289683](https://github.com/shentschel/teddycloud/actions/runs/38360289683)
passed all four jobs for the final formatting commit, including deterministic
workspace gates, catalog tests, persistence/domain race evidence,
reproducibility, browser smoke and network advisory checks.

The local process starter remained unavailable after a host restart with
`Failed to create unified exec process: No such file or directory (os error 2)`.
Therefore no local clean-worktree or local test claim is made. Remote commit
scope and CI provide the checkpoint evidence.

## Acceptance boundary

This checkpoint introduces no schema, network acquisition, public API, review
decision state machine, Tag change, media behavior or production migration.
Rollback is the focused additive source revert. PI-08/A and the queryable catalog
milestone are not accepted by this checkpoint.
