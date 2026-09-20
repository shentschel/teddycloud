# PI-04 review

Status: R/A/B complete; T not started
Milestone: not accepted
Phase gate: Gate A not accepted

## R outcome

R separates physical Tag identity, editorial Content, immutable ContentVersion,
commercial Product/model, Set membership, Assignment history and provenance
observations. Internal opaque IDs are distinct from external facts. UID/rUID are
two codecs over the same eight bytes; audio ID/hash is an inseparable observed
pair but not a guaranteed global identity.

Original/Custom classification, `valid`, `exists`, `claimed`,
`cloud_auth` and `nocloud` remain independent facts. Preferred-library
selection is read-only and cannot erase Custom Cards or conflicts. Version
recency requires explicit evidence and never follows numeric audio-ID, import
order or file time.

R records ten migration risks and divides delivery into:

- PI-04/A: pure identity and ContentVersion value objects with collision tests;
- PI-04/B: Set, Assignment, provenance and lossless fixture migration prototype;
- PI-04/T: normalization, mutation tests and data-loss/security audit.

Final public V3 field names, Set cardinality and provenance vocabulary remain
PI-13 decisions. PI-04 must keep enough internal expressiveness to avoid a
second membership model or synthetic Boxine model IDs.

## Delivery status

| Task | State | Evidence |
| --- | --- | --- |
| PI-04/R | complete | plan, budget, this review, issues #11-#13 and green docs CI |
| PI-04/A | complete | commit `a12d773`; green Next CI run 35510774321 and architecture CI run 35510774314 |
| PI-04/B | complete | commit `59abd77`; green Next CI run 35529019182 and architecture CI run 35529019417 |
| PI-04/T | not started | no final audit |

## Validation and open evidence

R passed architecture-document validation and a clean diff. Exact stable-ID
searches found no existing PI-04 tasks, and issues
[#11](https://github.com/shentschel/teddycloud/issues/11),
[#12](https://github.com/shentschel/teddycloud/issues/12) and
[#13](https://github.com/shentschel/teddycloud/issues/13) now carry the routed
delivery scopes. Commit `f22f2fc` passed
[architecture CI run 35494117118](https://github.com/shentschel/teddycloud/actions/runs/35494117118).

No code, database schema, production migration, hardware/cloud call or legacy
mutation belongs to R. The milestone and Gate A remain open until the executable
domain prototype, sanitized migration evidence, technical audit and all CI
evidence in the plan are complete.

## A checkpoint

PI-04/A adds opaque `TagID`, `ContentID`, `ContentVersionID` and `ProductID`
value objects; strict eight-byte UID/rUID codecs; typed model, article, audio ID
and hash observations; immutable Content/ContentVersion values; and a
deterministic in-memory revision index. Exact-pair and audio-ID lookups return
typed no-match, unique or ambiguous outcomes. Crossed pairs do not match,
duplicate candidates are deterministic, and explicit ordering evidence is the
only source of version order.

Unit tests, three bounded fuzz runs, the architecture boundary, the full
`next` check, artifact verification, two-directory reproducibility,
architecture-document validation and diff validation pass locally. The code is
pure additive domain work: no persistence, HTTP, filesystem, logging,
third-party dependency, production identifier or legacy-file mutation was
introduced. Commit `a12d773` passed
[Next CI run 35510774321](https://github.com/shentschel/teddycloud/actions/runs/35510774321)
and
[architecture CI run 35510774314](https://github.com/shentschel/teddycloud/actions/runs/35510774314).

## B checkpoint

PI-04/B adds ordered Set-to-Content relations, immutable effective-dated
Assignment history, provenance-bearing observations and accepted facts, and a
read-only in-memory legacy migration seam. Sanitized fixtures cover a normal
Original, a protected Custom Card, duplicate physical/logical tags, unavailable
TAF/source state, crossed and unequal audio/hash arrays, conflicting evidence
and a four-member Set with independent Content identities.

Migration outcomes retain raw records and source references while reporting
accepted, review-required, ambiguous or rejected reasons deterministically.
Tracks never create Set membership, the Set article is not copied into member
models, and a Set is not published when any referenced member fails acceptance.
No persistence, HTTP, filesystem, provider call, destructive dedupe, production
identifier or final PI-13 wire vocabulary was introduced.

Unit tests, four bounded fuzz runs, the architecture boundary, full `next`
checks, artifact verification, two-directory reproducibility, documentation
validation and diff validation pass locally. Commit `59abd77` passed
[Next CI run 35529019182](https://github.com/shentschel/teddycloud/actions/runs/35529019182)
and
[architecture CI run 35529019417](https://github.com/shentschel/teddycloud/actions/runs/35529019417).
