# PI-01 execution plan

Status: R complete; A reused; B awaiting admission; T reserved
Milestone: implementation architecture baseline with explicit evidence gates
Decision owner: executing architecture agent within the authorized roadmap

## Scope and sequence

This milestone defines the control plane, Set identity boundaries and semantic
UI extension system together. It produces architecture documents; runtime code,
production changes and device certification belong to later milestones.

| Sprint | Model | Admission and output |
| --- | --- | --- |
| R | `gpt-6-astra` | Complete: this plan, budget method, review, reference environment and refined charter |
| A | `gpt-5.6-sol` | Reuse PI-00/A at `f3d6612`; context and ownership reviewed against R; do not repeat the slice |
| B | `gpt-6-astra` | Admit one bounded ADR slice after a fresh capacity check: sequencing, technology/topology and persistence |
| T | `gpt-5.6-sol` | Mandatory reserve: review vocabulary, failure flows, ADR evidence, links and deferred work after B |

The original dependency R-before-A is satisfied by revalidating the early A
artifacts against this refinement. B consumes that reviewed result. R has found
two issues for B/T: blob files and SQLite cannot share a single atomic commit,
and successful core assignment must be distinguished from legacy playback
projection. These are tracked as F-06 in the [backlog](pi-01-backlog.md).

## Priority and acceptance

1. Preserve playback and protected data: J-01, J-02, J-04, J-05, J-09 are
   non-negotiable correctness and migration gates.
2. Product differentiators: J-03 and J-08 require explicit Set identity and
   semantic slots in the architecture from the beginning.
3. Feature parity: J-06 and J-07 keep their acceptance rules and worker owners;
   implementation follows core import/assignment contracts.

All nine journeys remain required for feature-complete release. Priority changes
their implementation order, not acceptance scope. Their evidence owners remain
the sprint IDs in the [charter](pi-01-architecture-charter.md).

## Reference environment and evidence

See [reference environment](pi-01-reference-environment.md). Read-only host
inspection confirms a Debian 13 x86-64 LXC with one CPU and 2 GiB RAM, ext4 and an
active systemd TeddyCloud service. The existing live device generation, firmware
and reproducible browser baseline are unverified. Existing playback success is
user-reported, not new gateway test evidence.

Correctness targets are accepted design requirements. The 100 ms feedback and
300 ms data-update p95 targets remain engineering targets until PI-10/26 records
a pinned browser, fixture version and repeated cold/warm results. They cannot be
used to claim a passing release today. No architecture work waits for a device
unless it would make a new hardware support or cutover claim.

## Exit evidence and rollback

- R documents priorities, environment and explicit unknowns.
- B compares at least two options per ADR and names the acceptance authority,
  compatibility effects, secret owner and recovery gates.
- T verifies all nine journeys and seven enhancement mappings, records remote
  CI evidence or its absence, and resolves/assigns every finding.
- Run `python3 scripts/check_architecture_docs.py` and `git diff --check`.
- Commit and push the documentation branch; mainline merge remains separate.
- Documentation rollback is a revert of the relevant commits.

The milestone is incomplete until B and T pass. Hardware and migration tests are
later gates with explicit owners; none is reported as executed by this PI.
