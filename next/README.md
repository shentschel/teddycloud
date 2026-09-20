# TeddyCloud Next workspace

This additive workspace is the PI-03 scaffold for the future TeddyCloud control
plane. It does not replace or modify the legacy C server, Web UI submodule,
release ZIP, containers, production data or installed services.

## Pinned prerequisites

- Go `1.27.1` (`go1.27.1` toolchain)
- Node.js `24.21.0` LTS
- pnpm `12.4.2`, selected through the root `packageManager` field with its
  registry SHA-512 integrity
- Corepack `0.36.0`, supplied by the pinned Node distribution
- Python `3.14.4` for repository-owned generators and quality gates
- Playwright `1.63.0` with its bundled Chromium build `v1243`
- SDK generator `1.0.0`
- `govulncheck v1.8.0`, including transitive modules locked in `tools/go.sum`
- GNU Make

The Go module is `github.com/shentschel/teddycloud/next/backend`. JavaScript
packages are private workspace package `@teddycloud-next/web` and SDK package
`@teddycloud-next/sdk`. These names reserve local dependency identities only;
they do not claim a public registry namespace.

## Commands

Run from the repository root:

```text
make -C next bootstrap  # verify tools and restore exactly from pnpm-lock.yaml
make -C next generate   # derive the TypeScript client from OpenAPI
make -C next build      # build backend, SDK, Web UI and SDK test package
make -C next test       # Go tests and typed SDK client smoke test
make -C next lint       # Go formatting/vet and TypeScript type checks
make -C next check      # deterministic contracts, boundaries, licenses and build
make -C next browser-install                 # install bundled Chromium locally
make -C next browser-smoke                   # 1440x900, device scale 1 load test
make -C next artifact artifact-check         # layout plus SHA-256 validation
make -C next reproducibility-check           # compare two fresh artifact builds
make -C next advisory                        # network Go/JS vulnerability queries
make -C next clean      # remove next/dist only
```

`bootstrap` is the only command expected to resolve JavaScript dependencies over
the network. Lifecycle scripts are disabled during restore. Subsequent commands
use the committed lockfile and installed cache. Corepack resolves only the exact
pnpm version and verifies the integrity stored in `packageManager`; no mutable
global pnpm installation is created.
The repository-owned generator reads `contracts/openapi.json`; it has no package
dependency and emits `sdk/typescript/src/generated.ts` with provenance.

`check` also runs five mutation tests proving that stale generation, an invalid
OpenAPI success response, direct Web API access, an unapproved license and a
changed artifact digest are rejected. `advisory` is intentionally separate: it
uses pinned `govulncheck v1.8.0` and the locked pnpm audit, but requires current
network advisory services.

## Boundaries and output

- `backend/` is one Go module. Domain code imports neither transports nor Web
  packages. Its executable is only a composition/build smoke scaffold.
- `contracts/` is the canonical API input.
- `sdk/typescript/` is generated from the contract and is the only application
  API imported by `web/`.
- The packed SDK is staged from compiled JavaScript and declarations under
  `next/dist/sdk`; its tarball never exposes TypeScript sources as runtime files.
- `web/` is a minimal React/TypeScript compile proof, not a production UI.
- All build/test/package output is under ignored `next/dist/`. Package caches are
  ignored separately. Nothing is copied into legacy or production paths.
- `artifact` stages `dist/artifact/application`, the compiled SDK tarball and a
  sorted `SHA256SUMS`; `artifact-check` rejects missing, added or changed files.

## Local and CI support

| Gate | Local reproduction | CI behavior |
| --- | --- | --- |
| Locked restore | `make -C next bootstrap` | required, pnpm/Go caches keyed by lock/module files |
| Deterministic acceptance | `make -C next clean check` | required, 25-minute timeout |
| Browser load | `make -C next browser-install browser-smoke` | required; CI installs Chromium host libraries |
| Artifact | `make -C next artifact artifact-check` | required; uploaded for 7 days as non-release evidence |
| Advisories | `make -C next advisory` | separate, visible non-blocking network job |

The browser check loads only built static files and verifies its viewport and
device scale. It is not a performance measurement. The path-scoped workflow has
read-only repository permission, cancellation concurrency and no publish,
deployment or secret-bearing step.

The workflow uses the fixed `ubuntu-24.04` runner label and immutable action
commit SHAs; adjacent comments retain the reviewed action release tags. There
is no container or base image in this workflow, so no image digest applies.
Caches contain only pnpm/Corepack downloads, Go modules and the Playwright
browser bundle. The non-release evidence artifact is retained for seven days.

`reproducibility-check` copies the tracked Next workspace into two fresh
temporary directories, restores locked dependencies, builds both artifacts and
compares their complete file sets and SHA-256 digests. The PI-03 reference run
produced six files with byte-identical digests. Generated SDK files retain the
generator version and source contract digest in their header.

PI-03 does not publish packages, access a live host, use secrets, change
schemas/data or invoke a legacy release workflow.
