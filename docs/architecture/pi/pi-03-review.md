# PI-03 review

Status: A complete locally; B/T not started
Milestone: not accepted

## R outcome

R chose an additive `next/` monorepo workspace in this fork. The Go backend is a
single module/deployment unit; canonical contracts generate an independently
versioned TypeScript SDK; the React Web UI consumes that SDK. Backend and matching
Web assets form one future application release, while SDK compatibility remains
an explicit independent contract. Workers stay isolated and are not scaffolded.

PI-03 CI will produce only finite-retention test artifacts. It must not invoke or
modify the legacy C/Web release path, publish packages, deploy services, change a
schema or write production data. Concrete tools are pinned by A and audited by T.

R split delivery into issue-ready [PI-03/A, PI-03/B and PI-03/T](pi-03-plan.md),
all routed to `gpt-5.6-sol`, with ordered dependencies, exact deliverables,
verification, compatibility, rollback and security requirements.

## Delivery status

| Task | State | Evidence |
| --- | --- | --- |
| PI-03/R | complete | plan, budget and this review skeleton |
| PI-03/A | complete locally | additive scaffold, locks, generator, local checks and artifacts |
| PI-03/B | not started | no next-only CI or remote run evidence |
| PI-03/T | not started | no pin/reproducibility/security audit |

## A outcome

- `next/backend` is one Go module with a composition-root executable, separated
  application/domain packages and an import-boundary test.
- `next/contracts/openapi.json` is the canonical minimal OpenAPI 3.1 contract.
  Repository generator `1.0.0` derives the committed TypeScript client and embeds
  the source SHA-256 provenance in its output.
- A private pnpm workspace contains `@teddycloud-next/sdk` and a React/TypeScript
  Web package that imports only that SDK package for API access.
- Go `1.27.1`, Node `24.21.0`, pnpm `12.4.2`, TypeScript `7.0.2`, React `19.3.0`
  and Vite `8.3.0` are exact manifest/toolchain pins. The pnpm lockfile is
  committed; Go has no external module dependency and therefore no `go.sum`.
- `next/Makefile` supplies bootstrap, generate, build, test, lint, check and clean.
  Build, generated SDK, contract copy, Web assets and SDK tarball stay in ignored
  `next/dist`; the tarball exports compiled JavaScript plus declarations rather
  than TypeScript runtime sources. Package caches are separately ignored.
- No legacy source/build/release workflow, production path, schema or data changed.
  No package was published and no server, secret or live host was used.

## Verification

R verification is documentation-only. `python3 scripts/check_architecture_docs.py`
passed and reported 36 checked architecture documents; `git diff --check` also
passed. Product build, lint, test, artifact, browser, runtime, migration, hardware
and deployment checks have not run.

A verification used temporary checksum-validated Go/Node toolchains because this
host lacks usable Linux installations. Frozen bootstrap, deterministic generator
check, Go format/vet/tests, the domain boundary test, TypeScript type checks, SDK
typed-client smoke test, backend build, React/Vite build and SDK packing passed.
The built executable returned the expected lower-case health/build metadata.
Browser/runtime, remote CI, dependency advisory, reproducibility comparison,
migration, hardware and deployment checks remain outside A.

## Open evidence and acceptance decision

The following prevent milestone acceptance:

- No next-only CI run, browser smoke or artifact digest manifest exists; B owns
  those gates.
- No dependency advisory/license result or two-environment reproducibility
  comparison exists; B/T own those gates.
- A's SDK package name is a local workspace identity. Public registry ownership
  and publication remain deliberately undecided until a later release PI.

PI-03 remains incomplete until A, B and T satisfy the milestone evidence in the
plan. R changes only documentation; rollback is a documentation revert.
