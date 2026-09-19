# TeddyCloud Next workspace

This additive workspace is the PI-03 scaffold for the future TeddyCloud control
plane. It does not replace or modify the legacy C server, Web UI submodule,
release ZIP, containers, production data or installed services.

## Pinned prerequisites

- Go `1.27.1` (`go1.27.1` toolchain)
- Node.js `24.21.0` LTS
- pnpm `12.4.2`, selected through the root `packageManager` field
- Python 3 for the repository-owned SDK generator `1.0.0`
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
make -C next check      # generation drift, lint, test and build
make -C next clean      # remove next/dist only
```

`bootstrap` is the only command expected to resolve JavaScript dependencies over
the network. Subsequent commands use the committed lockfile and installed cache.
The repository-owned generator reads `contracts/openapi.json`; it has no package
dependency and emits `sdk/typescript/src/generated.ts` with provenance.

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

PI-03/A does not publish packages, run a server, access a live host, use secrets,
change schemas/data or add a CI/release workflow.
