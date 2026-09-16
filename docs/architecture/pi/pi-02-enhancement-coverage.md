# PI-02/A enhancement compatibility disposition

Status: source-level coverage only; no deployed-suite or device conformance

The seven seams are the six pinned component archives plus the meta installer
in the [contract inventory](pi-02-contract-inventory.md) and
[deployment note](pi-02-deployment-seams.md). `cataloged` means an initial
synthetic/source-derived fixture exists for one mechanism; `deferred` names
the missing acceptance evidence. None is unsupported or production-verified.

| Seam | Current fixture/source disposition | Deferred acceptance evidence |
| --- | --- | --- |
| Common | CT-01/02 synthetic tag wrapper, UID/rUID and form assignment; source-level Common version/asset path cataloged | Browser global/version mismatch, wrong content type, stale index, source/auth classification and negative readback in disposable install |
| Tonie Manager | CT-01/08/10 source-level index, event and discovery cataloged | Multiple-card retention, copy lifecycle, teardown and actual UI timing in a browser fixture |
| Current Tonie | CT-01/08 synthetic placement/removal distinction cataloged | RTNL fallback, playback state, reconnect and two-box isolation; server removal is MQTT-only in reviewed source |
| Unknown Tonie | CT-01/10 and pinned library/index source cataloged | Catalog search/upsert/reload, model ambiguity and external catalog response in isolated stubs |
| YouTube2Tonie | Plugin path/service/API contract source-cataloged in inventory | Separate backend health/job/part assignment, response statuses, cover/title and interruption against synthetic service |
| NFC Sync | Worker/service/timer and catalog/form update source-cataloged | Dry-run rate limits, cert path, download-trigger behavior and idempotency in isolated fixture; no real NFC/cloud contact |
| Meta installer | Lockfile pins, archive checks and rollback path source-cataloged | Disposable wrong-digest/version, missing Common, no-change second run and reverse-order rollback test |

The [pinned Web UI seam](pi-02-webui-seam.md) adds the manifest/iframe/library
consumer side, but the submodule is not initialized locally. For A, every seam
has an explicit disposition; B/T own negative executable cases and consistency
checks. Source-derived evidence must not be promoted to wire compatibility.
