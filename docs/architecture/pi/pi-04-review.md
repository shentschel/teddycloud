# PI-04 review

Status: R complete; A/B/T not started
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
| PI-04/A | not started | no domain implementation |
| PI-04/B | not started | no relationship/migration prototype |
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
