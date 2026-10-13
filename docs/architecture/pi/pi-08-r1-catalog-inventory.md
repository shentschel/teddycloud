# PI-08/R1 catalog source and compatibility inventory

Status: evidence-only checkpoint; final PI-08/R policy is not accepted.
Issue: [PI-08/R #25](https://github.com/shentschel/teddycloud/issues/25).
Evidence baseline: `docs/pi-00-r-charter` at
`f6a0f066334d53cbe27ef2c7b017a02d213ade04` (2026-10-13 inspection).

## Scope and evidence limits

R1 inventories repository facts, persisted assumptions and decision seams. It
does not select source precedence, authenticate a provider, freeze provenance
vocabulary, change a schema, accept PI-08/R or admit A/B/T implementation.
The task-authorized bounded evidence-inventory exception to
[model routing](../task-model-routing.md) applies only here; final policy,
security/migration tradeoffs and independent acceptance remain Astra work.

Read inputs: [roadmap](../teddycloud-next-pi-roadmap.md), PI
[README](README.md), PI-04 [plan](pi-04-plan.md)/[review](pi-04-review.md),
PI-05 [plan](pi-05-plan.md)/[review](pi-05-review.md),
PI-06 [plan](pi-06-plan.md)/[review](pi-06-review.md), and
PI-07 [plan](pi-07-plan.md)/[review](pi-07-review.md).
Later acceptance sections supersede earlier pending-status paragraphs:
PI-06's transactional Tag milestone and PI-07's scoped software milestone are
accepted. Physical power-loss, deployment and production migration are not.

Paths and line spans below refer to the pinned baseline, not a live installation.
Code inspection establishes mechanisms, not execution results. Historical
explorations, proposed contracts and source claims are marked as such.
No live catalogs, household records, credentials, provider accounts or private
automation state were read. External generator/provider behavior was not
revalidated. Repository inspection used GitHub after the local process runner
failed before execution; local checkout contents and Git status are unknown.

## Source inventory and trust boundary

| Input / source class | Current evidence and ingress | What it can establish; boundary |
| --- | --- | --- |
| Public legacy V1 catalog | `tonies_init` loads configured `tonies.json`; `tonies_update` requests `api.revvox.de/tonies.json`. [E01], [E02] | Candidate editorial metadata, model and audio/hash assertions. A URL or successful download does not establish original authorship, accepted identity, freshness or overwrite authority. |
| Bundled catalog / external generator lineage | The repository tree includes `contrib/config/tonies.json`. The [Set exploration](../set-tonie-metadata-exploration.md) attributes a historical snapshot to `toniebox-reverse-engineering/tonies-json@ecef6768e3fa823c0464bfb8097fa8f8b3ea7095`. | Bundled file and documented upstream lineage are separate observations. R1 does not prove that the bundled bytes or today's download correspond to that revision; historical counts and pilot assignments are not newly measured evidence. |
| Local custom metadata | `tonies.custom.json` is loaded before public metadata. Custom API validates, normalizes and replaces entries by model, then reloads caches. [E01], [E03], [E04] | Local/user/plugin assertions, including deliberate overrides. Local origin alone does not prove physical Original/Custom status, model authenticity or provider provenance. Custom catalog entries and physical Custom Cards are different concepts. |
| V2 catalog scaffolding | Separate V2 cache/type declarations exist, but `toniesV2_readJson` is empty. The V2 update function uses V1 temporary/target variables and the V1 body callback. [E05] | A prospective format seam, not an operational richer catalog source. The declared confidence field has no demonstrated ingestion policy. |
| Legacy sidecars and overlays | `contentJson_t`, `load_content_json`, `content_json_update_model`, `getTonieInfo` and tag JSON projection. [E06], [E07] | Assigned model, source association, independent state flags and runtime routing. These are mixed legacy state, not a provenance-bearing catalog record. Secret-bearing fields must stay outside ordinary catalog evidence. |
| Local TAF bytes | Next `ValidateTAF` verifies finite envelope/payload facts and computes a complete-file digest; ContentFS verifies retained bytes. [E12], [E13] | Audio-ID/payload-SHA1 pair, size, track offsets and complete-byte identity. This verifies bytes/envelope, not title, commercial model, ownership, Set membership, provider authorship or audible playback. |
| MyTonies / NFC connector observations | The [dependency matrix](../current-system-dependency-matrix.md), enhancement-repository row for `teddycloud-nfc-sync`, records NFC files, MyTonies, catalog/tag APIs and TAF download. The Set exploration proposes these as corroborating evidence. | Documented external integration seam only. No MyTonies client or provider adapter exists in the inspected Next application/adapter tree; no current provider response, stable record-key contract or authentication/retention behavior is established here. |
| Reviewed/operator evidence | Generic `evidence.Source`, `Observation`, `Fact` and PI-06 Tag support decisions. [E10], [E17] | Existing internal evidence vocabulary and explicit review mechanisms. Constructors validate representation; they do not authenticate the caller or authorize catalog acceptance. Catalog review persistence/service still needs design. |
| Existing Next database facts | Content repository plus PI-07 version/blob/import tables. [E11], [E14], [E15] | Durable internal Content identity and verified media-import history. An import receipt proves the retained command relationship, not external catalog provenance. Existing rows have no catalog source/support columns. |

The boundary for later delivery is external bytes/claims -> bounded adapter
validation -> retained observations/candidates -> explicitly authorized
acceptance -> catalog projection. This is an acceptance concern, not a selected
new schema. A copied public record in a custom file, or MyTonies data relayed by
a plugin, must not acquire authenticated-provider status merely by claiming a
source name. [E10] accepts caller-supplied source and review values; issuer
binding, import permissions and review authorization remain unresolved.

Image URLs are also external input: the legacy reader can register/fetch them
through its cache settings [E02]. A future metadata import must decide whether
it records image references without fetching, or uses a separately bounded
asset worker. URI text and record locators must not become implicit network or
filesystem capabilities.

## Exact evidence ledger

All code links are repository-relative; line spans are baseline locators.

| ID | File / symbols and baseline lines | Observed assumption |
| --- | --- | --- |
| E01 | [settings.h](../../../include/settings.h#L20-L24) 20-24; [toniesJson.c](../../../src/toniesJson.c#L34-L60) 34-60, `tonies_init` | V1/V2 filenames and custom/public caches. V2 custom filename aliases V1 custom filename. |
| E02 | [toniesJson.c](../../../src/toniesJson.c#L64-L132) 64-132, `tonies_downloadBody`/`tonies_update`; 272-355 and 380-406, `tonies_readJson` | HTTP-200 body staging and delete/rename refresh; CA-check TODO is present, not proof of a transport exploit. V1 independently parses audio/hash arrays with 8-bit counts and no pairing check in this reader; image caching can fetch. |
| E03 | [toniesJson.c](../../../src/toniesJson.c#L446-L534) 446-534, lookup functions; 563-667, search | Audio match scans all hashes in an item, returns the first match, accepts a TeddyBench audio-ID offset, and searches custom before public. Model lookup is case-insensitive, returns first match and is a fallback after pair lookup. Search suppresses duplicate model strings and has 18 result slots; it is not a complete identity index. |
| E04 | [handler_api.c](../../../src/handler_api.c#L2672-L2923) 2672-2923, `validateToniesCustomJson`; 3018-3162, normalization; 3403-3515, save/model lookup; 3587-3635, upsert; 3844-3968, rename | API checks equal arrays, hash syntax, case-insensitive model uniqueness and duplicate positional pairs; upsert replaces the whole matching object. Temp/move, backup rotation and cache reload exist. Rename optionally updates sidecars after catalog save; no cross-file transaction is established. |
| E05 | [toniesJson.h](../../../include/toniesJson.h#L28-L59) 28-59; [toniesJson.c](../../../src/toniesJson.c#L135-L169) 135-169 and 439-444 | V2 has article/data/IDs and a numeric confidence field; parser is empty and updater uses V1 paths/callback. Do not treat structs as implemented ingestion. |
| E06 | [contentJson.h](../../../include/contentJson.h#L20-L47) 20-47; [contentJson.c](../../../src/contentJson.c#L73-L163) 73-163, 193-204, 236-261 | Sidecar stores source, assigned model and independent flags alongside cloud fields. Stream detection can adjust live/nocloud; version/error handling can save. Model enrichment uses legacy matching and marks updates. |
| E07 | [handler.c](../../../src/handler.c#L902-L918) 902-918 and 951-955; [handler_api.c](../../../src/handler_api.c#L4352-L4430) 4352-4430 | Assigned model and TAF-derived source model feed distinct `tonieInfo`/`sourceInfo`. `hasCloudAuth` includes override semantics. Tag read calls save, but persistence is gated by both `_updated` and `_create_if_missing`; call presence alone does not prove an unconditional GET write. |
| E08 | [catalog/content.go](../../../next/backend/internal/domain/catalog/content.go#L18-L88) 18-88; [catalog/external_ids.go](../../../next/backend/internal/domain/catalog/external_ids.go#L25-L50) 25-50, 52-90 and 133-147 | Content identity is opaque; facts are title plus separate optional model/article. ContentVersion owns ContentID, one inseparable fingerprint and order evidence. Model syntax validation does not certify a provider model. AudioID domain is uint64; it is not recency. |
| E09 | [catalog/resolver.go](../../../next/backend/internal/domain/catalog/resolver.go#L21-L103) 21-103; [version_order.go](../../../next/backend/internal/domain/catalog/version_order.go#L9-L65) 9-65 | Immutable in-memory multimaps preserve exact-pair/audio-ID ambiguity; sorting is by opaque version ID. Same version ID with different facts conflicts. Ordering requires accepted namespace/position; unknown and conflicting order are explicit. |
| E10 | [evidence/evidence.go](../../../next/backend/internal/domain/evidence/evidence.go#L40-L170) 40-170 | Source name/revision/record are bounded opaque text, never a fetched URI. Internal confidence is unknown/tentative/corroborated and review is independent. Canonicalization retains disagreement and rejects changed same-ID observations; accepted contrary claims conflict. No source registry or caller authentication exists in these constructors. |
| E11 | [application/catalog/ports.go](../../../next/backend/internal/application/catalog/ports.go#L11-L22) 11-22; [sqlite/content_repository.go](../../../next/backend/internal/adapters/sqlite/content_repository.go#L94-L111) 94-111 and 179-207 | Application catalog exposes only transactional Save and FindByID. Save overwrites title/model/article by ContentID with no expected editorial revision or provenance guard. No version query/import/review service is provided by this port. |
| E12 | [content/blob.go](../../../next/backend/internal/domain/content/blob.go#L10-L17) 10-17; [content/taf.go](../../../next/backend/internal/domain/content/taf.go#L74-L101) 74-101 and 117-183 | BlobID is SHA256 of complete original bytes; payload SHA1 and AudioID cannot substitute for it. Validation checks envelope, payload hash, offsets and EOF without catalog inference. |
| E13 | [contentfs/store_linux.go](../../../next/backend/internal/adapters/contentfs/store_linux.go#L77-L158) 77-158; [contentstore/service.go](../../../next/backend/internal/application/contentstore/service.go#L31-L68) 31-68 and 103-111 | Range verification checks descriptor/size/envelope/digest before output. Import preflights receipt, publishes media, checks envelope, then records DB facts under a shared operation fence; no SQL transaction spans filesystem I/O. Availability verification does not update metadata. |
| E14 | [sqlite/schema.go](../../../next/backend/internal/adapters/sqlite/schema.go#L3-L111) 3-111 | Migrations 1-4: Content title/model/article; Tag identity/evidence/support; blob, version, binding and import tables. Catalog provenance/history/support are absent. Version rows reference Content; one binding per version, many versions can share a digest; no unique audio/hash constraint. |
| E15 | [sqlite/blob_repository.go](../../../next/backend/internal/adapters/sqlite/blob_repository.go#L187-L268) 187-268 and 339-411, `loadVersion`; [content/import_command.go](../../../next/backend/internal/domain/content/import_command.go#L39-L83) 39-83 | RecordImport requires existing Content and rejects differing immutable version/blob facts. Command retains version/order, complete digest, size/profile and original canonical bytes. Media import restricts AudioID to uint32, unlike the wider pure domain type. |
| E16 | [migration/migration.go](../../../next/backend/internal/domain/migration/migration.go#L1-L5) 1-5, 54-100 and 226-280 | Read-only fixture prototype, not a production reader. It retains raw records/source references, marks CustomCard records protected, rejects unequal arrays and marks equal unproven arrays ambiguous. Explicit pairs are separate; multiple pairs are not silently collapsed into one version. |
| E17 | [sqlite/schema.go](../../../next/backend/internal/adapters/sqlite/schema.go#L38-L75) 38-75; [PI-06 review](pi-06-review.md#b1-reviewed-metadata-acceptance) | Durable reviewed support and revision fencing exist for four independent Tag facts. These are precedent and compatibility constraints, not an already implemented catalog provenance repository. |
| E18 | [set/set.go](../../../next/backend/internal/domain/set/set.go#L37-L68) 37-68; [assignment/assignment.go](../../../next/backend/internal/domain/assignment/assignment.go#L60-L86) 60-86 | Set stores ordered Content references, permits sparse positions/repeated references and defers cardinality policy. Assignment references ContentVersion, not a preferred/latest Content projection. |
| E19 | [sqlite/lifecycle_owner.go](../../../next/backend/internal/adapters/sqlite/lifecycle_owner.go#L27-L40) 27-40 and 75-115; [sqlite/migration.go](../../../next/backend/internal/adapters/sqlite/migration.go#L158-L181) 158-181 | One selected database owner and callback fence; applied migration identity/checksum drift fails. Future catalog writes must share ownership and append migrations rather than edit old ones. |
| E20 | [catalog/audit_test.go](../../../next/backend/internal/domain/catalog/audit_test.go#L9-L76) 9-76; [sqlite/missing_blob_test.go](../../../next/backend/internal/adapters/sqlite/missing_blob_test.go#L92-L144) 92-144 | Existing synthetic tests cover pair collisions across Content identities, immutable-order conflicts, unknown recency and actual missing-media/exact re-import with retained metadata. R1 inspected these tests; it did not rerun them. |

## Content versus ContentVersion and persisted implications

Content is the editorial work; a metadata correction cannot re-key it from a
title, model, article, source path or fingerprint. Current Content facts are
narrower than legacy metadata: series, episodes, language, cover and track labels
have no corresponding fields in the inspected Content value/table [E08], [E14].
A future importer must explicitly retain or report unsupported fields; silently
dropping them is not lossless migration.

ContentVersion is an immutable revision owned by one Content. Exact-pair
matching can still yield multiple versions or Content identities [E09], [E20].
Blob deduplication consequently cannot merge editorial identities or protected
Custom records. SHA1 payload evidence, complete-file SHA256, opaque version ID
and provider revision are different facts [E08], [E12], [E15].

PI-07 already persists versions, but through verified media import, not through a
general catalog service. Its reader joins version/blob facts and RecordImport
creates a binding and receipt [E15]. Catalog-only versions without local media
must be designed explicitly; merely inserting a version row is not evidence
that current media-oriented reads can query it. The distinction also preserves
missing media as an availability result, not deletion of catalog/ownership facts.

Order evidence is part of immutable version equality and canonical import
commands. Adding or correcting order on an existing version is therefore not a
harmless UPDATE: it can invalidate exact replay [E09], [E15], [E20]. Final R must
choose how later evidence relates to retained versions and receipts. Numeric
audio-ID, opaque source-revision text, import order and file mtime cannot supply
a default latest version.

Current Content Save is a replacement mechanism, not reviewed fact acceptance.
Reusing it as the future catalog-import policy would silently allow metadata
overwrite without support history or revision conflict [E11]. A new guarded
service may reuse transaction infrastructure, but cannot claim that protection
already exists.

## Compatibility seams to preserve or explicitly project

- Legacy V1 clients consume flat metadata and arrays. The custom write API has
  stricter validation than the V1 file reader; equal lengths alone do not prove
  pairing. Preserve original arrays/source coordinates for review [E02]-[E04],
  [E16]. TeddyBench's offset is a codec/compatibility rule requiring explicit
  adapter evidence, not a second editorial identity rule [E03].
- Custom precedence is existing legacy lookup behavior, not universal source
  trust. Custom API model keys/rename and catalog duplicate-pair rejection are
  scoped legacy contracts; Next allows different identities sharing metadata or
  fingerprints [E03], [E04], [E09]. Keep physical Custom Card protection and
  preferred-view selection separate from catalog identity or deletion.
- Assigned model, source model, cloud capability/override, claim state and media
  availability remain independent [E06], [E07], [E17]. A catalog import must not
  accept an assignment, assert physical ownership or reclassify a card from a
  URI, model prefix or `nocloud`.
- MyTonies/NFC inputs need an explicit mapping from provider/account-specific
  observations to catalog candidates. Authentication success would establish
  retrieval context, not universal editorial correctness. Source scope,
  credential isolation, safe locator retention and replay remain unknown until
  connector evidence is inspected; PI-14/PI-32 own later integration.
- Set tracks/title heuristics can nominate review candidates only. The historical
  [Set exploration](../set-tonie-metadata-exploration.md) reports flattened
  article/data and member-order problems; R1 does not revalidate its catalog.
  Existing Set relations reference independent Content, while model and article
  stay separate [E08], [E18]. No synthetic member model, positional assignment
  from parallel arrays, duplicate membership field or public V3 freeze follows.
- Follow existing single-owner, bounded transaction and verified backup/restore
  infrastructure [E19]. No legacy writer transfer, shadow write-back, live
  migration, media deletion or assignment change is authorized here.

## Open decisions for Astra: viable alternatives, none selected

| Decision | Option A | Option B | Required discriminator / remaining evidence |
| --- | --- | --- | --- |
| Source identity and issuer binding | Core-managed source registry with adapter identity, allowed scope and retained source revision/record. | Immutable import-batch identity with retained artifact digest and trusted importer identity; observations bind to that batch. | Distinguish publisher, mirror, relay and operator; neither arbitrary source labels nor artifact digests authenticate authorship by themselves. Decide authorized issuers and retention/redaction. |
| Provenance granularity | Field-level observations plus explicitly supported accepted projections. | Immutable record snapshots with explicit field-level overrides/conflicts where records disagree. | Lossless unsupported fields, query cost, correction history and bounded support size; catalog rules must not be inferred from Tag-specific limits. |
| Acceptance and precedence | Require explicit review for every conflicting or changed accepted fact. | Allow a versioned field/source-specific acceptance policy for bounded non-conflicting enrichment; conflicts still require explicit resolution. | Prove policy authority, support retention, concurrency fencing and permutation independence. Confidence alone never grants overwrite permission. |
| Confidence representation | Reuse internal unknown/tentative/corroborated values and retain provider confidence separately. | Add policy-versioned evidence dimensions with an explicit mapping to existing internal views. | Provider meanings, missing confidence and incompatible scales; no unreviewed public enum or invented numerical conversion. |
| Catalog-only versions | Separate catalog observations/candidates from media-backed version records until an accepted binding is available. | Add catalog-version query/write seams with optional media association, preserving existing media-service behavior via additive migration/projection. | Queryability without local files, absent versus corrupt binding, schema upgrade/exact restore and existing receipts. |
| Later order/correction evidence | Retain immutable version facts/receipts and attach separately versioned accepted ordering/correction decisions. | Create explicit superseding version records with links/support while retaining historical version/assignment/receipt identities. | Define whether new evidence changes a playable revision or its interpretation; never mutate old canonical commands. Both options need unknown/conflict and stale-candidate semantics. |
| V1 array admission | Keep all unproven arrays as unresolved candidates; require explicit source pair evidence. | Permit positional decoding only for a pinned source contract whose generator/validation evidence proves pairing, retaining the raw arrays and contract revision. | Equal length is insufficient; crossed/unequal arrays and Set member association need independent checks. Option B cannot silently weaken the existing fixture seam. |
| Legacy custom precedence | Preserve custom-first behavior only in a compatibility projection with explicit match/conflict evidence. | Require reviewed conflict resolution and expose an explicit compatibility-unresolved result rather than reproducing first-hit selection. | Older client response/status constraints and protected Custom records; later facade tests must specify behavior before writer transfer. |

PI-08/R may settle bounded internal service/import rules. Public V3 vocabulary,
cross-provider identity merging, Set cardinality, full V1/custom/MyTonies import
precedence and later review UX still need coordination with PI-13 through PI-17.
This checkpoint supplies alternatives without choosing those later contracts.

## Risk register

| Risk | Evidence / failure | Acceptance evidence to require later |
| --- | --- | --- |
| Ambiguity | Legacy first-hit and model search hide alternatives; exact pairs can collide across identities [E03], [E09]. | Query returns every bounded candidate and explicit no-match/unique/ambiguous outcomes; reordered input cannot change acceptance. |
| Overwrite / lost correction | Content UPSERT and custom object replacement lack catalog reviewed-history semantics [E04], [E11]. | Revision-fenced acceptance, exact replay and concurrent conflicting updates retain all evidence; no silent patch or same-ID mutation. |
| Provenance spoofing | Source/review constructors trust caller values; mirrors and local copies can claim upstream labels [E10]. | Untrusted import cannot mint a trusted issuer or accepted review state; retain acquisition context separately from asserted origin, without secrets. |
| Stale versions | Legacy release/model and current arrival order can be mistaken for recency; version order is immutable and namespace-specific [E02], [E09], [E15]. | Delayed/replayed source revisions preserve newer reviewed decisions, explicit unknown/conflict order and historical receipts; no numeric AudioID winner. |
| Parallel arrays / Set confusion | V1 reader crosses arrays; custom API checks shape but not member ownership; fixture seam refuses unproven pairing [E02]-[E04], [E16]. | Crossed/unequal/equal-unproven arrays, multiple member versions and explicit member-order fixtures produce lossless reports without fabricated pairs/models. |
| Media/catalog conflation | Existing version read/write is media-oriented, while missing bytes leave durable facts intact [E12]-[E15], [E20]. | Catalog-only queries, missing/corrupt/unavailable distinctions and exact re-import preserve Content, version, binding and receipt identities. |
| Compatibility drift / unsafe input | V2 is scaffolding; legacy image fetching, model rename, count truncation and differing AudioID widths are visible [E02], [E04], [E05], [E08], [E15]. | Bounded parser/count/text limits, explicit width conversion, no implicit fetch, unsupported-field report and tested V1/custom projections; source inspection is not an exploit or runtime certification. |

## Proposed final PI-08/R acceptance questions

1. Which concrete source classes and adapter/issuer identities are admitted?
   What distinguishes asserted origin, authenticated acquisition, artifact
   revision and record locator, including offline/custom and unavailable sources?
2. What evidence supports each accepted Content fact and ContentVersion relation?
   Are raw unsupported fields, dissent and superseded support retained and
   queryable with documented count/byte/deadline bounds?
3. Which commands may accept, enrich, correct or resolve a fact, under which
   expected revision and policy version? Can an untrusted importer spoof source,
   confidence or review authority, or overwrite a protected custom correction?
4. How do no-match, ambiguous identity, conflicting facts, stale evidence and
   unknown/conflicting recency appear in service results? Do all input
   permutations preserve the same outcome without first-hit or import-order rules?
5. How are catalog-only versions queried without media, and how are later
   ordering/correction decisions represented without mutating PI-07 receipts,
   historical assignments or version identity? How are uint64 domain AudioIDs
   handled at the existing uint32 media-import boundary?
6. What exact V1/custom/MyTonies/Set behavior belongs to PI-08 versus later
   adapters? What proves positional pairs, member identity and TeddyBench
   normalization; what remains unresolved? Which old-client projection preserves
   compatibility without copying custom-first lookup into global trust policy?
7. What additive migration and verified backup/exact-schema restore tests protect
   existing Content/Tag/version/blob/import rows? Which rollback retains originals
   and media, and how are incomplete/uncertain imports replayed safely?
8. Are A/B/T tasks bounded by exact files, named tests, dependencies, independent
   review, rollback and security criteria, with no production migration,
   assignment/classification change or public V3 freeze hidden in delivery?

Final R needs written decisions and runnable acceptance targets answering these
questions before delivery is admitted. R1 does not close issue #25 or claim the
PI-08 milestone.

## Verification and publication boundary

Only this file and one checkpoint link in README are in the write scope.
Documentation rollback is a focused revert; there is no persistent-state change.

Required verification is `python3 scripts/check_architecture_docs.py` plus
diff/scope/whitespace inspection. Local execution was blocked at process
creation with `Failed to create unified exec process: No such file or directory
(os error 2)`; no local checker pass or clean working tree is claimed.
The existing [documentation workflow](../../../.github/workflows/check-architecture-docs.yml#L36-L50)
runs the exact checker after a push to this branch. Publication must report its
observed result for the new commit and verify the remote two-file diff and head.
Runtime tests listed above are inherited inspected evidence, not tests run by R1.
