# PI-07 refinement checkpoint review

Status: R design, A and PI-07/B are fully accepted after independent parent
review, including the real helper-process SIGKILL/reopen matrix at
`a62ea20cd420bd5219b8be71fe6c83eddcc91f6d` and green Next CI 37327204076.
The independent T audit, PI milestone and hardware power-loss evidence remain
open. Issue #23 is closed/completed (`closed_at`: 2026-10-05T15:02:39Z).
Finding PI07-T-F01 was subsequently confirmed High, fixed and parent-accepted
at `76cab7a33eaf715cd0209653c9be2236ced63b94`, with all four jobs of Next CI
37375380712 green. PI-07/T-01 #17 is also accepted and closed at
`39c4c3e4f90231ffd07bbb5597a413485bb16d5d`; the rest of T remains open.
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

B1 was accepted without a present-media claim. Issue #23 is now closed/completed
after full B acceptance; accepted B2a/B2b filesystem primitives and bounded
B2c integration are recorded below.
Real helper-process recovery was outstanding at B1 acceptance; full B acceptance
is recorded below.

## B2a verified ranges acceptance

Commit `1a3ee4c49567adcbf9cf2c18d7a3c900a06b0c3e` adds bounded,
descriptor-verified range delivery with typed missing/corrupt outcomes and no
output before full BlobID/envelope verification. Parent focused/full tests,
vet and whitespace checks passed; Next CI run 37202334719 passed all jobs.

Issue #23 is now closed/completed after full B acceptance. B2b inventory/quarantine
acceptance follows below; bounded B2c cross-boundary reconciliation and subsequent real helper-process
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
the subsequent full B acceptance is recorded below. Issue #23 is now
closed/completed; T, the PI milestone and hardware power-loss evidence remain open.

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
evidence remain open. Issue #23 is closed/completed after full B acceptance.
This closeout makes no code change, deployment or merge.

## PI-07/T independent audit checkpoint (2026-10-05)

Status: incomplete, stopped on explicit user instruction. T and the PI
milestone are not accepted. Baseline branch `docs/pi-00-r-charter`, HEAD
`7ca1a2f60d61c08a4cde1f85b3899d5fbd8d60e3`, was clean at entry and stop.
No runtime or test files were changed. Private automation state is unchanged.

### Observed evidence

- `go test ./...` from `next/backend` completed with exit 0 using the existing
  `golang:1.27.1-bookworm` container and isolated Linux filesystem fixtures.
  All backend packages passed, including contentfs, SQLite, contentstore and
  architecture tests. The command finished before the stop-status check;
  no further test execution was started after the stop instruction.
- GitHub run [37327204076](https://github.com/shentschel/teddycloud/actions/runs/37327204076)
  was independently fetched: all four jobs succeeded. This is historical
  B evidence, not CI evidence for this audit checkpoint.
- Existing issue [#17](https://github.com/shentschel/teddycloud/issues/17)
  was read and remains open. This audit has not added or executed a new
  application-level removal/reimport test satisfying all its assertions.

### Findings and acceptance blockers

| ID | Severity / evidence level | Observation and required follow-up |
| --- | --- | --- |
| PI07-T-F01 | Confirmed High; fixed and parent-accepted | Runtime reproduction confirmed that lowering import admission misclassified healthy retained bytes as corrupt and allowed quarantine. Fixed at `76cab7a33eaf715cd0209653c9be2236ced63b94`; Next CI 37375380712 passed all four jobs. See the finding closeout below. |
| PI07-T-G01 | Blocking audit incompleteness | The complete recovery/resource/lock/restore/idempotency matrix has not been independently reconciled against implementation and tests. A green backend suite alone does not close this gate. |

No confirmed critical runtime finding is claimed. PI07-T-F01 is resolved;
the incomplete audit still blocks a T/PI acceptance recommendation.

### Resume checklist

Finish the required preflight reads (the large private state output was
truncated and its final reread was interrupted), then complete the independent
contract-to-code/test matrix. Explicitly finish #17 real removal and exact
reimport evidence, typed error distinctions, configured operation deadlines,
range/lifecycle fencing, concurrent imports, all resource bounds and sanitized
error-tree coverage. Reconcile the existing process-kill scenarios with each
required recovery row; process termination does not prove power-loss durability.

`go vet`, dedicated race/repetition/fuzz commands and the architecture-document
checker were not run in this audit before the stop. They remain required, as
does CI on any subsequent corrective implementation. No issue was modified.
No schema change, deletion/GC, deployment or mainline merge was performed.
Hardware power-loss, real playback and coordinated production media/secret
restore remain unproven. Recommendation: retain this checkpoint and keep T/PI
acceptance blocked; do not repeat already accepted delivery as a new task.

## PI07-T-F01 finding closeout (2026-10-05)

The narrowly scoped follow-up confirmed the High finding with executable
evidence. `TestBlobRetainedVerificationAfterLowerImportLimit` publishes a valid
12,289-byte generated TAF under the original admission profile, closes the
owner and reopens it with a 4,097-byte import limit. Before the fix, Verify and
ReadRange returned `ErrCorrupt`, and Quarantine incorrectly succeeded.

Fix commit [`76cab7a33eaf715cd0209653c9be2236ced63b94`](https://github.com/shentschel/teddycloud/commit/76cab7a33eaf715cd0209653c9be2236ced63b94)
changes only `contentfs/store_linux.go` and
`contentfs/verification_linux_test.go`. Retained verification now validates
the stored envelope profile using its fixed 1-GiB ceiling and the verification
deadline, independently of current import admission settings. Earlier caller
deadlines still win; invalid verification configuration is unavailable rather
than evidence of corruption. Imports continue to enforce their configured
limit. Full descriptor/hash/EOF validation, finite reads and confinement remain
in force. Genuine byte corruption still returns `ErrCorrupt`, emits no range
bytes and permits explicit quarantine with retained bytes and typed missing
availability afterward.

The regression was observed failing before the fix and passing afterward.
Local full backend tests, `go vet ./...`, relevant ContentFS/ContentStore/SQLite
race tests, ten focused verification/quarantine repetitions, formatting,
architecture tests/document checks and whitespace checks passed. Parent review
is complete; [Next CI run 37375380712](https://github.com/shentschel/teddycloud/actions/runs/37375380712)
was independently checked and all four jobs succeeded.

PI07-T-F01 alone is closed. PI-07/T and the PI milestone remain open for the
remaining independent contract-to-code/test audit, including actual removal
and exact re-import evidence for [issue #17](https://github.com/shentschel/teddycloud/issues/17).
The focused evidence does not establish completion of the remaining recovery,
resource, lifecycle, error-sanitization or fuzz audit. Hardware power-loss,
real playback and production migration/deployment evidence remain unproven.

## PI-07/T-01 missing-media recovery acceptance (2026-10-06)

Commit [`39c4c3e4f90231ffd07bbb5597a413485bb16d5d`](https://github.com/shentschel/teddycloud/commit/39c4c3e4f90231ffd07bbb5597a413485bb16d5d)
adds Linux integration evidence in `adapters/sqlite/missing_blob_test.go` without
changing production code. A generated TAF is imported, its generated fixture file
is removed through test-only access, and both availability and verified range
delivery return typed missing results without output. The committed Content,
version, blob binding and import receipt remain unchanged, including the protected
synthetic catalog record.

A current-schema database backup/restore retains those facts while media remains
absent. Exact verified re-import with the original command repairs availability
and range bytes while retaining exactly one blob, version, binding and receipt.
Separate cases distinguish invalid typed identity, same-size digest corruption,
missing catalog content and an unavailable database. Existing production behavior
already satisfied the new test; no corrective product change was necessary.

Focused missing-media tests, the full backend, vet, relevant race suites, focused
and SQLite repetitions, architecture checks and the Darwin cross-build passed
locally. Initial CI 37422149365 exposed unrelated newly published advisory
GHSA-68fv-2mgg-jv7q in locked `source-map-js@1.2.1`. The lock-only refresh
[`d5437e9dc754f8d1b238d15e8e17c93b92878867`](https://github.com/shentschel/teddycloud/commit/d5437e9dc754f8d1b238d15e8e17c93b92878867)
selects patched 1.2.2 within PostCSS's existing compatible range. Next CI
[37422492722](https://github.com/shentschel/teddycloud/actions/runs/37422492722)
passed all four jobs, including advisory, deterministic build/smoke,
reproducibility and persistence/race evidence.

PI-07/T-01 #17 is accepted and may be closed. PI-07/T #24 and the PI milestone
remain open for the rest of the independent contract-to-code/resource/error
audit. This evidence does not claim hardware power-loss durability, real playback,
production deployment or coordinated media/credential restore.


## PI07-T-F03 inventory page-bound closeout (2026-10-08)

Finding PI07-T-F03 is fixed at
[`eaf20c91fe0cfb946c6412ab60e3895476e7a5db`](https://github.com/shentschel/teddycloud/commit/eaf20c91fe0cfb946c6412ab60e3895476e7a5db).
The public inventory page limit is now explicitly 128, independent of the
existing 1,024-entry inspection bound. Requests above 128 fail before changing
the active scan or cursor; the accepted 128-entry request continues to enforce
the 1,024 inspection and four-descriptor bounds.

Parent review confirmed the three-commit diff is limited to
`contentfs/{store.go,inventory.go,inventory_test.go}`. All four jobs in
[Next CI 37837578422](https://github.com/shentschel/teddycloud/actions/runs/37837578422)
passed, including focused SQLite repetition, persistence/domain race evidence,
qualified Linux filesystem repetition, non-Linux boundary compilation,
deterministic builds and advisory checks.

PI07-T-F03 alone is closed. PI-07/T and the PI milestone remain open for
PI07-T-F02, PI07-T-F04 and the remaining independent audit coverage. No
deployment, migration, deletion or hardware power-loss claim is made.

## PI07-T-F02 configured import deadline correction

The service now starts the immutable media import duration before owner
admission and carries that context through publication and the selected SQLite
session, including commit. The existing fixed outer operation timeout, short
transaction timeout, direct publication timeout, byte ceilings and retained
verification limits remain independent and unchanged. Earlier caller deadlines
win through normal context inheritance.

`TestImportConfiguredDeadlineThroughCommit` uses real qualified media storage
and SQLite with bounded test delays before SQL admission and after inserts
immediately before commit. It checks deadline propagation, rollback of all four
import tables, absent receipts, retained canonical orphans and exact retry
reusing the same file. Each delay is also tested with an earlier caller deadline.

Parent review found that cancellation during the pre-commit seam still allowed
a commit. Follow-up commit
[`8ffc75141ab6e7c6d6bbbba1dd997fd0d5c584af`](https://github.com/shentschel/teddycloud/commit/8ffc75141ab6e7c6d6bbbba1dd997fd0d5c584af)
adds the required final context check immediately before commit. The initial CI
run correctly caught the regression; all four jobs in
[Next CI 37858019336](https://github.com/shentschel/teddycloud/actions/runs/37858019336)
then passed. Architecture documentation CI 37857374082 also passed.

PI07-T-F02 alone is closed. The local process runner remains unavailable, so no
local verification is claimed. PI-07/T and the PI milestone remain open for
PI07-T-F04 and the remaining independent audit coverage.

## PI07-T-F04 intermediate directory trust correction

Code commit [`5e559c0e61f5dd8299082dc4a9ecc2f8287742f0`](https://github.com/shentschel/teddycloud/commit/5e559c0e61f5dd8299082dc4a9ecc2f8287742f0)
adds a read-only descriptor walk for all three canonical shard components.
Each open retains the existing openat2 confinement and applies checkDir's
owner, device, write-mode and casefold policy. The chain remains open until
operation completion and its trust and entry identities are rechecked before
range output and after delivery. Quarantine uses the same source walk and
checks its retained destination directory before moving and acknowledging.
Missing paths are never created by this walk.

The focused regressions cover 0777 at each shard level, 0700 controls,
quarantine source/destination rejection without moving bytes, trust changes
at the pre-output/pre-rename seams, directory swaps and missing components.
Inventory already applies checkDir while descending each directory through
pushInventory; its bounded iterator algorithm is unchanged.

The local runner failed to create a process with os error 2. No local test
result is claimed. [Next CI 37886853669](https://github.com/shentschel/teddycloud/actions/runs/37886853669)
is pending at this publication checkpoint; final CI results and independent
parent review are still required. PI-07/T and the milestone remain open.
The trusted-root boundary still excludes arbitrary hostile same-owner mutation;
descriptor checks do not establish hardware power-loss or deployment evidence.
