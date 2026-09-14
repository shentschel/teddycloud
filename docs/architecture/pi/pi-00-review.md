# PI-00 review

Status: complete; R, T and optional A are locally verified.

## R outcome

- [PI-01 charter](pi-01-architecture-charter.md) now distinguishes requirements,
  planning recommendations and evidence that still needs collection.
- Nine acceptance journeys cover current/copy, Set members, lookup conflicts,
  preferred originals, Custom Cards, media playlists, sync, extensions and recovery.
- Seven enhancement repositories have explicit primary owners and supporting
  core/SDK/worker boundaries.
- [Backlog](pi-01-backlog.md) defines PI-01 R/A/B/T outputs, dependencies, models
  and acceptance gates, plus five deferred findings with ownership.
- Source review confirmed crossed audio-ID/hash matching and identified legacy
  GET write-back as a shadow-import risk. The running installation was not changed.

## Decisions and limitations

Control-plane-first and semantic UI slots remain the planning direction.
Technology, schema cardinality, repository topology and hardware support are not
silently approved by this refinement. PI-01/B must record final ADR decisions.
The proposed performance thresholds require an explicit reference environment.

GitHub issue publication is blocked by disabled Issues (HTTP 410); local task IDs
are the executable fallback, tracked by F-05. Personal budget measurements are
local-only. No credentials, physical card IDs or live captures belong in this review.

## Verification

Local verification passed: seven PI documents, 29 relative links, balanced code
fences, all seven ownership rows, all nine journey IDs and eleven unique task
records. `git diff --check` passed. T then added the same reusable architecture
documentation check to CI and reran it successfully across 14 documents. No
runtime behavior changed, so hardware tests, deployment and production migration
are not applicable. The new remote workflow has not run on this branch yet, so no
green remote CI result is claimed.

## T outcome

- The proposed V3 example now has one canonical Set membership relation. Its
  real cardinality remains a PI-04/PI-13 evidence decision.
- UI filter extensions now contribute validated server query clauses and cannot
  replace paginated results or decide identity.
- PI work consistently uses R/A/B/T. PI-00 has no B and calls its optional
  delivery slice A.
- Calibration separates model, effort and task type. One sample is not described
  as a confidence bound, and delivery does not inherit R/T estimates.
- `scripts/check_architecture_docs.py` and a path-scoped workflow validate local
  links, fenced blocks and whitespace without publishing release artifacts.
- The six-hour automation contract records idempotent dispatch, `waiting_budget`
  resumption and tolerance for reset timestamp drift. The fixed cadence checks
  every five-hour reset by the following run without relying on self-rescheduling.

## A outcome

- [System context](../system-context.md) defines gateway, core, UI/SDK, event hub,
  workers, stores and external systems with explicit trust and failure flows.
- [Component ownership](../component-ownership.md) assigns one target writer for
  each durable mutation and maps the provisional legacy API surface.
- All seven enhancement repositories have a supported target integration and no
  independent path to the core database.
- Successful and interrupted Copy plus offline/interrupted connector scenarios
  preserve prior assignments, verified TAFs and idempotent recovery.
- The documents keep hardware, schema, deployment and dual-write choices open for
  evidence and ADR review instead of claiming they are already accepted.

Delivery uses the documentation branch `docs/pi-00-r-charter`. Mainline merge is
separate from this refinement: the inherited mainline workflows include container
publication, which is not part of the documentation result. The branch and local
resume state preserve the complete, reviewable change.

## Remaining work and next admission

R, T and A have local start/end observations. A supplies the first bounded Sol
delivery sample; foundational ADR acceptance remains in PI-01/B with Astra.
PI-01/R is the next dependency-safe candidate and requires a fresh admission
check with PI-01/T capacity reserved. F-05 remains partly open because GitHub
Issues are disabled, but the executable backlog and documentation checker remove
that dependency from ongoing work. The automation should wait for the next
verified capacity and must not repeat PI-00 work.
