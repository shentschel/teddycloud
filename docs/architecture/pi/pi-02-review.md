# PI-02 review

Status: R and A delivered; milestone incomplete pending B/T

R inventories the source router families and seven enhancement seams at pinned
local revisions. It records confirmed GET write-back, different auth flag
semantics, source metadata precedence, form assignment/readback and a bare-array
plugin discovery response.

The removal behavior needs a correction to the earlier conversation assumption:
in this source snapshot `tbs_tag_removed` sends MQTT only. Existing browser code
also recognizes an RTNL removal signal. A negative-UID SSE event is a supported
client input, not evidence that this server emits it. CF-02 owns the fixtures.

The [router manifest](../../../tests/fixtures/compatibility/router-manifest.tsv)
enumerates all 69 entries in `src/server.c` order, with method, prefix,
server type and handler. Six
[synthetic fixture cases](../../../tests/fixtures/compatibility/ct-01-02-08-10-catalog.json)
cover tag wrappers/write-back, form assignment/readback, placement versus
removal and bare-array discovery. JSON parses; entries are source-derived and
have not been replayed. A separate
[deployment-seam note](pi-02-deployment-seams.md) inventories pinned installer
and plugin paths. No production API, cloud content request or device operation
was performed.

A second checkpoint adds a [synthetic CT-15 protobuf fixture](../../../tests/fixtures/compatibility/ct-15-freshness-protobuf.json)
for the legacy freshness endpoint: the HTTP body has no TAF-length prefix,
fixed-width UID/audio fields use protobuf little-endian encoding, and response
byte length is explicitly checked. It documents setting/MQTT effects and
untested cloud forwarding. The 69-route manifest now records why each route
is cataloged or deferred. Neither the synthetic response bytes nor numeric
HTTP status were captured from a running server.

The first agent-produced fixture contained invalid JSON and a nested
`sourceInfo.tonieInfo` shape. Integration corrected both against
`getTagInfoJson`: `sourceInfo` is detached `tonieInfo`, so `model` is direct
under `sourceInfo`. This is a fixture correction, not a production change.

The source fixtures and Web UI inspection do not establish runtime
conformance.

The [pinned Web UI seam](pi-02-webui-seam.md) now establishes from source that
the UI requests `/api/plugins/getPlugins`, then per-folder manifests, and uses
same-origin `/plugins/{id}/index.html` iframes. The server's registered
`/api/plugins/get` prefix implicitly matches that longer UI request. Library
uses `fileIndexV2` special roots. Direct build, disposable-install and browser
checks remain open; the exact Web UI gitlink is not initialized locally.
CF-01…05 have source-level dispositions, not closure of runtime behavior.

Complete handler/status tracing, catalog reconciliation and harness tests
remain A/B work. Do not treat this as a PI-02 milestone or deployable UI.

A further [status/framing matrix](pi-02-status-framing.md) establishes the
router's source-selected 404 paths and explains why an `ERROR_FAILURE` does
not prove HTTP 500. Two [negative source-derived cases](../../../tests/fixtures/compatibility/ct-negative-status.json)
cover absent plugin directory and truncated protobuf without asserting wire
status. The [seven enhancement seams](pi-02-enhancement-coverage.md) now each
have a cataloged/deferred disposition. No response bytes were recorded from a
running server. Offline parser/harness and disposable installation remain B/T
work; A completion needs a final coverage and provenance audit.

The [bounded A audit](pi-02-a-acceptance-audit.md) did not pass A completion:
cataloged route rows lack meaningful reasons/revision, positive binary framing
lacks digest/version, and CF-01/03 state/negative/collision coverage is still
schematic. Static fallback and alias scope also need a distinct reconciliation.
These are A-owned source artifacts; executable negative proof remains B-owned.

Follow-up source-artifact work pinned all 69 router rows to `65d699b`, added
reasons for the six cataloged rows, recorded CT-15 positive request/response
digests and framing version, and added a separate
[fallback/alias inventory](../../../tests/fixtures/compatibility/router-fallback-manifest.tsv).
The bounded CF-01/03 fixture job hit its admitted cap before producing a file;
the disposable state-diff, source/auth negative and synthetic collision cases
remain open. The prior audit is a historical snapshot, not a claim that these
later corrections closed A.

After a verified quota reset, the same bounded job completed the
[CF-01/03 synthetic catalog](../../../tests/fixtures/compatibility/ct-01-03-negative-state.json).
It specifies conditional GET write-back capture, assigned versus content model
and cloud-auth disagreement, and shared audio ID with distinct 20-byte hashes.
All identities and hashes are synthetic; physical ownership is explicitly not
inferred. Integration validated four JSON catalogs, 12 unique fixture IDs, UID/
rUID reversal, binary lengths/digests, 69 ordered route rows and four fallback
rows. A is complete as source-derived compatibility evidence. B must execute
the disposable state/parser/harness cases; no wire or runtime claim exists.

Validation: architecture link/whitespace checks are required before publishing;
remote check evidence is recorded in the local execution state. A/B/T remain
open until their own artifacts and verification exist.
