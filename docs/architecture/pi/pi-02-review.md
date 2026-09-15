# PI-02 review

Status: R delivered; A source-derived checkpoint; milestone incomplete

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

Web UI source, complete handler/status tracing, CF-01…05 and harness tests
remain A/B work. This checkpoint is not runtime conformance.

Validation: architecture link/whitespace checks are required before publishing;
remote check evidence is recorded in the local execution state. A/B/T remain
open until their own artifacts and verification exist.
