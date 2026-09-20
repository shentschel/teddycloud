# PI-03 review

Status: B complete; T implementation checkpoint under verification
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
| PI-03/B | complete | local gates plus remote CI and browser evidence |
| PI-03/T | checkpoint | hardening and reproducibility implementation; remote CI pending |

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

## B outcome

- New path-scoped `.github/workflows/next-ci.yml` has read-only contents
  permission, cancellation concurrency, explicit job timeouts, lock-keyed pnpm,
  Go and browser caches, and no publish/deploy/write/secret step.
- The required job restores locks and invokes the same `next/Makefile` check,
  browser and artifact commands documented for local use. Its non-release
  artifact retention is seven days.
- Deterministic checks cover Go format/vet/tests, TypeScript types/tests/build,
  OpenAPI structure, SDK drift, Web-to-SDK boundaries, dependency licenses and
  five mutation proofs for custom negative gates.
- Playwright `1.63.0` pins bundled Chromium `v1243`, viewport 1440 x 900 and
  device scale 1. The test is a static load check and makes no performance claim.
- `artifact` stages the backend/Web/contract application layout and compiled SDK
  tarball under `next/dist/artifact`; `artifact-check` verifies its complete
  sorted SHA-256 manifest.
- Go `govulncheck v1.8.0` and locked pnpm audit run in a separate visible,
  non-blocking network job. Local execution reported no known vulnerabilities.
  The deterministic pnpm license allowlist passed.

## B verification

`make -C next clean check` passed, including five negative-gate tests, one Go
architecture test and one typed SDK client test. `make -C next artifact
artifact-check` passed for five payload files plus `SHA256SUMS`. The bundled
Chromium and FFmpeg downloaded successfully, but local browser launch stopped
before page creation because this host lacks `libnspr4.so`. The CI workflow uses
Playwright's `--with-deps` installation on Ubuntu and must supply the required
browser evidence after publication.

Remote [Next CI run 35478551044](https://github.com/shentschel/teddycloud/actions/runs/35478551044)
completed successfully. Its required job passed locked restore, Chromium host
library installation, deterministic workspace gates, the 1440 x 900 browser
smoke, artifact creation/validation and seven-day artifact upload. The separate
network advisory job also passed. Artifact `10595366382` contains the integrated
layout and manifest for commit `91d97f9` and expires after seven days.

## Open evidence and acceptance decision

The following prevent milestone acceptance:

- The two-environment reproducibility comparison and final pin/security audit
  remain T scope.
- A's SDK package name is a local workspace identity. Public registry ownership
  and publication remain deliberately undecided until a later release PI.

PI-03 remains incomplete until A, B and T satisfy the milestone evidence in the
plan. R changes only documentation; rollback is a documentation revert.

## T checkpoint

- Go `1.27.1`, Node `24.21.0`, pnpm `12.4.2` with registry SHA-512,
  Corepack `0.36.0`, Python `3.14.4`, Playwright `1.63.0`/Chromium
  `v1243`, generator `1.0.0` and `govulncheck v1.8.0` are reconciled
  across manifests, commands, CI and documentation.
- All third-party actions use immutable commit SHAs with reviewed release tags
  in comments. CI uses the fixed `ubuntu-24.04` label. No container or base
  image is used, so an image digest is not applicable.
- Restore disables package lifecycle scripts. Corepack verifies the exact pnpm
  package integrity without a mutable global install. The Go advisory tool and
  transitive modules are locked by `next/tools/go.mod` and `go.sum`.
- Workflow permissions remain read-only. No secret, publish, deployment,
  production-directory or write-capable pull-request path was added.
- A local reference run passed deterministic checks, six negative quality
  tests, artifact validation and two fresh-directory builds. Both artifacts
  contained the same six files with byte-identical SHA-256 digests.
- Generated SDK provenance remains embedded. Dependency licenses are checked,
  caches contain only dependency/browser downloads and evidence retention
  remains seven days.

Final acceptance still requires the reviewed commit to pass the deterministic,
browser, reproducibility and advisory jobs in remote CI. No deferred
high-severity security or supply-chain finding is known at this checkpoint.
