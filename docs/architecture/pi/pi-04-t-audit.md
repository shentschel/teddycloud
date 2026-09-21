# PI-04/T domain and migration-safety audit

Status: implementation complete; final workspace and remote-CI evidence pending
Scope: additive `next/backend` prototype only

## Audit conclusion

The prototype keeps Tag, Content, ContentVersion, Product, Set, Assignment and
Observation identities separate. External identifiers are typed evidence, not
aggregate keys. Persistence, HTTP, provider calls and production migration stay
outside the domain packages. Legacy parallel arrays exist only in the bounded
migration input seam and can never create a playable ContentVersion.

Mutation-equivalent tests cover opaque-ID normalization, UID/rUID byte order,
exact audio/hash pairs and crossed pairs, immutable version conflicts, Set
position collisions, Assignment history conflicts, provenance mutation,
Custom preservation and deterministic lossless migration. T found one
same-ContentVersion-ID comparison seam: conflicting immutable facts were
reported as the same version. The comparison now returns an explicit conflict;
the index already rejected the same input. No unresolved high-severity or
prototype data-loss finding remains.

## Migration-risk disposition

| Risk | Current evidence | Later owner | Executable acceptance criterion |
| --- | --- | --- | --- |
| MR-01 crossed/unequal audio and hash arrays | migration fixtures retain raw arrays and return ambiguous/rejected outcomes | PI-13, PI-14 | A version is created only from an explicit pair; crossed, unequal and multiply plausible input is reported without first-match selection. |
| MR-02 tracks masquerading as Set members | explicit ordered SetMember relations and negative track tests | PI-13, PI-15 | Four independently identified member Contents survive projection with their own metadata; changing tracks cannot add, remove or reorder membership. |
| MR-03 duplicate Tag/UID/rUID records | strict codecs plus deterministic duplicate-tag migration outcomes | PI-06, PI-34 | Normalized physical collisions produce a reviewable conflict; migration never deletes either raw record or a protected assignment. |
| MR-04 audio-ID collisions and revisions | exact-pair multimap, crossed-pair and same-pair collision tests | PI-08, PI-13 | Audio ID alone returns ambiguity whenever more than one revision exists; exact-pair collisions preserve all candidates. |
| MR-05 missing TAF/source conflated with identity | independent availability state and missing-data fixture | PI-07, PI-08 | Catalog identity remains queryable while playback availability is false; no missing file causes catalog deletion. |
| MR-06 Original/Custom inferred from flags | explicit Classification plus flag-independence tests | PI-09 | Classification policy consumes reviewed evidence; no single `nocloud`, URI, model prefix or auth flag decides Original/Custom. |
| MR-07 MyTonies/NFC/TAF disagreement | provenance observations preserve dissent | PI-14, PI-15 | Conflicting source observations remain addressable and block acceptance until a reviewed resolution names its support. |
| MR-08 read endpoints mutate metadata | pure migration package has no I/O or adapter dependency | PI-34, PI-36 | Compatibility reads pass side-effect tests; every remaining mutation is an authenticated command with revision fencing. |
| MR-09 title/model dedupe removes Custom Cards | shared-metadata Original/Custom test preserves both identities | PI-09, PI-10 | Preferred-library projection is read-only and all protected Custom records and assignments survive rebuild and rollback. |
| MR-10 import order implies newest version | explicit order evidence and unknown/conflict tests | PI-08, PI-14 | Reordering imports does not change preferred revision; recency requires accepted sequence/revision or trusted source time. |

## Naming, ownership and cardinality

- Set owns ordered references to Content, not tracks, versions or Product
  identifiers. Repeated Content references are allowed; duplicate positions are
  rejected. Final nesting and cross-Set cardinality remain PI-13 decisions.
- Assignment owns an immutable revision and half-open effective interval while
  referencing one Tag and one ContentVersion. It does not own TAF bytes or the
  referenced aggregates.
- Observation owns source/revision/record provenance and an observed claim.
  Accepted Fact retains all dissent and identifies only reviewed support.
- ContentVersion owns one inseparable audio-ID/hash observation and optional
  explicit ordering evidence. Blob identity, product metadata and filesystem
  time are not version order.

No duplicate membership model, synthetic model number, title/model/audio-ID
uniqueness assumption, implicit latest-version rule, transport DTO or
persistence concern remains in the audited domain packages.

## Security and data-loss review

- Inputs are bounded before expensive parsing; malformed values are rejected
  without echoing physical identifiers.
- Sanitized fixtures contain generated identities only. No household UID,
  certificate, token, credential, production path or NFC authentication payload
  is present.
- Migration is in-memory and read-only, copies caller slices and preserves raw
  records and fixed source references for every per-record outcome.
- Ambiguous, review-required and rejected records publish no playable candidate.
  A Set is withheld if any referenced member is not accepted.
- No network, filesystem, subprocess, database, logging or secret boundary was
  introduced. Supply-chain exposure remains the pinned PI-03 toolchain only.

## Deferred evidence

This audit does not approve a V3 wire schema, persistent database migration,
live legacy adapter, cloud/NFC import, production deduplication or hardware
cutover. Those require their named later PIs and real fixtures. Gate A may accept
only the pure executable domain vocabulary and sanitized migration seam after
the full workspace, reproducibility, advisory, architecture and remote-CI
checks recorded in the PI-04 review pass.
