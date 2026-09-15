# PI-02 review

Status: R delivered; milestone incomplete

R inventories the source router families and seven enhancement seams at pinned
local revisions. It records confirmed GET write-back, different auth flag
semantics, source metadata precedence, form assignment/readback and a bare-array
plugin discovery response.

The removal behavior needs a correction to the earlier conversation assumption:
in this source snapshot `tbs_tag_removed` sends MQTT only. Existing browser code
also recognizes an RTNL removal signal. A negative-UID SSE event is a supported
client input, not evidence that this server emits it. CF-02 owns the fixtures.

Web UI source, complete handler/status tracing, fixture files and harness tests
remain A/B work. No production API, cloud content request or device operation
was performed. R provides source evidence and planning, not runtime conformance.

Validation: architecture link/whitespace checks are required before publishing;
remote check evidence is recorded in the local execution state. A/B/T remain
open until their own artifacts and verification exist.
