# PI-07 refinement checkpoint review

Status: R design, A and PI-07/B are fully accepted after independent parent
review, including the real helper-process SIGKILL/reopen matrix at
`a62ea20cd420bd5219b8be71fe6c83eddcc91f6d` and green Next CI 37327204076.
The independent T audit, PI milestone and hardware power-loss evidence remain
open. Issue #23 remains open for parent issue maintenance.
Refinement baseline: `aef53a69ab10f1480c1dade9797ce4dd9ce645bd`.

Inspected roadmap/routing, PI-05/06 plans/reviews/contracts, ADRs 0001–0003,
actual Content domain/application/SQLite boundaries, lifecycle and backup code,
and legacy TAF validation/library/range evidence. The
[contract checkpoint](pi-07-taf-storage-contract.md) records conclusions and
the now-concrete gate decisions. The [plan](pi-07-plan.md) and
[budget](pi-07-budget.md) provide bounded implementation checkpoints following
independent design acceptance and fresh admission.

Assumptions: one trusted core owner, qualified local Linux filesystem, immutable
files and synthetic fixtures. Network filesystems, arbitrary same-owner hostile
mutation, public HTTP APIs, conversion/download workers, production migration,
device playback and coordinated media/credential restore are outside this PI
checkpoint. Power-loss and deployment evidence remain unproven.

## Issue disposition

Exact-ID search queries were performed against GitHub for `PI-07/A`, `PI-07/B`,
`PI-07/T` and `PI-07/T-01`, plus checked-in references. Search returned references
in older issues, but no exact A/B/T task identity. Proposed task IDs are
`PI-07/A`, `PI-07/B`, `PI-07/T`; none was published or modified. Recheck exact
identity before later publication. Reuse existing open
[PI-07/T-01 #17](https://github.com/shentschel/teddycloud/issues/17), whose body
was fetched and read. Its evidence belongs inside T. No duplicate missing-media
issue is proposed.

Next action: separately admit PI-07/T after fresh capacity checks. Existing
PI-06 review accepts T and the transactional Tag milestone; older status text
in its plan is stale, not grounds to repeat completed work.

Verification for this documentation checkpoint is the architecture-document
checker and diff whitespace check; final tool results accompany the handoff.
Implementation tests listed in the plan are future requirements, not passes.

## Gate dispositions and acceptance boundary

| Gate | Selected decision and review focus |
| --- | --- |
| TAF validation | Versioned `taf-envelope-v1`, exact 4092-byte protobuf, required/optional fields, padding, bounded unsigned decoding, 0..99 tracks, checked offsets, finite length/EOF and payload SHA1. Payload remains opaque; no codec/playback certificate. Shared deterministic envelope fixture belongs to A. |
| Limits/read policy | Concrete byte, duration, concurrency, staging/range/inventory/quarantine caps; full same-descriptor hash/envelope check before any range output, no mtime cache. Deadline failure is unavailable, not corruption. |
| Import/schema | Independent `imp_` key, exact versioned binary fingerprint, retained canonical command bytes, additive schema 4, explicit immutable ContentVersion binding and exact replay/conflict. No catalog deduplication from digest or AudioID. |
| Linux/locks | Qualified ext4/XFS, openat2 confinement and renameat2 RENAME_NOREPLACE, file and both directory flushes, lifetime root ownership lock. Existing serialized lifecycle gate spans the operation; internal session enters SQL without recursively acquiring the gate. |
| Recovery/cleanup | Named tests for every original recovery row; bounded generation-sensitive inventory, explicit corruption quarantine and exact repair. All files retained; no deletion/GC/TTL or pin release in this PI. #17 requires an actual removed fixture. |

No design choice remains intentionally unspecified. Parent review checked the
command byte counts, existing 30-byte identity representation, additive schema
constraints, lock ordering, validation profile and every recovery outcome.
The design is accepted. Implementation evidence was open at R acceptance;
subsequent delivery acceptance is recorded below.

Risks retained explicitly: strict envelope acceptance may reject legacy files;
generated fixtures do not prove audible media; 1-GiB full verification can exceed
the read deadline; serialized imports can delay metadata; retained stages can
fill capacity; syscall probes cannot prove power-loss durability; kernel I/O can
outlive cancellation. These are selected scope/operational limits with tests,
not implementation discretion. Production compatibility/migration/deployment,
coordinated media/secret restore and later event delivery remain unproven.

Required handoff checks are `python3 scripts/check_architecture_docs.py` and
`git diff --check`; the final report records observed outcomes. This change has
no runtime test evidence. Delivery tasks are published as
[PI-07/A #22](https://github.com/shentschel/teddycloud/issues/22),
[PI-07/B #23](https://github.com/shentschel/teddycloud/issues/23) and
[PI-07/T #24](https://github.com/shentschel/teddycloud/issues/24); T reuses
`PI-07/T-01` in existing #17.

## A1 domain acceptance

Commit `90c4960902228e4b13153d559182e110aa55d32e` implements the
accepted envelope, BlobID and canonical import-command domain contracts with
generated synthetic fixtures and boundary/fuzz tests. Parent focused/full tests,
vet and bounded fuzzing passed. Local race evidence was unavailable without a C
compiler, so commit `fe969a2d16182490bdfe866844be4a6058511de1`
added the packages to remote Linux race CI; run 37180869682 passed all jobs.

A1 is accepted without a playback-validity, filesystem, database, production
migration or deployment claim. A2 supplies the separately reviewed filesystem
boundary and completes issue #22.

## A2 qualified filesystem acceptance

Commit `7e86d2bfaa3b6806f327471ed915b59655fea3ff` implements the
descriptor-owned Linux content store with ext4/XFS qualification, exclusive
ownership, bounded staging, exact validation, no-replace publication, explicit
flush boundaries, verified reuse and fail-closed non-Linux behavior. Parent
full/focused tests, vet, repetition, cross-build and whitespace checks passed;
the worker also passed focused race testing. Remote Next CI run 37199532054
passed the qualified filesystem, repetition, race and cross-build gates.

A2 and issue #22 are accepted. Retained-entry reconciliation remains ordered in
B2. Physical power-loss durability, playback, database binding, migration,
production deployment and deletion remain outside this acceptance.

## B1 transactional metadata acceptance

Commit `c8eb4d68bd9d883c04c12371c974d36766f355e0` adds migration 4,
owner-scoped revocable content sessions and atomic import metadata with exact
replay/conflict, corrupt-row refusal and uncertain-commit readback. Parent
full/focused tests, vet, repetition and whitespace checks passed; the worker
also passed focused race tests. Remote Next CI run 37201527034 passed all jobs.

B1 was accepted without a present-media claim. Issue #23 stays open; accepted
B2a/B2b filesystem primitives and bounded B2c integration are recorded below.
Real helper-process recovery was outstanding at B1 acceptance; full B acceptance
is recorded below.

## B2a verified ranges acceptance

Commit `1a3ee4c49567adcbf9cf2c18d7a3c900a06b0c3e` adds bounded,
descriptor-verified range delivery with typed missing/corrupt outcomes and no
output before full BlobID/envelope verification. Parent focused/full tests,
vet and whitespace checks passed; Next CI run 37202334719 passed all jobs.

Issue #23 remains open. B2b inventory/quarantine acceptance follows below;
bounded B2c cross-boundary reconciliation and subsequent real helper-process
recovery acceptance are recorded below.

## B2b bounded inventory and quarantine acceptance

Code commit `91f839f3817746500327f24dc943aaad7d07e80a` implements bounded
content-store inventory and explicit quarantine primitives. Inventory provides
the five required classifications, bounded pages/inspection/descriptors and
single-use cursors invalidated by mutation, restart, idle expiry or the explicit
restore hook. Maintenance startup fences mutations until retained capacity has
been reconciled. Quarantine freshly verifies corruption under the exclusive
Store operation lease, preserves bytes with generated no-replace destinations,
flushes both directories and does not remove database references.

The parent reviewed the code commit and storage contract, and confirmed
[Next CI run 37220679818](https://github.com/shentschel/teddycloud/actions/runs/37220679818)
green with all four jobs successful. Worker full backend tests, vet, ContentFS
race tests, ten focused repetitions, Darwin cross-build and whitespace checks
passed. B2b is accepted as this bounded adapter checkpoint only.

At B2b acceptance, application/lifecycle integration, gated database reference
reconciliation and helper-process recovery were still open. The bounded B2c
integration acceptance follows below. B and T were incomplete at that checkpoint;
the subsequent full B acceptance is recorded below. Issue #23, T, the PI milestone
and hardware power-loss evidence remain open.

## B2c bounded application and lifecycle reconciliation acceptance

Code commit `c55637a2672c9c85ea490a5d1b69e05788023dca` connects the accepted
inventory/quarantine primitives to the existing content-operation lifecycle
gate and the currently selected, revocable database session. Reference writes
and restore invalidate inventory cursors. Quarantine follows the gate/store
lease order, retains database facts and reports typed missing availability
after a move. Active imports and verification reads fence maintenance and
restore; no SQL transaction spans filesystem I/O or recursively enters the gate.

The parent reviewed service, ports, lifecycle/reference code and tests against
the PI-07 contract and verified
[Next CI run 37222899075](https://github.com/shentschel/teddycloud/actions/runs/37222899075)
fully green with all four jobs successful. Local full backend tests, vet,
relevant race tests, ten focused repetitions and whitespace checks passed.
Deterministic application failpoint/retry tests cover publication before DB
commit, orphan reuse and lost-response readback. Selected current/pre-v4 restore,
cursor invalidation, quarantine retention and connection-release tests passed.

Accepted bounded limitation: schema 4 has no digest index on version bindings.
ReferenceLookup inspects at most 1,024 bindings within its context/time bound;
when absence cannot be established it returns unavailable, never a false orphan
classification. Existing migration checksums are unchanged. Content transactions
conservatively invalidate cursors even when the callback only reads metadata.
This limitation does not establish unrestricted large-catalog reconciliation.

B remained open at B2c acceptance for actual helper-process termination after
durable publication before DB commit, and before/after quarantine rename and
each directory sync. The deterministic failpoint alone was not process-kill/
reopen evidence. The subsequent acceptance below completes that B rest matrix.

## B helper-process recovery and full sprint acceptance

The parent independently reviewed code commit
[`a62ea20cd420bd5219b8be71fe6c83eddcc91f6d`](https://github.com/shentschel/teddycloud/commit/a62ea20cd420bd5219b8be71fe6c83eddcc91f6d),
both test-only files (`internal/adapters/contentfs/recovery_helper_test.go` and
`internal/adapters/sqlite/content_recovery_process_test.go`) and
[Next CI 37327204076](https://github.com/shentschel/teddycloud/actions/runs/37327204076),
whose four jobs are green. PI-07/B is fully accepted.

The tests start separate helper executables, park at observed real boundaries,
hard-kill and reap them with SIGKILL, then reopen the actual Store and SQLite
database. Existing hooks do not fabricate successful syscall results. The
named scenarios map to the following real positions:

| Scenario labels | Observed position |
| --- | --- |
| `publish-before-commit` | Store publication has returned after file and both directory syncs; the application DB transaction has not started. |
| `before-rename` | Immediately before the real quarantine no-replace rename. |
| `after-rename`, `before-source-sync` | The same real intermediate position: rename succeeded, source-directory sync has not begun. |
| `after-source-sync`, `before-destination-sync` | The same real intermediate position: source-directory sync succeeded, destination-directory sync has not begun. |
| `after-destination-sync` | Quarantine has returned after both directory syncs and descriptor revalidation, while the application lifecycle gate remains held. |

Thus seven named scenarios cover five observed positions, including application
return boundaries; they are not seven physically different syscall positions.
After each kill, bounded inventory and direct file-byte checks establish actual
placement. DB references and import facts remain intact; availability is typed
missing or corrupt as appropriate. Exact retry verifies/reuses the durable orphan
or repairs quarantined bytes, retaining exactly one version binding and import
receipt. A second real reopen verifies the repaired result persists.

Ten focused matrix repetitions, full backend tests, vet, relevant race tests
(including race-built local helpers), architecture checks and CI regressions,
filesystem qualification and Darwin cross-build passed. This evidence concerns
process termination and reopen, not hardware power-loss durability.

The independent T audit (including #17), PI milestone and hardware power-loss
evidence remain open. Issue #23 is deliberately left open for the parent to
maintain. This closeout makes no code change, deployment or merge.
