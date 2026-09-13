# PI-00 review

Status: complete for initial R/T calibration; optional A was not admitted.

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
- The hourly automation contract now records idempotent dispatch, `waiting_budget`
  resumption and tolerance for reset timestamp drift.

Delivery uses the documentation branch `docs/pi-00-r-charter`. Mainline merge is
separate from this refinement: the inherited mainline workflows include container
publication, which is not part of the documentation result. The branch and local
resume state preserve the complete, reviewable change.

## Remaining work and next admission

Both R and T have local start/end observations. Optional A was not admitted
because the available foundational ADR work is routed to Astra and would not be
a representative Sol delivery sample. PI-01 starts with R and reserves T; each
delivery slice requires fresh admission. F-05 remains partly open because GitHub
Issues are disabled, but the executable backlog and documentation checker remove
that dependency from ongoing work. The automation should wait for the next
verified weekly window and must not restart PI-00.
