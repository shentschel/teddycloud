# PI-01 review

Status: R/B/T complete locally; A reviewed and reused; remote CI evidence pending

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
feature-branch push alone does not establish a green CI result. No deployment or
hardware tests are claimed here.

## Remaining work

F-01 remains partially open for browser/device qualification; F-02/F-03 require
later contract and schema fixtures. F-05 issue publication remains non-blocking.

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
need observation. A PR-triggered build/release workflow may also run, so the
documentation workflow is now scoped to the `docs/pi-*` branch push to obtain
proportional CI without triggering a release. The remote result remains pending.

## T consistency review

T corrected the stale R/B candidate wording, distinguished SQLite commits from
durable blob publication, recorded the core event outbox separately from Event
Hub delivery, and made desired-versus-legacy-applied assignment state explicit.
The backlog now states that A was delivered early and revalidated; it cannot be
admitted a second time. The roadmap distinguishes a candidate milestone ID from
the one shared weekly quota interval.

| Journey | Planning artifact and owner | Runtime evidence still required |
| --- | --- | --- |
| J-01 current tag | Context event/outbox boundary; PI-02/11/29 | Late-response and reconnect fixture/device result |
| J-02 Copy | ADR-0001/0003 desired/applied revision; PI-09/29 | Assignment, stale projector and readback failure injection |
| J-03 Set member | Charter separate Tag/Content/Version/Set; PI-13/16/17 | Independent member/model/cover fixtures |
| J-04 lookup conflict | Source-observed crossed pair, charter exact-pair rule; PI-04/15 | Positive/negative and shared-audio lookup tests |
| J-05 preferred Original | Charter C-03/C-04 and Library Query; PI-09/10/28 | Preferred query plus protected multi-card import tests |
| J-06 playlist | Charter media worker and part rules; PI-31 | Three-card and chapterless media tests |
| J-07 sync | Context connector/TAF recovery; PI-32 | Retry, rate-limit and interrupted import tests |
| J-08 UI extension | ADR-0002 SDK/slots and extension registry; PI-18/21/24/28 | Disable, compatibility and failing-plugin tests |
| J-09 import/rollback | ADR-0001/0003 backup and fencing; PI-34/37/38 | Disposable snapshot restore and semantic reconciliation |

All seven enhancements remain mapped in [component ownership](../component-ownership.md),
including independent repositories and public SDK compatibility. Source-reviewed
GET write-back and audio-pair collision have owners F-02/F-03. F-04 terminology
and quota were closed in PI-00/T; F-06 design is accepted but its runtime proof
remains with PI-05/07/09/36/37. F-01 browser/device evidence and F-05 Issues
publication remain open; neither is represented as passed.

Local architecture checks are passing. The architecture charter is a locally
accepted implementation baseline, with external CI and hardware/migration gates
open. The roadmap's remote-green definition of done is not yet evidenced, so
the PI-01 milestone is **not marked complete**. PI-02/R can refine fixtures on
this accepted baseline while the remote documentation check is observed.
