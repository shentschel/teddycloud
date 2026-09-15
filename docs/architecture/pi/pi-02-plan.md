# PI-02 execution plan

Status: R complete; A/B/T pending
Milestone: reproducible initial compatibility evidence

## Agent tasks and dependencies

| ID | Model | Depends on | Deliverable and acceptance |
| --- | --- | --- | --- |
| PI-02/R | `gpt-6-astra` | PI-01 baseline | Source-pinned contract inventory, effect risks, fixture schema and issue-ready A/B/T; complete |
| [PI-02/A](https://github.com/shentschel/teddycloud/issues/5) | `gpt-5.6-sol` | R | Enumerated router manifest and initial sanitized HTTP/protobuf/event fixture catalog; trace CF-01 through CF-05 and mark unknowns |
| [PI-02/B](https://github.com/shentschel/teddycloud/issues/6) | `gpt-5.6-sol` | A | Offline harness for fixture validation and initial adapter contracts; failures must detect swapped UID bytes, wrong wrappers, omitted side effects and malformed framing |
| [PI-02/T](https://github.com/shentschel/teddycloud/issues/7) | `gpt-5.6-sol` | B, or checkpoint | Normalize duplicate fixtures, audit provenance/secret absence and unsupported cases, review CI and reconcile inventory coverage |

Use the [contract inventory](pi-02-contract-inventory.md) as the acceptance input.
A owns catalog/manifest files, B owns harness files; no concurrent edits to the
same fixture. T remains reserved if A/B span quota resets.

## Bounded delivery order

First fixture slice: CT-01/02/08/10 (tag wrappers and write-back, assignment,
events and discovery). Include one protobuf framing case from CT-15/16 before
claiming that the initial corpus covers both HTTP and protobuf. Next enumerate
all remaining routes and link each to covered, deferred or unsupported status
with a reason and owner. Route coverage is distinct from behavior coverage.

A must inspect the pinned Web UI and installer for CF-04. Existing unit tests
in enhancement repos are input evidence; importing their assumptions blindly
does not establish server conformance. Use disposable data and synthetic
credentials. Capturing live device traffic is a later explicitly bounded step.

## Definition of done

- Fixtures identify provenance, revisions, state effects and expected assertions.
- Every router entry and all seven enhancements have a coverage disposition.
- Offline tests and path-scoped CI pass; a negative fixture proves each initial
  parser/contract check can fail for a meaningful mismatch.
- Hardware/TLS and external-provider gaps retain owners; no cutover claim.
- Public review records limitations; local state records actual usage.
- Commit/push to the documentation branch. Merge and deployment are separate.

Rollback is a revert of the sprint commits. No production data is changed.
