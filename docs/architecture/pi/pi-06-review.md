# PI-06 review

Status: R accepted; A1 identity/service, decision-ID and count-gate checkpoints implemented; remaining encoded aggregate and A2/B/T pending.
R milestone: design acceptance only. A/B/T and transactional Tags are not complete.
Reviewed source baseline: `2aedabbd141adb5aaf02746ae041eb7e5596317c`.

## Result and evidence basis

The [plan](pi-06-plan.md), [budget](pi-06-budget.md) and
[contract](pi-06-tag-registry-contract.md) refine PI06 into A1/A2 and B1/B2
checkpoints plus independent T review. Suggested A/B/T issue bodies include
files, dependencies, acceptance targets, commands and rollback/security scope.
The parent searched exact stable IDs and published A/B/T as
[PI-06/A #19](https://github.com/shentschel/teddycloud/issues/19),
[PI-06/B #20](https://github.com/shentschel/teddycloud/issues/20) and
[PI-06/T #21](https://github.com/shentschel/teddycloud/issues/21), without duplicates.

The refinement inspected the roadmap/routing, ADRs 0001–0003, component ownership,
PI04 plan/review, PI05 plan/review/audit and the actual PI04 identity/tag/evidence
types, migration and assignment seams. It also inspected the current catalog
ports and SQLite schema, repository transaction, lifecycle and backup code.

Source-grounded conclusions:

- UID/rUID codecs already enforce strict eight-byte identity and byte reversal.
  They permit all byte values and reject malformed text without logging inputs.
- TagID is already distinct from physical identifiers and Content identity.
  Evidence retains disagreements and rejects reused observation identities.
- Catalog ports currently expose a callback-scoped Content repository, not a
  generic multi-domain transaction. The proposed tag method has a distinct name
  and shares the existing lifecycle gate instead of creating a second owner.
- PI05 already provides serialized access, bounded contention, migration ledger,
  verified pre-upgrade backup and fenced exact-schema new-path restore. These
  are integration foundations, not unfinished PI05 deliverables.
- Legacy `hasCloudAuth` includes overlay semantics. It cannot be copied into a
  universal physical/auth classification rule.

## Assumptions, risks and explicit gates

| Item | Decision / remaining evidence |
| --- | --- |
| Registry scope | One physical UID per database; multi-household tenancy is not designed here. |
| Allocation | Internal caller supplies independently allocated TagID; no public allocator/API is promised. |
| Auth/ownership | Evidence metadata only, independent facts; no secret execution or authorization decision. |
| Conflicting evidence | Immutable observations plus explicit revision-fenced support selection; no automatic winner. |
| Bounded history | At most 4096 observations/decisions each, 16384 cumulative decision-support references and 8 MiB encoded aggregate per Tag. A1 verifies worst-case encoding and rejects overflow before copies; no truncation/GC. |
| Lifecycle extension | Must preserve Content port behavior, callback non-replay and exact-schema restore. |
| Uncertain commit | Readback/replay semantics need B2 fault evidence; error does not prove no effect. |
| FK snapshots | F-PERSIST-01 #18 must supply foreign-key validation evidence before B2 recovery acceptance. R does not implement it. |
| External blobs | PI-07/T-01 #17 remains deferred/unproven; Tag tests cannot substitute for missing-media tests. |
| Secrets, assignment and migration | PI12, PI09 and later production migration retain ownership. No provider or real-card fixtures. |

Existing issue references are inherited from the checked-in PI05 audit:
[F-PERSIST-01 (#18)](https://github.com/shentschel/teddycloud/issues/18) and
[PI-07/T-01 (#17)](https://github.com/shentschel/teddycloud/issues/17).
The parent rechecked both live issues: they remain open, neither duplicated
nor counted as completed.

## Verification and runnable handoff

The subagent changed only four PI06 Markdown files. The parent independently
read the final files, checked domain/SQLite seams, integrated README links and
published the bounded issue backlog. The initial per-decision bound allowed
an excessive cumulative support graph; review added a total-reference and
encoded-aggregate bound before acceptance.
No implementation, migration, production data or deployment was changed.

From the checkout root, the documentation checkpoint is:

```sh
python3 scripts/check_architecture_docs.py
git diff --check
git ls-files --others --exclude-standard docs/architecture/pi/pi-06-*.md
```

The architecture checker reads untracked Markdown too, checking local links,
fences and trailing whitespace. `git diff --check` alone does not inspect new
untracked files; validate each new file against `/dev/null` with
`git diff --no-index --check /dev/null <file>` before integration.
The delegated check passed all 55 architecture documents and new-file
whitespace checks. Parent baseline full backend tests/vet, compatibility fixture
validation and eight compatibility/reference-adapter tests passed. Parent
rechecks the integrated documentation and staged diff before publication. Planned A/B/T acceptance tests are not present or run
as part of R. Publication is subject to the path-scoped documentation CI; no new runtime
was changed, and the full Next build is not rerun solely for this R document.
No implemented Tag milestone is claimed.

Next step: admit A1 separately only when its uncertainty, parent integration,
remaining T reserve and final five-percent safety margin fit both live windows. R remains a documentation-only design result.

## A1 identity/service checkpoint

The [identity checkpoint](pi-06-a1-identity-checkpoint.md) adds immutable Tag
identity, strict normalization, replay/conflict registration and callback-scoped
point queries. Independent review rejected invalid/wrong-key storage records
and added actual hexadecimal case tests. The new application port has no dummy
metadata-mutation method and does not open SQLite itself.
Full A1 limits/decision-ID/worst-case evidence and A2 persistence remain open;
no transactional Tag milestone or production behavior is claimed.

## A1 internal decision-ID checkpoint

The [decision-ID checkpoint](pi-06-a1-decision-checkpoint.md) implements only
the fixed internal value codec and malformed/oversized/privacy tests. It does
not implement evidence decisions, aggregate limits or a public wire schema.
Full A1 remains open pending real retained-history/cumulative-support and
encoded-size preflight with worst-case/overflow evidence; A2/B/T remain pending.

## A1 count-only preflight checkpoint

The [history-count checkpoint](pi-06-a1-counts-checkpoint.md) adds allocation-free
checks over actual slices, including all retained cumulative support links and
the independent command observation count. Exact, one-over, native-int overflow
and zero-allocation tests pass. Independent parent review confirms bounded
iteration, no copies, no semantic scanning and generic errors.
The APIs are composable gates, not yet called by metadata mutations or storage
loading. Full retained-history/encoded aggregate preflight remains open.
32-bit compilation passed; execution was sandbox-blocked, not claimed passing.
Local race evidence is unavailable without a C compiler; remote CI owns it.

## A1 internal encoding refinement

The [encoding refinement](pi-06-a1-encoding-refinement.md) is accepted as design
only. Parent independently checked every positional length formula and boundary
fixture arithmetic: maximal escaped history is 10816539 bytes, with valid field
adjustments reaching exactly 8388608 and 8388609. Implementation tests remain
future evidence, not arithmetic alone. Tag-specific UTC year range 0000..9999 is
accepted without changing generic evidence. Introduced revision preserves active
support reconstruction; it does not introduce a timestamp winner.
Parent removed a new-reader requirement from current A1 scope: it has no actual
consumer. Complete domain encoding and persisted preflight still need separate
implementation admission. No runtime, migration, B1 transition or public format
was implemented or accepted as delivered here.
