# PI-10 execution plan

Status: complete Library-query/pagination refinement for parent review; A/B/T
are specified, not delivered or admitted. Milestone **Large libraries return
stable pages quickly** remains pending. Request
`pi10-r-library-query-pagination-20261114`, basis
`b75799cf767b00c5717d7ad20fdd8f42f61266d6`.

The [contract](pi-10-library-query-contract.md) separates E1 code facts, D1-D8 new
normative decisions and future proof. The [budget](pi-10-budget.md) governs this
whole R including parent review/publication/CI/accounting; each later job receives
fresh admission. [Roadmap](../teddycloud-next-pi-roadmap.md) and
[model routing](../task-model-routing.md) remain unchanged.

## R scope and routing

Requested routing: `gpt-6-astra`, `high`, overriding the matrix's Sol PI-10/R
recommendation for this request because identity, integrity reuse, cursor ABA and
cross-owner visibility require architecture decisions. This records requested
routing, not independently verified model-host metadata. No model switch grants
new quota. Future A/B/T default to `gpt-6.1-sol`, high; if unavailable, record the
fallback and re-estimate the entire job before dispatch.

The exhaustive R write manifest is:

```text
docs/architecture/pi/pi-10-library-query-contract.md
docs/architecture/pi/pi-10-plan.md
docs/architecture/pi/pi-10-budget.md
docs/architecture/pi/README.md
```

No runtime, schema, workflow, issue, status, personal ledger or private usage
history change. Direct workspace changes, one local commit, no push. Parent owns
review and separately authorized publication and exact-head CI. No independent
review or remote CI result is inferred from this local refinement.

Additional source reads are limited to the contract's E1 dependency ledger and
verification dependencies: existing application query tests to select focused
regressions, `next/go.work` and `next/backend/go.mod` for the pinned runner, and
the existing architecture/workspace checkers for their actual paths/coverage.
After SQLite open failed, only the two existing database opening tests and their
Open implementation were additionally inspected to diagnose the runner failure.
The two catalog transaction/lifecycle files and database connection setup are
needed to make the future lifecycle manifest concrete; they remain unchanged.

## Sequencing and ownership

One complete A, then one complete B, then one complete T: no independent tiny
codec/schema/activation jobs. Index readiness, integrity validation, pagination,
connection lifetime and lifecycle revocation are one A acceptance boundary.
Search normalization, index keys, deterministic title order and ownership/current
filters are one B boundary. Profiling and any measured N+1 correction are one T
boundary. Identity ordering is required already in A for valid pagination; B adds
the requested user-selectable title ordering, not basic determinism retroactively.

Before dispatch record the accepted prerequisite SHA, exact manifest, runner,
required tests and independent integrated B5/BW upper estimates. A requires parent
acceptance of this R and reconfirmed inherited PI-08/09 compatibility at the then
current head; no requirement to reinterpret their historical pending labels.
B requires accepted A; T requires accepted B. No issue is created by this plan.
All manifests below are exhaustive and relative to `next/backend/`; paths marked
new are prospective, not links to existing files. No implicit generated file,
module, workflow, durable schema or documentation allowance. A missing necessary
path is a concrete scope finding: report and obtain a revised manifest before
editing, not after. The four R files are not automatically writable in A/B/T.

## A: complete indexed inventory and pagination

Owner: Library application/SQLite implementer; lifecycle changes reviewed by the
existing SQLite lifecycle owner. New files:

```text
internal/application/library/queries.go
internal/application/library/queries_test.go
internal/application/library/ports.go
internal/application/library/service.go
internal/application/library/service_test.go
internal/application/library/errors.go
internal/adapters/sqlite/library_index.go
internal/adapters/sqlite/library_repository.go
internal/adapters/sqlite/library_cursor.go
internal/adapters/sqlite/library_index_test.go
internal/adapters/sqlite/library_query_test.go
internal/adapters/sqlite/library_cursor_test.go
internal/adapters/sqlite/library_lifecycle_test.go
```

Existing files, only the stated responsibilities:

```text
internal/adapters/sqlite/lifecycle_owner.go
internal/adapters/sqlite/database.go
internal/adapters/sqlite/catalog_repository.go
```

`lifecycle_owner.go` owns volatile index lifetime, generation/read capability,
prepare/read ports and close/restore revocation; preserve existing gates.
`database.go` supplies explicit physical-connection incarnation, including actual
connector reconnection, without changing connection limits, migration policy,
backup or writer semantics. `catalog_repository.go` may expose/reuse bounded
complete aggregate reconstruction helpers for preparation; existing Query,
Find, integrity checks, ambiguity limits and encodings must remain behaviorally
identical. No page-path call to global Find, source write, new persistent schema,
Tag/Assignment service call or media/network capability.

Implement all D1/D4/D5/D6 invariants and identity-ordered all-version queries.
Keep complete typed query shape for B, but explicitly reject otherwise valid
OwnerContentID/current/title/search options as Unsupported in this intermediate
A capability. Do not ignore them, and do not claim full PI-10 query delivery.
A prepares every D1 summary; B adds its additional private index keys/partitions.
Refresh/build state, budgets, cancellation, failure cleanup and observable
IndexNotReady are part of A, not deferred performance work. Source state and
receipts compare equal before and after prepare, query and failed operations.

Required tests in the matching new application/SQLite test files:

| Test family | Required positive and negative evidence |
| --- | --- |
| `TestLibraryInventoryIdentity` | Independent equal-fingerprint Contents, multiple/superseded/unmanaged/catalog-only versions, zero-version Content exclusion, exact IDs/revisions and no assignment fallback; broken ownership/support/graph fails preparation even outside the eventual page. |
| `TestLibraryProjection` | Baseline versus accepted/unknown/conflicted fields, no scalar conflict winner, protected custom independent of provenance/classification, stored media only; full detail APIs/receipts unchanged. |
| `TestLibraryIndexPreparation` | Complete atomic publish, resumable bounded advance, empty inventory, exact/+1 item/Content/byte/page caps, unsupported/dirty schema, cancellation/failure/epoch change, no old+new memory double allowance, no read-triggered build. Prove limit reachability before exact/+1 claims. |
| `TestLibraryPagePartition` | Oracle equality for Limit 1/16/128 and actual byte-short pages, duplicate sort keys, final/empty pages, lookahead retained on continuation, oversized first item failure; zero/129/overflow and exact/+1 bytes. |
| `TestLibraryCursorBinding` | Golden codec/response bytes, canonical tokens, replay, query/Limit/scope/profile/db changes, forged/truncated/oversized/foreign PI-08/09 tokens, missing expected snapshot, future/different snapshot and missing last ID. |
| `TestLibraryEpochABA` | Local write and undo, external commit and undo, unmanaged Save, review/order/import/registration/supersession/source revocation/media changes; identical values/revisions never revive an old cursor. |
| `TestLibraryLifecycleFence` | Real close/reopen, helper-process restart with old token, current/pre-supported-schema fenced restore, connection discard/replacement, uncertain cleanup and late cancellation, escaped callback/read scope; no retained pointer identity shortcut. |
| `TestLibraryReadOnlyBounds` | Actual authorized service path, early unauthorized/invalid input has zero SQL, scoped callback once, no nesting/retry, Busy/deadline, bounded query statements/candidates, immutable source and no file/network access. |

Golden fixtures include the codec fields and canonical response accounting,
escaping and UTF-8, changed query digests and byte boundary with a nonempty Next.
Test the real SQLite owner, not only a fake repository. Use a real process test
only for the restart boundary it proves; no physical power-loss claim.

Rollback: disable the new Library reader, revoke tokens and discard its private
in-memory index; existing catalog reads remain. Source revert is safe because A
introduces no durable state or schema. Never delete/rewrite catalog data or use a
source downgrade against an unrelated newer inherited schema. If implementation
would require durable storage, stop and refine a separate migration/rollback
contract before touching it.

## B: complete sort, prefix search and ownership filters

Owner: Library query implementer, with catalog owner review of projected fields.
Depends on accepted A. Exact manifest:

```text
internal/application/library/queries.go
internal/application/library/queries_test.go
internal/application/library/service.go
internal/application/library/service_test.go
internal/application/library/search.go
internal/application/library/search_test.go
internal/adapters/sqlite/library_index.go
internal/adapters/sqlite/library_repository.go
internal/adapters/sqlite/library_query_test.go
internal/adapters/sqlite/library_search_test.go
internal/adapters/sqlite/library_cursor_test.go
internal/adapters/sqlite/library_lifecycle_test.go
internal/adapters/sqlite/library_index_test.go
```

Scope finding accepted for request `pi10-b-search-sort-ownership-20261116`: A-only
Unsupported expectations and private-index trace/capacity fixtures require the
three additional test files above to preserve cursor and capacity invariants in B.

`search.go`, `search_test.go` and `library_search_test.go` are new. Other files
extend the complete A path. Own D2/D3 features and D6 partitioned covering indices;
no catalog schema/semantic change, personal owner inference or post-page filtering.
Use the normalization profile from R without adding a Unicode/FTS dependency.
All admitted filter/sort combinations must use matching ordered ranges. Changed
index layout forces a fresh index generation; A cursors cannot survive rebuild.

Required tests: `TestLibrarySearchNormalization` (all D3 examples, controls,
invalid UTF-8, 256/257 bytes, whitespace-only, literal wildcards, Unicode combining
forms and non-ASCII preservation); `TestLibraryTotalOrder` (ties, missing-last
both directions, mixed comparator, insertion permutation, no rowid dependency);
`TestLibraryOwnershipVisibility` (exact present/absent/unknown owner, all/current,
unmanaged exclusion only from current, Unauthorized, unsupported mine/shared,
no custom/public ownership inference); `TestLibraryFilterPagination` (oracle
concatenation of every admitted shape and each Limit, equivalent normalization
replay, cross-filter/sort/owner stale rejection, search+identity Unsupported).

`TestLibrarySearchPlan` proves covering range/index use for prefix/owner/current
and both directions, no full temporary sort, constant SQL count and bounded
lookahead. `FuzzLibraryQueryAndCursor` runs a bounded 10-second corpus covering
parser/normalization/cursor determinism without storage/network capabilities.
Record B's matched-runner timing baseline for T; do not claim the final profiling
gate from a single EXPLAIN or warm-cache measurement. Rerun A invariants with B
options, including restart/restore/ABA and exact byte-boundary pages.

Rollback: remove B feature admission, invalidate tokens and rebuild the A-capable
volatile index. Preserve source facts, authority and old detail queries. No
persistent down migration, evidence cleanup or silent conversion of old tokens.

## T: profiling, N+1 removal and composed milestone

Owner: performance implementer; parent reviewer independently checks integrity
reuse and evidence. Depends on accepted B. Exact manifest:

```text
internal/adapters/sqlite/library_index.go
internal/adapters/sqlite/library_repository.go
internal/adapters/sqlite/library_profile_test.go
internal/adapters/sqlite/library_benchmark_test.go
internal/adapters/sqlite/library_audit_test.go
internal/application/library/service_test.go
```

The three profile/benchmark/audit files are new. Corrections are restricted to
measured index/query inefficiencies in the two implementation files. A defect
in codec, lifecycle, source validation or semantics returns to its A/B owner with
a finding and revised manifest before correction. No T allowance to weaken R,
raise limits/deadlines or modify CI tiers/workflows. Store reproducible fixture
construction, instrumentation and oracle checks in these test files; raw timing
output belongs in the handoff, not an unlisted artifact or private status file.

Required tests: `TestLibraryQueryCost` (D7 statements and measured visits across
all shapes/cardinalities/positions); `TestLibraryNoNPlusOne` (Limits 1/16/128,
multiple Contents and annotation counts, correlated scans included);
`TestLibraryProfileGate` (all D7 cold/warm/refresh metrics and scaling review);
`TestLibraryMilestone` (100k independent valid versions, deterministic oracle,
search/current/owner filters, byte-short page, mutation rejection, rebuild and
restart/restore); `TestLibraryCompatibilityAudit` (existing PI-08 lookup outcome,
PI-09 exact target/classification, PI-07 receipt and PI-06 metadata invariance).
`BenchmarkLibraryPages` emits comparable runner-labelled measurements and
allocation results; fixture preparation is timed separately, never hidden.

A/B cost tests must already prevent known N+1; T profiles the complete surface,
removes any observed residual and proves the final gate. If none exists, report
that evidence instead of manufacturing a refactor. Before/after corrections use
the same fixture, runner, cache protocol and successful sample count. Explain
any >2x p95 regression; structural/authority gates cannot be waived. T rollback
reverts only measured query/index changes, invalidates/rebuilds its volatile
index and preserves the B contract. No fallback to an unbounded query path.

## Common delivery gates and acceptance ownership

Each test family has the acceptance owner above. Later reruns are regressions,
not new duplicate scope or a second accounting entry for an earlier run. All
semantic/byte/cursor/lifecycle tests run uncached, focused tenfold and race as
appropriate to their state/concurrency boundary; the large profiling matrix runs
once as a measured workload, not tenfold or as a duplicate full race matrix.
A/B/T must select real existing named tests; a zero-match command is not evidence.

Each delivery job runs the existing architecture/workspace/Python gates, focused
new package tests, inherited affected catalog/assignment tests and the repository's
required exact-head CI unchanged. Preserve PI-08's accepted expensive accounting
tier exceptions, mandatory one-shots, normative deadlines and assertions. Before
running media regressions qualify the existing content-filesystem fixture; do
not reinterpret runner failures as product failures or passing tests.

Milestone acceptance requires composed A/B/T, parent review, green required CI at
the published exact head, structural D7 bounds and reproducible profiling with
no open High/security/data-loss finding. Other findings require explicit owner,
severity and reviewer disposition. Hardware, deployment, public API/V3 and
production migration remain excluded. R alone establishes none of these results.

## R verification and handoff

Required commands from repository root:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 next/tools/check_architecture_docs.py
PYTHONDONTWRITEBYTECODE=1 python3 scripts/check_architecture_docs.py
PYTHONDONTWRITEBYTECODE=1 python3 next/tools/check_workspace_boundaries.py
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s next/tests -p 'test_*.py' -v
git diff --check
```

The first requested path is absent at the exact basis. Run it and report the
failure honestly; the existing `scripts/check_architecture_docs.py` supplies the
actual architecture/link check. No copied wrapper or fifth file is authorized.
Check every local Markdown target and heading fragment in all four R files
separately; the existing checker checks file targets but skips fragment validity.
Future-file manifests in code blocks are plans, not broken current links.

Go source is not writable; no gofmt. Focused existing catalog query tests suffice
to validate the starting evidence; no full Go matrix for this docs-only R. Use
`next/go.work` toolchain `go1.27.1`; the available Windows Go executable is an
acceptable local runner, with limitations recorded separately. R is not a claim
that future Library test names above already exist or have passed.

Before the single commit, inspect all new documents, verify exactly the four
allowed changed paths and no pre-existing edits, and run staged diff checks.
After commit verify its parent equals the basis, its four-file manifest and clean
working tree. Handoff includes verdict, full SHA, file paths, decisions, command
results/durations, remaining boundaries and live budget/stop statement. Parent
publication, CI and delayed accounting remain explicitly pending until evidenced;
local technical readiness is distinct from integrated acceptance and accounting.

Local execution results are recorded below before committing; commit identity,
post-commit cleanliness and session-only usage readings are in the handoff.

### Local evidence on 2026-11-14

| Check | Result | Wall time |
| --- | --- | ---: |
| Requested `next/tools/check_architecture_docs.py` | Exit 2: file absent at basis; no fifth-file workaround | 0.028 s |
| Existing `scripts/check_architecture_docs.py` | Pass, 110 architecture documents and their relative file links | 2.944 s |
| `next/tools/check_workspace_boundaries.py` | Pass | 0.154 s |
| Python unittest discovery above | Pass, all 6 quality-gate tests | 0.358 s |
| Four-file local targets/fragments, basis, empty staging and diff check | Pass, 131 local links, 9 fragments and exact four-path set | 4.919 s |
| Focused application catalog tests below | Pass on Go 1.27.1 Windows/amd64 | 7.89 s |
| Focused SQLite catalog tests below | Runner blocked: all fail opening SQLite, not semantic assertions | 55.61 s |
| Two existing SQLite open diagnostics below | Confirm driver rejects Windows file URI with `invalid uri authority: C:` | 1.22 s |

Focused Go selections, run once from `next` with the available Go 1.27.1 runner:

```sh
go test -count=1 -run '^TestCatalog(Query|SupersessionAnnotations)' ./backend/internal/application/catalog
go test -count=1 -run '^TestCatalog(RevisionBoundCursor|QueryBounds|QueryReadOnlyTransaction|GlobalFindIdentitiesAndOutcomes|GlobalFindCursor|GlobalFindBoundsAndCorruption|GlobalFindEncodedBytes)$' ./backend/internal/adapters/sqlite
go test -count=1 -v -run '^(TestOpenCreatesAndReopensSchema|TestDriverEvidence)$' ./backend/internal/adapters/sqlite
```

The initial Windows runner launch was blocked by WSL socket restrictions; the
approved retry ran the pinned toolchain. Application tests passed (0.569 s package
time). SQLite tests reached the driver but failed with the existing Windows URI
path before creating the fixtures (0.152 s package time); diagnostic tests expose
`sqlite connection invariant not satisfied: acquire connection: SQL logic error:
invalid uri authority: C: (1)`. No Go file changed, no alternate implementation,
limit/timeout relaxation or full runtime matrix was attempted. Existing SQLite
cursor/integrity evidence was inspected but is **not locally revalidated** by
this run. A qualified Linux runner or parent CI must supply that check; it is not
an R implementation finding or proof of a passing existing runtime baseline.

The four-document refinement is locally reviewable with this explicit verification
limitation and the checker-path substitution. Parent review/required exact-head
CI determine integrated R acceptance. No remote publication or CI was attempted.
Final post-edit docs/link/scope/staged checks and clean committed head are reported
in the handoff; these earlier timings are not replaced with invented final-run
measurements.
