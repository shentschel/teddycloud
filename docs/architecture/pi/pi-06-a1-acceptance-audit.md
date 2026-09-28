# PI-06/A1 acceptance audit

Status at audited commit: two focused test-evidence gaps; no production-code
change recommended. The later test follow-up is recorded below.
Audited commit: `2c021a0324d81da99766353aa351691785047f0d`.

This audit applies the A1 acceptance text in the
[plan](pi-06-plan.md) and [Tag Registry contract](pi-06-tag-registry-contract.md)
to the current domain and application implementation. Public metadata commands
are B1 work. SQLite storage, load preflight and lifecycle integration are A2;
their absence is not counted as an A1 failure.

## Acceptance matrix

| Criterion | Production and test evidence | Result |
| --- | --- | --- |
| Explicit UID/rUID presence; strict malformed/empty/mismatch rejection; lowercase input renders uppercase; rUID reverses bytes; zero bytes remain valid | `application/tagregistry/service.go:175` (`normalizePhysicalIdentity`) and `domain/tag/uid.go:35,56,71`; `TestTagRegistrationNormalization`, `TestTagRegistrationNormalizationHexCase`, `TestUIDRUIDCanonicalRoundTrip`, `TestPhysicalIdentifierRejectsMalformedAndOversizedInput`, `TestUIDPairRejectsMismatchWithoutLeakingIdentifiers` | **Pass** |
| Immutable independent TagID/UID identity; revision 1; all four metadata facts unknown | `domain/tag/registry.go:47` (`NewTag`) and private retained-value ownership at `domain/tag/retained_value.go:16`; `TestNewTagStartsWithImmutableIdentityAndUnknownMetadata`, `TestTagRetainedConstructorOwnsCallerSlices` | **Pass** |
| Exact registration replay is a no-op; same UID/different ID and same ID/different UID never merge or overwrite | `application/tagregistry/service.go:36` compares both indexes and complete `Tag.Equal` history; `TestTagRegistrationReplayAndCollisions`, `TestTagEqualUsesCanonicalRetainedHistory`, `TestTagRegistrationRejectsCorruptLookupWithoutWrite` | **Pass** for the bounded in-memory A1 service; durable uniqueness remains A2 |
| Complete retained representation validates references, revisions, accepted support and four independent projections before taking canonical copies | `domain/tag/semantic.go:14`, `domain/tag/retained_value.go:16`, and `domain/tag/registry.go:84`; `TestValidateRetainedSemanticsCompleteAndRegistration`, `TestValidateRetainedSemanticsRejectsInvalidSupportReferences`, `TestValidateRetainedSemanticsRejectsWrongActiveProjection`, `TestRetainedValueOwnsCanonicalSliceCopies` | **Pass** |
| Exact/one-over structural limits: 4096 observations, 4096 decisions, 4096 links per decision, 16384 cumulative decision-support links, 64 observations per command; repeated links count; checked arithmetic and count gates allocate nothing | `domain/tag/history_limits.go` and `domain/tag/retained.go:59`; `TestRetainedHistoryCountLimits`, `TestNewObservationCountLimit`, `TestCheckedAddWithinIsOverflowSafeOnNativeInt`, `TestRetainedViewCountsUseActualSlices`, `TestCheckedAddInt64Boundaries`, allocation tests | **Pass** |
| Complete TREG/1 byte accounting includes identity, four facts, observations, decisions, active/support history and source escaping; exact 8388608 bytes accepted by counter/writer and 8388609 rejected before output; individually valid maximal fields exceed the aggregate cap | `domain/tag/encoding.go:64,77`; `TestTagEncodingCompleteRepresentation`, `TestTagEncodingSourceEscapes`, `TestTagEncodingExactBoundary`, `TestTagEncodingPreflightAllocationCheckpoint`, `TestNewRetainedValueRejectsByteOverBeforeAllocation` | **Pass** for counting, writing and one-over pre-copy rejection; **open** because the semantically validating `newRetainedValue` path is not directly tested accepting the exact 8 MiB history |
| Positive signed-64 revision handling and overflow-safe decision increment | `domain/tag/encoding.go` rejects invalid revisions and `expectedRevision == MaxInt64`; `TestTagEncodingTimestampRevisionAndClosedMappings` covers encoding MaxInt64 | **Open evidence**: no direct test proves a MaxInt64 decision expectation is rejected before `expected+1` can wrap |
| Fixed internal DecisionID codec: exactly `dec_` plus 26 PI-04 opaque-alphabet characters; zero value distinct; malformed, uppercase, ambiguous, control, non-ASCII and oversized input rejected | `domain/tag/decision_id.go:18`; all tests in `decision_id_test.go`, notably `TestDecisionIDCanonicalRoundTrip`, `TestDecisionIDZeroValueDiffersFromAllZeroPayload`, and rejection tables | **Pass** |
| Domain/application dependency boundaries remain storage-neutral | Callback-scoped ports at `application/tagregistry/ports.go:13,21`; `TestDomainDoesNotImportAdaptersOrTransports`, `TestApplicationDoesNotImportPersistenceImplementations` | **Pass** |

## Verification performed

Using the required shared Go toolchain and caches:

```text
go test ./backend/internal/domain/tag ./backend/internal/application/tagregistry ./backend/internal/architecture
ok  .../internal/domain/tag
ok  .../internal/application/tagregistry
ok  .../internal/architecture
```

This is local focused evidence only. No CI, race, 32-bit execution, SQLite or
metadata-command result is claimed.

## Remaining A1 evidence and recommendation

A1 should not close yet. Add narrowly scoped domain tests that:

1. construct the semantically valid exact-8388608-byte boundary through
   `newRetainedValue` and confirm its complete write remains exactly 8388608;
2. exercise a decision with `expectedRevision == MaxInt64` and confirm generic
   rejection before increment/copy.

No implementation defect was demonstrated, so production changes are not
justified. B1 still owns observation/decision mutation, replay and compare-and-
swap commands. A2 still owns SQLite schema, durable uniqueness, persisted-load
preflight, joined-row avoidance and lifecycle fencing.

## Test-evidence follow-up

`TestRetainedConstructorsAcceptExactEncodedBoundary` now passes the exact
8 MiB view through both semantic constructors and verifies complete writes.
`TestNewRetainedValueRejectsMaxExpectedRevisionBeforeIncrement` directly covers
overflow rejection; the one-over constructor rejection is also asserted.
Local full backend tests/vet and 10 focused repetitions passed. [Next CI](https://github.com/shentschel/teddycloud/actions/runs/36395318633)
passed all four jobs; [docs CI](https://github.com/shentschel/teddycloud/actions/runs/36395318632)
passed. The two evidence gaps at the audited commit are closed; A1 is accepted.
B1 metadata transitions and A2 durable identity remain future work.
