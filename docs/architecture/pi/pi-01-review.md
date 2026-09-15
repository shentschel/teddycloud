# PI-01 review

Status: R/B complete; A reviewed and reused; T outstanding; milestone incomplete

## R outcome

- [Execution plan](pi-01-plan.md) prioritizes every J-01 through J-09 journey and
  preserves the existing seven enhancement mappings.
- [Reference environment](pi-01-reference-environment.md) records read-only
  deployment observations and distinguishes hardware, source and user evidence.
- Performance targets remain engineering goals until reproducible measurements;
  correctness, Custom Card preservation and explicit lookup conflicts remain
  mandatory acceptance rules.
- [Budget method](pi-01-budget.md) reserves T, reuses A and allows further safe
  work within the same weekly allowance after a completed slice.
- Reusing A identified a missing filesystem/SQLite commit protocol and unclear
  assignment-versus-playback success. F-06 assigns both to B and later tests.

## Verification

R reviewed the charter, context, ownership, source-level GET write-back and the
crossed-pair lookup. Host inspection verified only the documented OS/resource/
service properties. No device traffic or credentials were captured.

Local verification passed: `python3 scripts/check_architecture_docs.py` checked
20 architecture documents; `git diff --check` passed.
The existing docs workflow triggers for pull requests and mainline pushes; a
feature-branch push alone does not establish a green CI result. Remote CI will
be recorded when queried; no deployment or hardware tests are claimed here.

## Remaining work

T must verify the resulting architecture and close this milestone. F-01 remains
partially open for browser/device qualification; F-02/F-03 require later contract
and schema fixtures. F-05 issue publication remains non-blocking.

## B outcome

- [ADR-0001](../adr/0001-control-plane-and-gateway-sequencing.md) accepts
  control-plane-first sequencing with explicit operation ownership and rollback.
- [ADR-0002](../adr/0002-technology-and-repository-topology.md) selects a modular
  Go core, React/TypeScript, independently versioned SDK and isolated workers.
  Initial workspace scaffolding uses this fork; existing enhancement repositories
  retain their supported contracts and releases.
- [ADR-0003](../adr/0003-persistence-and-projection-recovery.md) defines separate
  durable blob/DB steps, recovery, desired/applied assignment revisions, projection
  fencing and coordinated backup retention. F-06 design is complete; failure
  injection evidence is assigned to implementation sprints.
- J-02/J-03/J-09 design walkthroughs expose interruption and ambiguous identity
  states. Source/model metadata is not used as physical card classification.
- Local documentation checks passed across 23 documents and whitespace checks
  passed. No runtime tests are claimed for these design documents.

The GitHub connector returned no pull-request-triggered runs for R commit
`cdbe7fe645a3b9778c028013cf7c84543559eaa1`. That query is limited to PR runs and
does not prove that all other workflow types are absent. B/T remote checks still
need observation; the PI milestone stays incomplete until T records its result.
