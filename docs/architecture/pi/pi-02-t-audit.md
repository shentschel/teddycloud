# PI-02/T technical-debt and evidence audit

Audit target: `teddycloud` `ccec417a6d39694ef57cc17567d87fb5f74c5eac`.
Scope is the reproducible initial compatibility-evidence milestone defined by the
[PI-02 plan](pi-02-plan.md), not TeddyCloud wire, browser, provider, device or
production conformance.

## Acceptance audit

| T criterion | Result | Evidence and boundary |
| --- | --- | --- |
| Unique fixture identities and reconciled semantic overlap | **Pass** | The four JSON catalogs contain 12 cases and 12 unique IDs. Canonical JSON hashes found no exact duplicate cases. Three request paths intentionally overlap: CT-01 source-response versus disposable state execution, CT-10 successful bare-array discovery versus missing-directory failure, and CT-15 valid protobuf versus truncated protobuf. These pairs are complementary, not duplicate assertions. |
| Provenance, revisions and source evidence | **Pass** | Every catalog has an origin and source revision; all four declared revisions exist locally. All 50 parsed source path/line references resolve at their catalog's declared revision. The [validator](../../../scripts/check_compatibility_fixtures.py) also requires revision/origin, source evidence and the fixture-contract fields. |
| Synthetic-only identities and collision evidence | **Pass** | All 12 cases inherit or declare synthetic/source-derived provenance. Five identity objects have valid eight-byte UID/rUID reversal. The collision case has two distinct 20-byte synthetic hashes sharing one audio ID. No fixture contains a certificate, credential, sanitized household capture or ownership inference. The [reference adapter](../../../scripts/pi02_reference_adapter.py) leaves physical-card ownership explicitly unsupported. |
| Secret-pattern audit | **Pass** | A count-only scan covered 21 PI-02 fixture, manifest, harness, test and documentation artifacts. Counts were zero in each class: private-key header, AWS access key, GitHub token, bearer JWT, URL user-info and credential assignment. Matches and candidate values were never printed. This is a strong-pattern check, not a proof against every possible secret encoding. |
| Router and fallback reconciliation | **Pass** | The [router manifest](../../../tests/fixtures/compatibility/router-manifest.tsv) has 69 rows and matches the pinned `65d699b` `request_paths` tuples and order exactly: 6 cataloged and 63 deferred. Every row has a contract, owner and reason. The [fallback manifest](../../../tests/fixtures/compatibility/router-fallback-manifest.tsv) has four ordered entries: one source-cataloged prefix alias and three reasoned deferred web fallbacks. Route disposition is not behavior coverage. |
| Seven enhancement seams | **Pass** | The [seam disposition](pi-02-enhancement-coverage.md) has exactly Common, Tonie Manager, Current Tonie, Unknown Tonie, YouTube2Tonie, NFC Sync and Meta installer. Each row separates current source/fixture evidence from deferred executable evidence. |
| Offline contract execution | **Pass** | The corpus validator accepts four catalogs, 12 cases and two manifests. Five mutation tests cover the accepted corpus plus four meaningful rejection codes. Three reference-adapter tests execute atomic CF-01 state replacement, CT-02 optional form/readback and CF-03 model/auth separation. The [C harness](../../../tests/ct15_protobuf_parser_harness.c) and [temporary-build test](../../../tests/test_ct15_protobuf_parser.py) execute TeddyCloud's bundled protobuf-c request/response code. |
| CI evidence | **Pass** | Public run [35096077623](https://github.com/shentschel/teddycloud/actions/runs/35096077623) is a successful completed run for exact head `ccec417`; documentation, corpus, mutation, protobuf-c and reference-adapter steps all passed. The audit host lacks a C compiler and locally reports the parser test as a clear skip; remote CI supplies the required compiled execution evidence. |
| Unsupported behavior and downstream ownership | **Pass** | Source-only, offline-executed and deferred claims remain distinct in the [inventory](pi-02-contract-inventory.md), [status/framing matrix](pi-02-status-framing.md), [Web UI seam](pi-02-webui-seam.md), [deployment seam](pi-02-deployment-seams.md) and historical [A audit](pi-02-a-acceptance-audit.md). No fixture is promoted to captured HTTP, browser, device, provider or production evidence. |

## Corpus overlap and normalization decision

| Shared request | Cases | Reconciliation |
| --- | --- | --- |
| GET `/api/getTagInfo?ruid=8877665544332211` | `A-CT01-info-writeback`, `A-CF01-disposable-get-writeback` | The first fixes response wrapper/source semantics; the second executes the declared semantic file-state transition on a disposable copy. |
| GET `/api/plugins/get` | `A-CT10-bare-array`, `A-CT10-missing-plugin-directory` | Positive bare-array shape and negative pre-response directory failure are distinct branches. |
| POST `/v1/freshness-check` | `A-CT15-freshness-raw-protobuf`, `A-CT15-truncated-protobuf-message` | Positive decode/exact response pack and malformed-input rejection are distinct parser outcomes. |

No case, ID or canonical case body is duplicated. Origin strings retain useful
qualifiers such as “not wire-observed” while consistently identifying the data as
synthetic and source-derived. No existing fixture, validator or test was changed:
normalization would add churn without resolving an inconsistency.

## Coverage reconciliation

The 69 route rows cover all 16 CT families plus the `/robots.txt` `OTHER` row.
Six rows are cataloged for the initial CT-01/02/08/10 slice; the remaining 63
retain a named owner and a concrete deferral reason. The four non-table fallback
rows separately preserve the `getPlugins` prefix alias, root redirect, Web SPA
fallback and static-file fallback. This closes inventory accounting, not the
deferred route behaviors.

The seven enhancement rows likewise satisfy disposition coverage. Their deferred
browser, service and installer tests remain downstream qualification work and do
not contradict the bounded source/offline milestone.

## Remaining gaps and downstream owners

| Gap outside PI-02 acceptance | Downstream owner | Required evidence before a broader claim |
| --- | --- | --- |
| Physical devices, firmware generations, device-facing TLS, claim/content and RTNL | Legacy gateway; product owner supplies device/firmware observations; cutover gate PI-38/47 | Sanitized device matrix, TLS/firmware qualification and rollback evidence before support or cutover. |
| SSE removal, reconnect and two-box isolation | Gateway/Event Hub; PI-11/29 implementation | Ordered event, reconnect and multi-box runtime tests; preserve that reviewed server removal is MQTT-only. |
| Catalog collision, source/auth classification and ownership policy | Catalog service and Tag/Library facade; PI-04/09 and PI-04/15 | Exact/crossed pair and classification policy tests without inferring physical ownership. |
| Browser base path, plugin discovery/manifest/iframe/library behavior and performance | PI-03 browser/runtime baseline; Web boundary and named Common/Manager/Current/Unknown component owners | Pinned browser build, same-origin and missing-file cases, lifecycle tests and later PI-10/26 measurements. |
| External cloud, reverse proxy, YouTube2Tonie and NFC services | Connector boundary plus YouTube2Tonie and NFC Sync component owners | Isolated stubs first; separately authorized provider qualification with credentials removed. |
| Installer paths, archive checks, service units and rollback | Meta installer owner; deployment/cutover gate PI-38/47 | Disposable install, wrong digest/version, missing Common, idempotency and reverse rollback. |
| Numeric HTTP error status, headers, socket behavior and static fallback runtime | Per-route owners in the router manifest; Web boundary for fallback rows | Disposable server execution. `ERROR_FAILURE` remains insufficient evidence for HTTP 500. |

These gaps remain explicit and owned. They do not block the initial evidence
milestone because its acceptance requires dispositions and offline contracts, not
runtime conformance or a cutover.

## Validation record and conclusion

Local read-only validation at `ccec417` produced:

- architecture checker: 32 documents passed before adding this audit;
- compatibility validator: four JSON catalogs, 12 cases and two route manifests;
- mutation suite: five tests passed;
- reference-adapter suite: three tests passed;
- protobuf-c suite: one clear local skip because no system compiler is installed;
- route comparison: 69/69 pinned tuples and order matched;
- fallback/seam accounting: 4/4 and 7/7;
- provenance: four revisions present and 50/50 source references resolved;
- identity/collision checks: 5/5 UID/rUID relations and two distinct 20-byte hashes;
- remote CI: all five workflow checks passed at exact head `ccec417`.

**Conclusion: PI-02 is technically accepted at its stated “reproducible initial
compatibility evidence” boundary.** There is no T-owned evidence blocker and no
cutover, deployment or wire-conformance claim. Parent integration must commit and
push this audit artifact; merge and deployment remain separate decisions.
