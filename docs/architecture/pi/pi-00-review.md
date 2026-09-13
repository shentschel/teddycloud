# PI-00 review

Status: R refinement complete and locally verified. PI-00 remains incomplete.

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
records. `git diff --check` passed. No runtime behavior changed, so hardware tests,
deployment and production migration are not applicable. Existing CI has no
dedicated documentation link checker; no green CI result is claimed.

Delivery uses the documentation branch `docs/pi-00-r-charter`. Mainline merge is
separate from this refinement: the inherited mainline workflows include container
publication, which is not part of the documentation result. The branch and local
resume state preserve the complete, reviewable change.

## Remaining work and next admission

PI-00/T must execute with `gpt-5.6-sol`, review the documented inconsistencies,
and collect its own local consumption sample. Optional D is not admitted. R alone
does not calibrate Sol or justify a fixed PI-01 sprint count. PI-00 remains
`in_progress`; a completed refinement must not be restarted on resumption.
