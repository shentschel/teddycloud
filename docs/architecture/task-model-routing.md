# TeddyCloud Next task model routing

Status: active planning policy

This policy assigns every candidate roadmap task to either `gpt-5.6-sol` or
`gpt-6-astra`. It is a recommendation for execution and cost control, not a
guarantee that either model is available through every execution environment.

## Routing rules

Use `gpt-5.6-sol` by default for:

- bounded implementation with settled acceptance criteria;
- tests, fixtures, adapters, UI work and documentation;
- mechanical migrations and refactoring with strong regression coverage;
- performance measurement and routine CI/release work.

Use `gpt-6-astra` for:

- architecture and irreversible domain/schema decisions;
- ambiguous Tonie identity, Set membership and conflict policy;
- protocol reverse engineering, TLS, certificates and hardware cutover;
- authentication, authorization, sandboxing and security review;
- production migration, dual-write conflict policy and rollback gates;
- cross-system reviews where a wrong decision can cause data loss or lock-in.

An issue may override its assigned model only when the reason is recorded before
execution. Escalate from Sol to Astra when hidden ambiguity, security impact or
cross-boundary design appears. Downgrade from Astra to Sol only after an accepted
ADR or refinement has reduced the task to bounded implementation.

## Task assignment matrix

`R` is refinement/exploration, `A` and `B` are the ordered delivery slices, and
`T` is technical debt/refactoring. This covers every task in the candidate PI
backlog.

PI-00 is a calibration PI with required R and T plus optional A. It has no B
slice; optional A is admitted only after both required measurements.

| Candidate PI | R | A | B | T |
| --- | --- | --- | --- | --- |
| PI-00 | `gpt-6-astra` | `gpt-5.6-sol` | n/a | `gpt-5.6-sol` |
| PI-01 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-6-astra` | `gpt-5.6-sol` |
| PI-02 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` |
| PI-03 | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` |
| PI-04 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-05 | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` |
| PI-06 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-07 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-08 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-09 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-10 | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` |
| PI-11 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-12 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-13 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-14 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-15 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-16 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-17 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-18 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` |
| PI-19 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-20 | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` |
| PI-21 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-22 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` |
| PI-23 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-24 | `gpt-6-astra` | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-25 | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` |
| PI-26 | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` |
| PI-27 | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` |
| PI-28 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` |
| PI-29 | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` |
| PI-30 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-31 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-32 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-33 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-34 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-35 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` |
| PI-36 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-37 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-38 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-39 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-40 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-41 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-5.6-sol` |
| PI-42 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-43 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-44 | `gpt-6-astra` | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-45 | `gpt-6-astra` | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-6-astra` |
| PI-46 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-47 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-48 | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` | `gpt-5.6-sol` |
| PI-49 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-50 | `gpt-6-astra` | `gpt-5.6-sol` | `gpt-5.6-sol` | `gpt-6-astra` |

## Issue creation rule

Copy the matrix value into every sprint issue as `Recommended model`. The agent
must preserve the exact model ID. Record the actual model and reasoning effort in
the budget record so PI-00 can maintain separate consumption estimates for Sol
and Astra.
