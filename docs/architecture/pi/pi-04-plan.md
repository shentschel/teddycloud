# PI-04 execution plan

Status: R complete; A/B/T not delivered
Milestone: domain/schema prototype accepted
Phase gate: Gate A — approved domain vocabulary and executable skeleton
Repository: `shentschel/teddycloud`

## R decision: identity is not metadata

PI-04 prototypes domain identities and relationships inside the additive
`next/backend` workspace. It does not create a database schema, migrate legacy
files, mutate production data or finalize the public V3 catalog contract planned
for PI-13.

Every aggregate receives an opaque internal identifier. External identifiers are
typed facts with provenance, not substitutes for aggregate identity:

| Concept | Internal identity | External facts and invariants |
| --- | --- | --- |
| Tag | `TagID` | UID is exactly eight bytes. rUID is the byte-reversed UID representation; both must round-trip or the input is rejected. Text casing, colons and byte order are codecs, not identity. |
| Content | `ContentID` | One playable editorial work. Title, series, episode, image and model may change and never define identity. |
| ContentVersion | `ContentVersionID` | Immutable playable revision belonging to one Content. Audio ID, hash, size and track layout are observations of a revision, not a globally unique primary key. |
| Product | `ProductID` | Commercial article and member model are separate optional external identifiers. A set article must not be copied into each member's model. |
| Set | `SetID` | A grouping aggregate with ordered SetMember relations to Content. A track list never establishes membership. |
| Assignment | `AssignmentID` plus revision | Time-bounded Tag-to-ContentVersion relation. Assignment does not own Tag, Content or TAF bytes. |
| Observation | `ObservationID` | Source, source revision, observed value, time, confidence and review state. Accepted facts retain their supporting observations. |

Opaque IDs are stable once allocated and are never derived from titles, paths,
models, audio IDs or hashes. Normalization occurs only at typed boundaries.
Conflicting external facts remain explicit conflicts; import order must not pick
the first candidate silently.

### Version matching and ordering

- Exact `audio ID + hash` is the strongest legacy fingerprint, with the pair
  kept inseparable. It is not assumed globally unique.
- Audio ID alone may produce a unique lookup result only when the current
  catalog index proves one candidate. Otherwise the result is ambiguous.
- Version recency is an explicit accepted sequence/revision or trusted source
  timestamp. Numeric audio-ID order, file modification time and import order are
  not recency.
- Two byte-identical TAF blobs may support one or multiple catalog revisions;
  the blob digest deduplicates storage, not editorial identity.
- ContentVersion is immutable. Correcting metadata creates a new accepted fact
  or superseding version record instead of rewriting historical evidence.

### Tag state and classification

Legacy booleans stay independent:

| Fact | Meaning | Must not imply |
| --- | --- | --- |
| `valid` | identifier/protocol validation succeeded | local file availability or ownership |
| `exists` | referenced local content currently exists | cloud authorization or original status |
| `claimed` | legacy claim workflow reports claimed | catalog match or physical ownership |
| `cloud_auth` | observed authorization capability, subject to overlay semantics | current reachability, metadata correctness or latest version |
| `nocloud` | assignment/runtime policy suppresses cloud use | Custom Card identity |
| Original/Custom | reviewed classification supported by physical/auth/catalog evidence | deletion, preferred-view selection or assignment |

Custom Cards remain protected records even when they share a title, model or
TAF with an Original. Library preference is a read policy and cannot delete
alternatives. `sourceInfo`, assigned model and catalog metadata remain separate
views until a reviewed command accepts a fact.

## Migration-risk register

| ID | Risk | Prototype response | Deferred owner |
| --- | --- | --- | --- |
| MR-01 | V1 parallel `audio_id[]`/`hash[]` arrays allow crossed pairs | importer prototype accepts explicit pairs and reports unequal/ambiguous arrays | PI-13/14 |
| MR-02 | Set members are encoded as tracks and inherit one article/model/image | SetMember is an ordered relation to independent Content; heuristics create review candidates only | PI-13/15 |
| MR-03 | Duplicate tags and UID/rUID text variants create multiple records | binary normalization plus collision result; no destructive merge | PI-06/34 |
| MR-04 | Audio IDs collide or appear in multiple revisions | exact-pair multimap and explicit ambiguous result | PI-08/13 |
| MR-05 | Missing TAF or source is treated as absence of the Tonie | represent blob/source availability independently from catalog and ownership facts | PI-07/08 |
| MR-06 | Original/Custom inference conflates `nocloud`, URI and model prefixes | preserve observations and require explicit classification policy | PI-09 |
| MR-07 | MyTonies, NFC dumps and local TAFs disagree | source-ranked observations; no silent repair or cloud call in prototype | PI-14/15 |
| MR-08 | Legacy GET endpoints write metadata while being read | prototype is pure; compatibility write-back stays behind later adapter tests | PI-34/36 |
| MR-09 | Title/model deduplication can remove owned Custom Cards | no title/model uniqueness constraint; preferred view is projection only | PI-09/10 |
| MR-10 | Import order masquerades as latest audio version | explicit version evidence and unresolved ordering state | PI-08/14 |

## Deferred versus required decisions

Required now for A/B:

- typed opaque IDs and strict UID/rUID codecs;
- aggregate boundaries and ownership;
- immutable ContentVersion and explicit Assignment revision;
- exact-pair/ambiguous lookup outcomes;
- ordered SetMember relation;
- provenance-bearing observations and accepted facts;
- lossless legacy import report types.

Deferred to PI-13:

- public V3 JSON field names and schema versioning;
- final Set cardinality, nested sets and cross-set membership constraints;
- public provenance vocabulary and confidence scale;
- catalog compatibility projection and deprecation period;
- global rules for merging catalog identities across providers.

Deferral must not introduce a second membership representation or permit a
synthetic model number. A/B use internal types that can express the unresolved
cardinalities without exposing a public schema.

## Sequence and issue-ready delivery

```text
PI-04/R -> PI-04/A -> PI-04/B -> PI-04/T -> Gate A review
```

### PI-04/A — Implement identity and version domain types

Recommended model: `gpt-5.6-sol`
GitHub issue: [#11](https://github.com/shentschel/teddycloud/issues/11)
Depends on: accepted PI-04/R.

Deliverables:

- Add dependency-free Go value objects for TagID, UID/rUID, ContentID,
  ContentVersionID, ProductID and typed external identifiers.
- Add Content and immutable ContentVersion prototypes with exact-pair and
  ambiguity result types; do not add persistence or HTTP.
- Add table/property-style tests for byte-order round trips, malformed inputs,
  collisions, non-unique audio IDs and explicit version ordering.
- Document constructors and error taxonomy; prevent transport strings and
  legacy booleans from leaking into identity equality.

Acceptance:

- `make -C next test`, architecture boundaries, `make -C next check`,
  architecture-doc checks and `git diff --check` pass.
- Crossed audio/hash pairs, duplicate audio IDs and UID/rUID mismatches fail or
  return typed ambiguity; no first-match fallback exists.
- Existing PI-03 artifacts remain reproducible and no legacy file changes.

Rollback/security:

- Additive pure domain code; rollback is a revert.
- Reject oversized/malformed text before allocation-heavy parsing; no secret,
  filesystem, network, production path or logging of physical identifiers.

### PI-04/B — Prototype Set, Assignment and provenance relationships

Recommended model: `gpt-6-astra`
GitHub issue: [#12](https://github.com/shentschel/teddycloud/issues/12)
Depends on: accepted PI-04/A.

Deliverables:

- Add Set and ordered SetMember prototypes referencing Content IDs.
- Add versioned Assignment with effective interval and explicit current/history
  rules; it references one Tag and one ContentVersion.
- Add Observation, evidence source/revision, confidence/review state and
  accepted-fact prototypes without defining the public PI-13 vocabulary.
- Add an in-memory, read-only migration prototype for sanitized legacy fixtures
  covering a normal Tonie, a Custom Card, duplicate tags, missing TAF/source,
  crossed arrays and a four-member Set.

Acceptance:

- Each pilot Set member has an independent Content identity; tracks are not
  members and model/article remain separate.
- The migration result is deterministic and lossless: accepted, ambiguous,
  rejected and review-required records carry reasons and source references.
- Custom Cards and conflicting observations survive; no destructive dedupe,
  filesystem write, cloud request or database schema exists.
- Unit/fixture tests, PI-03 checks, docs and remote CI pass.

Rollback/security:

- Prototype state is in memory and fixture-only; rollback is a revert.
- Sanitized fixtures contain no household UID, certificate, token or provider
  credential. Input limits and untrusted text handling are tested.

### PI-04/T — Normalize schema seams and audit migration safety

Recommended model: `gpt-6-astra`
GitHub issue: [#13](https://github.com/shentschel/teddycloud/issues/13)
Depends on: accepted A and B checkpoints.

Deliverables:

- Audit names, ownership and cardinalities against ADRs and PI-13 deferrals;
  remove duplicate concepts and transport/persistence leakage.
- Mutation-test identity, pair matching, Set membership, assignment history and
  provenance conflict paths.
- Produce a migration seam report mapping every MR item to a later owner and
  record stable issue IDs for unresolved security/data-loss findings.
- Re-run two-directory artifact reproducibility and supply-chain checks.

Acceptance:

- No title/model/audio-ID uniqueness assumption, parallel-array domain model,
  synthetic model or implicit latest-version rule remains.
- No unresolved high-severity or data-loss finding; deferrals have owners and
  executable acceptance criteria.
- Reviewed commit has green deterministic, browser, reproducibility, advisory
  and architecture CI.

## Milestone and Gate A evidence

PI-04 is accepted only when:

- R/A/B/T commits and issue links are recorded in the review;
- pure domain tests prove identity normalization, explicit ambiguity, immutable
  versions, independent state booleans and non-destructive Custom handling;
- sanitized migration fixtures demonstrate normal, collision, Set and missing
  data outcomes without writes;
- the prototype keeps persistence, HTTP, production data and final V3 schema out
  of scope;
- PI-01 vocabulary/ADRs, PI-02 compatibility evidence and the PI-03 clean-build
  contract still pass from a clean checkout;
- Gate A can cite approved vocabulary, sanitized reproducible evidence and the
  green executable skeleton.
