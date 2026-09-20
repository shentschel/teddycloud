# PI-04 budget and admission

Status: R/A complete; B local checkpoint within revised estimate; T not admitted

PI-04 uses the measurement method from PI-00. Exact account usage, reset
identifiers, request IDs and agent IDs remain in ignored local automation state.
No reset credits, purchased credits or API billing are authorized.

## Routing and reserves

| Sprint | Model | Cost character |
| --- | --- | --- |
| R | `gpt-6-astra` | ambiguous identity and migration decisions |
| A | `gpt-5.6-sol` | bounded pure-Go implementation and tests |
| B | `gpt-6-astra` | Set/Assignment/provenance cardinality prototype |
| T | `gpt-6-astra` | cross-boundary and data-loss audit |

Each job receives an explicit cap and may stop at a reviewable checkpoint.
At least 5% of both live windows remains free. The full PI may not exceed 1.9
five-hour budgets, and sprint labels do not create quota. Before admitting A or
B, reserve a conservative Astra allowance for T plus parent integration and
remote-CI correction. A checkpoint that used less than its cap does not transfer
an automatic entitlement to the next sprint.

## Admission sequence

1. R may create only plan/budget/review artifacts and issue-ready scopes.
2. Publish A/B/T issues only after R validation and an exact-ID duplicate search.
3. Admit A after R is committed, CI is green and both windows are re-read.
4. Admit B only after A proves the identity value objects and explicit ambiguity
   contract. Split fixture work if the Astra estimate does not preserve T.
5. Admit T only with enough capacity for audit, integration, remote CI and the
   final 5% margin.

## Checkpoint requirements

Every stop records changed files, decisions, test results, unresolved risks and
the next idempotent action. An agent response, generated file or open workflow is
not completion. Hardware, MyTonies, NFC and production migration evidence are
outside PI-04 and must not be inferred from synthetic tests.

## R observation

The first Astra refinement dispatch hit its bounded checkpoint without producing
the three planned PI files because its file-creation patch failed. The
orchestrator recovered the documentation from the accepted roadmap, ADRs,
Set-metadata exploration and PI-02 evidence. This R remains incomplete until the
recovered artifacts passed local review, publication and documentation CI. The
failed dispatch is retained only in ignored local state and must not be
repeated.

## A observation

The bounded Sol dispatch produced the planned pure-Go value objects and tests
and stopped before its cap. Parent integration independently reran unit, fuzz,
architecture, artifact and reproducibility checks. No unused allowance is
transferred to B: B still requires a fresh reading of both live windows while
preserving the full T, integration and final-margin reserves. Exact account and
dispatch observations remain only in ignored local state.

## B observation

B was admitted after a verified five-hour reset using comparable Astra delivery
samples plus the integration variance observed in A. The agent stopped at its
explicit cap with a compiling checkpoint. Parent integration corrected one
cross-record acceptance seam, added its regression test and completed fuzz,
workspace, artifact and reproducibility validation within the revised
end-to-end B estimate. This actual is retained for recalibrating later Astra
relationship/migration work; it does not reduce the reserved T allowance.
