# PI-02 contract inventory

Status: source inventory and refinement; wire observations pending

## Reproducible source boundary

The reviewed local snapshots are `shentschel/teddycloud` at `65d699b`,
`teddycloud-plugin-common` at `6993c28`, `teddycloud-tonie-manager` at `df1370b`,
`teddycloud-current-tonie` at `a082043`, `teddycloud-unknown-tonie` at `f628cdc`,
`teddycloud-youtube2tonie` at `28654ea`, `teddycloud-nfc-sync` at `81cb6d1` and
`teddycloud-enhancements` at `87f153a`. These are local source revisions, not
verification that the production installation or remote heads match them.

The Web UI submodule is pinned to `e2f40c2c69a3b2b46093ddc5542773dd0fa0067e`
and is not initialized. Its manifest rendering, iframe and navigation contracts
still need source inspection in A. No device requests were replayed in R.

## Router inventory and ownership

All route families below come from [request_paths](../../../src/server.c).
Matching is by prefix and method, with server-type gating. A must enumerate
each concrete entry with its handler and preserve order; aliases, suffixes,
fallbacks and error-to-HTTP translation require separate cases. A route's name
or GET verb does not establish that it is read-only. Effects marked `review`
still require handler tracing; they are not approved for shadow replay.

| ID | Routes or family, method | Effects and target owner | Required fixture group |
| --- | --- | --- | --- |
| CT-01 | GET `/api/getTagInfo`, `/api/getTagIndex` | Confirmed metadata write-back; Tag/Library facade | Present/missing JSON, TAF, source disagreement, overlay, before/after files |
| CT-02 | GET `/content/json/get/`, `/content/json/`, `/content/`; POST `/content/json/set/` | Raw files and assignment mutation; Assignment adapter | Form body, omitted fields, invalid UID, write failure, readback |
| CT-03 | GET `/content/download/`, `/cache/` | Download delegates to cloud/local content handler; Content worker | Local/cache miss, header skip, ranges, interrupted transfer |
| CT-04 | GET `/api/toniesJsonSearch`, `/api/toniesJson`, `/api/toniesCustomJson`, `/api/tonieboxesJson`, `/api/tonieboxesCustomJson` | Catalog reads, review indirect effects; Catalog service | Wrappers, search fields, custom precedence, shared audio/model collisions |
| CT-05 | GET `/api/toniesJsonUpdate`, `/api/toniesJsonReload`; POST `/api/toniesCustomJsonUpsert`, `/api/toniesCustomJsonDelete`, `/api/toniesCustomJsonRename` | Catalog mutation and dependent assignments; Catalog service | Atomic save failure, rename references, duplicate models, reload |
| CT-06 | GET `/api/settings/getIndex`, `/api/settings/get/`; POST `/api/settings/set/`, `/api/settings/reset/`, `/api/settings/removeOverlay` | Settings/overlay mutations; Settings service | Secret exclusion, overlay scope, defaults and invalid keys |
| CT-07 | GET `/api/getBoxes`, `/api/trigger`; POST `/api/assignUnknown` | State/commands and assignment, review GET trigger; Gateway/Assignment | Box scope, command errors, unknown assignment |
| CT-08 | GET `/api/sse`; ANY `*binary` | Subscription and RTNL state updates; Gateway/Event Hub | Framing, missing removal, reconnect, late lookup, two boxes |
| CT-09 | POST `/api/auth/login`, `/api/auth/refresh-token`; GET `/api/auth/logout` | Session mutations; Authentication boundary | Missing/expired token, cookie scope, refresh/logout |
| CT-10 | GET `/api/plugins/get` | Directory discovery; legacy extension adapter | Bare array, extra directory, absent directory, bad manifest |
| CT-11 | POST `/api/fileDelete`, `/api/fileMove`, `/api/dirDelete`, `/api/dirCreate`, `/api/fileUpload`, `/api/fileEncode`, `/api/pcmUpload`, `/api/tafUpload` | Files and media mutations; Content worker | Paths, partial files, limits, multipart failures |
| CT-12 | GET `/api/fileIndex`, `/api/fileIndexV2`, `/api/stats`, `/api/cacheStats`; POST `/api/cacheFlush`, `/api/migrateContent2Lib` | Index/operational reads and mutations; Content/Admin | Library special paths, cache invalidation, migration recovery |
| CT-13 | POST `/api/uploadCert`, `/api/esp32/uploadFirmware`, `/api/esp32/extractCerts`; GET `/api/getFile/ca.der`, `/api/getFile/c2.der`, `/api/esp32/patchFirmware` | Certificate/firmware workflow, review GET effects; Gateway administration | Synthetic certs only, failed extraction/patch, denied access |
| CT-14 | ANY `/reverseGeneric`, `/reverse` | External proxy; Connector boundary | Method/query preservation, errors, timeout, credentials removed |
| CT-15 | GET `/v1/time`, `/v1/ota`, `/v1/claim`, `/v1/content`, `/v2/content`; POST `/v1/freshness-check`, `/v1/log`, `/v1/cloud-reset` | Device protocol with state/content/cloud effects; Legacy gateway | HTTP and protobuf corpus, claim/cache state, resets and TLS |
| CT-16 | POST `/v3/freshness-check`, `/v3/check-ota`, `/v3/setup-status`; GET `/v3/ota`, `/v3/chapter`, `/v3/content-meta` | Device protocol, handler review; Legacy gateway | Versioned payloads, missing chapters/content, device qualification |

CT-15 includes the `/v2/content` route; it must not disappear when enumerating
only `/v1` and `/v3`. Static web fallback and protocol framing outside the route
table are separate inventory entries for A.

## Confirmed compatibility traps

1. `getTagInfoJson` calls `saveTonieInfo(..., true)` before returning tag data.
   Tag index invokes the same helper. A snapshot request can update metadata;
   capture both response and filesystem changes on a disposable copy.
2. `hasCloudAuth` is `_has_cloud_auth && !cloud_override`. Download-trigger
   eligibility tests `_has_cloud_auth` separately, alongside `exists`, `nocloud`
   and system-tag handling. Preserve these distinct semantics in the adapter.
3. Returned metadata can contain both `tonieInfo` and `sourceInfo`. Common's
   `model()`/`title()` prefer `sourceInfo`; comparison solely by displayed title
   loses the assigned-model versus content-model distinction.
4. Assignment accepts URL-encoded fields even when Common sends `text/plain`;
   NFC Sync sends `application/x-www-form-urlencoded`. `source`, `tonie_model`,
   `live`, `nocloud`, `hide`, `claimed` are individually optional. Common rereads
   after writing and treats a mismatched readback as an uncertain write result.
5. SSE has an event name and JSON `{type,data}` envelope. Placement emits
   `TagValid`/`TagInvalid` with a compact UID; callers reverse bytes for rUID.
   In this snapshot, `tbs_tag_removed` emits MQTT only, not SSE. Current Tonie
   also consumes `rtnl-raw-log2` removal (function group 15, function 8630).
   A synthetic negative `TagValid` case must be labelled client compatibility,
   not a server-observed removal. Playback `stopped` is not proof of removal.
6. `sse_rawData` broadcasts to active subscriptions; this envelope lacks a box
   ID. Two-box isolation and reconnect reconciliation are open requirements,
   not capabilities supplied by today's placement event.
7. `/api/plugins/get` serializes the directory-name array itself. It does not
   return a `{plugins: [...]}` wrapper or validate each manifest in this handler.
8. Common classifies cards partly from `nocloud`, URI patterns and model prefixes.
   This is existing client behavior, not authoritative physical-card evidence.
   Migration must retain explicitly protected Custom Cards independently.

Sources: [tag/content API](../../../src/handler_api.c),
[SSE writer](../../../src/handler_sse.c),
[box state events](../../../src/toniebox_state.c),
[RTNL schema](../../../proto/toniebox.pb.rtnl.proto).

## Enhancement seams

| Component | Source entry at pinned revision | Contract to retain/test |
| --- | --- | --- |
| Common | `src/teddycloud-common.js` | UID validation, nested metadata precedence, cached index, form assignment/readback, version 1.0.1 and contract 1.0.0 |
| Manager | `src/teddycloud-gateway.js` | Common version checks, index refresh, copy and event subscription lifecycle |
| Current Tonie | `src/teddycloud-gateway.js`, `src/domain.js` | TagValid and RTNL removal fallback, playback state, unsubscribe |
| Unknown Tonie | `src/teddycloud-gateway.js` | fileIndexV2 library lookup, local search/custom upsert/reload, assignment, external catalog fetch |
| YouTube2Tonie | `backend/app.py`, `y2t_assignment.py`, `y2t_metadata.py` | Separate service GET health/jobs/job, POST jobs/part assignment; 404/409/429, multipart outputs, title/cover metadata, common assignment endpoint |
| NFC Sync | `nfc_sync_io.py`, `nfc-taf-download.py`, `mytonies-sync.py` | JSON catalog writes, form model update, downloadTriggerUrl, external credentials, idempotency and rate limiting |
| Installer | Existing dependency-matrix deployment mapping | A must inspect actual service units, proxy/base paths, shared asset installation, rollback and release pins |

Paths in this table belong to the named repositories, not this fork. Their local
snapshots may lag deployment. Tests against stubs must not be described as
hardware or cloud-provider conformance.

## Fixture contract and follow-up ownership

Every fixture needs a stable ID, CT family, source revision, producer/consumer,
origin (`synthetic`, `source-derived`, `sanitized-observed`), request method/path/
headers/body, response status/headers/body or framed events, initial/final state,
side effects and expected assertion. Binary cases carry schema and framing
version plus a digest. Synthetic identities must preserve UID/rUID relations and
audio/hash collisions without reproducing household identifiers or credentials.

| Finding | Owner | Required closure |
| --- | --- | --- |
| CF-01 GET write-back and download effects | PI-02/A,B | Response plus state-diff fixture; exclude from production shadow replay |
| CF-02 Removal/SSE and multi-box ambiguity | PI-02/A,B; PI-11/29 implementation | Separate observed and client-supported events; reconnect/two-box cases |
| CF-03 SourceInfo/auth/classification differences | PI-02/A,B; PI-04/09 | Positive/negative fixtures and explicit adapter policy |
| CF-04 Uninitialized UI and deployment mapping | PI-02/A | Read pinned Web UI and installer sources; record base paths and discovery contract |
| CF-05 HTTP statuses and protobuf framing incomplete | PI-02/A,B | Trace server fallback, auth gates and framing; synthetic corpus with explicit unsupported cases |

R's exit is an actionable inventory and fixture plan. Full endpoint behavior,
fixture execution and runtime compatibility remain A/B and later gateway gates.
