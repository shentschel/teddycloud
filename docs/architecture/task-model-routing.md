# TeddyCloud Next task model routing

Status: active planning policy

For unfinished work from PI-06 onward, route bounded delivery to `gpt-6.1-sol`
and reserve `gpt-6-astra` for the highest-risk decisions and independent audits.
PI-00 through PI-05 retain their historical routing; completed jobs keep their
actual recorded model. Routing is a recommendation, not proof of availability
or of lower Codex Plus quota use.

## Routing rules

Use `gpt-6.1-sol` by default for future work when available for:

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

Use `high` for bounded implementation with non-trivial invariants; start with
`medium` for routine changes and raise to `xhigh` only for demonstrated
cross-boundary complexity. Keep Astra for irreversible schema/protocol,
security, migration and independent data-loss review. If Sol 6.1 is unavailable
in the execution environment, record an explicit fallback and re-estimate the
job before dispatch. A model change never grants fresh five-hour or weekly
quota: recalibrate from complete integrated observations by model, effort and
work class, retaining uncertainty and mandatory reserves.

An issue may override its assigned model only when the reason is recorded before
execution. Escalate from Sol to Astra when hidden ambiguity, security impact or
cross-boundary design appears. Downgrade from Astra to Sol only after an accepted
ADR or refinement has reduced the task to bounded implementation.

## Task assignment matrix

`R` is refinement/exploration, `A` and `B` are the ordered delivery slices, and
`T` is technical debt/refactoring. This covers every task in the candidate PI
backlog. Entries for PI-00 through PI-05 are historical; rows PI-06 onward are
prospective recommendations. Accepted PI-06/R and A1 work retains its actual
model in the execution records.

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
| PI-06 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-07 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-08 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-09 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-10 | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` |
| PI-11 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-12 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-13 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-14 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-15 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-16 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-17 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-18 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` |
| PI-19 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-20 | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` |
| PI-21 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-22 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` |
| PI-23 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-24 | `gpt-6-astra` | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-25 | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` |
| PI-26 | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` |
| PI-27 | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` |
| PI-28 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` |
| PI-29 | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` |
| PI-30 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-31 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-32 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-33 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-34 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-35 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` |
| PI-36 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-37 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-38 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-39 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-40 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-41 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6.1-sol` |
| PI-42 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-43 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-44 | `gpt-6-astra` | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-45 | `gpt-6-astra` | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6-astra` |
| PI-46 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-47 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-48 | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` | `gpt-6.1-sol` |
| PI-49 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6-astra` | `gpt-6-astra` |
| PI-50 | `gpt-6-astra` | `gpt-6.1-sol` | `gpt-6.1-sol` | `gpt-6-astra` |

## Issue creation rule

Copy the prospective matrix value into each newly admitted sprint issue as
`Recommended model`; update existing open issues with stale recommendations.
Preserve completed work and its actual model in the historical record. Record
the actual model and reasoning effort in the private budget ledger, and keep
separate Sol 5.6, Sol 6.1 and Astra samples. Do not reuse old Sol quota estimates
as measured Sol 6.1 costs. See the [official Codex model guidance](https://learn.chatgpt.com/docs/models)
and [GPT-6.1 Sol model page](https://developers.openai.com/api/docs/models/gpt-6.1-sol).
