# PI-03 execution plan

Status: R complete; A/B/T refined but not delivered
Milestone: empty TeddyCloud Next product builds reproducibly
Repository: `shentschel/teddycloud`
Recommended model for R/A/B/T: `gpt-5.6-sol`

## R decision: workspace and release topology

PI-03 adds an isolated `next/` workspace to this repository. It does not modify
the legacy C build, Web UI submodule, release ZIP or container workflows. The
existing product and seven enhancement repositories remain independently
operational until later migration PIs explicitly replace their contracts.

```text
next/
  backend/                 Go module and executable composition root
    cmd/teddycloud-next/
    internal/              domain, application and adapter packages
  contracts/               canonical versioned OpenAPI and event schemas
  sdk/typescript/           generated public TypeScript client package
  web/                     React/TypeScript application
  tests/                   cross-workspace contract/build smoke tests
  package.json             private JavaScript workspace root
  pnpm-workspace.yaml      web and SDK workspace membership
  pnpm-lock.yaml           one JavaScript dependency lock
  go.work                  local Go workspace boundary
  Makefile                 stable next-only developer/CI commands
  README.md                prerequisites, commands and artifact boundaries
```

The Go backend is one module and one deployment unit. Domain packages do not
import HTTP, persistence or generated browser code. `contracts/` is the canonical
API input; the TypeScript SDK is generated from it and checked for drift. The
Web UI consumes the SDK as a workspace dependency, never backend internals.
Worker implementations remain outside this scaffold until PI-31/32; future
workers communicate through versioned commands and do not join the core module.
The initial Go module path is
`github.com/shentschel/teddycloud/next/backend`; the private workspace package
names and SDK package name are recorded once in A and cannot imply that a public
registry namespace already exists.

The JavaScript workspace uses `pnpm` through Corepack with an exact
`packageManager` value and committed lockfile. Go records a language version and
exact toolchain directive. A selects the concrete supported versions and records
them in manifests; T verifies that CI, local commands and documentation use the
same pins. Floating `latest` selectors are forbidden in the `next/` path.

### Release units and versioning

| Unit | Source | Version contract | PI-03 output |
| --- | --- | --- | --- |
| Integrated application | backend binary, built Web assets, API schema and notices | one application version; backend serves only its matching Web assets | non-published deterministic build artifact |
| TypeScript SDK | `next/sdk/typescript` generated from `next/contracts` | independent SemVer package; API compatibility matrix records supported application range | packed but not published test artifact |
| Contracts | `next/contracts` | versioned with the application; breaking API changes require an explicit compatibility decision | validated schema copied into build evidence |
| Workers | future isolated repositories/processes | independent versions and negotiated command capabilities | absent from PI-03 |

One CI invocation builds all three workspace areas from a clean checkout and
uploads short-lived evidence only. No PI-03 workflow pushes commits, packages,
containers or releases, and no production install layout is changed. A later PI
may promote the integrated artifact and SDK package through separate release
jobs after signing, compatibility and rollback policy exists.

## Sequence and dependencies

```text
PI-03/R -> PI-03/A -> PI-03/B -> PI-03/T -> milestone review
```

A owns the initial workspace and lockfiles. B consumes A's commands and adds
verification without redefining topology. T may simplify or harden the scaffold,
but it must preserve the decided boundaries. Each task is admitted separately
under [PI-03 budget rules](pi-03-budget.md).

## PI-03/A — Scaffold backend, Web UI and SDK workspaces

Recommended model: `gpt-5.6-sol`

### Objective and non-goals

Create the smallest buildable `next/` workspace whose backend, generated SDK and
Web UI demonstrate the dependency direction decided above. Do not implement
Tonie behavior, persistence, authentication, legacy adapters, deployment or a
production schema.

### Deliverables

- Add the exact directory topology above, developer README and next-only command
  surface: `bootstrap`, `generate`, `build`, `test`, `lint`, `check` and `clean`.
- Add a Go module with a composition-root executable and architecture-boundary
  test; packages contain only placeholder health/build metadata behavior.
- Add a private pnpm root, React/TypeScript Web package and public TypeScript SDK
  package. Commit the lockfile and generated client output.
- Add the smallest valid OpenAPI contract needed to generate and compile one
  typed client call. Record generator/tool versions in locked manifests.
- Build into ignored `next/dist/`; never copy into legacy `contrib/`, `data/`,
  `install/` or an existing release ZIP.
- Document the chosen module/package names, exact tool prerequisites and local
  command contract in `next/README.md`.

### Acceptance and verification

From a clean checkout with the documented toolchains:

```text
make -C next bootstrap
make -C next generate
git diff --exit-code -- next/contracts next/sdk/typescript
make -C next build
make -C next test
```

The backend binary, Web static files, packed SDK and contract copy exist only in
`next/dist/`; the Web package imports the SDK package; no legacy tracked file is
generated. Repeating generate/build without source changes produces no tracked
diff. `python3 scripts/check_architecture_docs.py` and `git diff --check` pass.

### Compatibility, rollback and security

Legacy build/release behavior must be byte-for-byte outside this task's files.
Rollback is deletion/revert of the additive `next/` tree. Network access is
limited to dependency resolution during bootstrap; builds/tests run from locks.
No secret, live host, production path, privileged port or external provider is
used. Generated code includes provenance and license metadata where supported.

## PI-03/B — Add CI, linting, tests and dependency checks

Recommended model: `gpt-5.6-sol`

Depends on: accepted PI-03/A.

### Objective and non-goals

Make A reproducible and reviewable in CI. Do not publish artifacts or broaden
the product feature surface.

### Deliverables

- Add a path-scoped `next` CI workflow with least-privilege read permissions,
  cancellation concurrency, dependency caches keyed by lockfiles and explicit
  timeouts. It invokes the same `make -C next check` command used locally.
- Run Go formatting/vetting/tests, TypeScript formatting/lint/type/tests/build,
  OpenAPI validation, generated-SDK drift detection and a cross-workspace smoke
  test. Every configured negative gate has at least one test that can fail.
- Add one browser load smoke test using a pinned automation package and bundled
  Chromium revision at 1440 x 900 and device scale 1; this establishes a runner,
  not a UI performance result.
- Add locked dependency vulnerability/license checks for Go and JavaScript.
  Network-dependent advisory checks are a distinct CI step; deterministic build
  acceptance does not silently depend on advisory-service availability.
- Upload the integrated application layout, packed SDK, contract and a manifest
  of file digests as non-release artifacts with finite retention.
- Document the local/CI support matrix and how to reproduce every gate.
- Update `next/README.md` and the PI review with commands, CI evidence and known
  limitations.

### Acceptance and verification

```text
make -C next clean
make -C next check
make -C next artifact
git diff --exit-code
python3 scripts/check_architecture_docs.py
git diff --check
```

A pull-request run must pass from a clean checkout. Artifact manifest digests
must match extracted files, SDK generation must be clean, and the workflow must
hold no write or package-publish permission. CI evidence is linked in the review;
local success alone does not complete B.

### Compatibility, rollback and security

The workflow is scoped to `next/**` and its own workflow file, and it must not
invoke legacy release, Docker-publish or Web-commit workflows. Untrusted pull
requests receive no secrets and execute no privileged container. Dependency
findings are either fixed or assigned stable IDs; suppressions require owner,
reason and expiry. Rollback removes the new workflow/check configuration while A
remains locally buildable.

## PI-03/T — Pin tools and remove unsafe defaults

Recommended model: `gpt-5.6-sol`

Depends on: PI-03/A and B, or their explicitly accepted checkpoint.

### Objective and non-goals

Audit and harden only the new scaffold and its evidence path. Do not repair all
legacy workflows, publish a release or introduce application features.

### Deliverables

- Reconcile Go, Node, pnpm, SDK-generator, linter/test and browser/runtime pins
  across manifests, CI and documentation; remove floating versions.
- Pin third-party CI actions to immutable commit SHAs and document their release
  tags for readability. Pin any CI/container base image by digest.
- Remove install scripts that execute downloaded code implicitly, broad workflow
  permissions, write-capable pull-request jobs, mutable global tool installs,
  production-directory defaults and accidental publish commands.
- Verify clean rebuilds in two fresh work directories or equivalent isolated CI
  jobs and compare normalized artifact manifests. Record unavoidable nondeterminism
  rather than claiming byte identity.
- Review license notices, generated-source provenance, cache contents and artifact
  retention. Record deferred security/supply-chain work with stable IDs.
- Update `next/README.md` and this review with final pins, reproducibility result,
  accepted limitations and any deferred issue IDs.

### Acceptance and verification

```text
make -C next clean check artifact
python3 scripts/check_architecture_docs.py
git diff --check
```

CI is green at the reviewed commit; no floating selector or publish credential
exists in the `next` workflow/manifests; two isolated builds have matching file
sets and explain any differing bytes. The review links evidence and either
accepts the milestone or names its remaining blocker.

### Compatibility, rollback and security

Pins must remain buildable on the Debian 13 x86-64 reference profile. Upgrades
are explicit dependency changes, not automatic `latest` resolution. Rollback is
a revert to A/B lockfiles and workflow; no database, schema, production service
or deployed package exists to migrate.

## Milestone evidence

PI-03 is complete only when all of the following are recorded in
[the review](pi-03-review.md):

- R/A/B/T have delivered commits and linked CI evidence.
- A clean checkout can generate, build, test, lint and package all workspaces
  using documented pinned tools.
- Generated SDK drift is rejected and the Web UI consumes only the SDK contract.
- The non-published application/SDK artifacts and their digest manifest are
  reproducible to the documented level.
- Security checks find no unresolved high-severity issue; no production path,
  deployment, schema or legacy release behavior changed.
