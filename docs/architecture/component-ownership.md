# TeddyCloud Next component and write ownership

Status: proposed ownership boundary from PI-00/A; subject to PI-01 ADR review
Source baseline: `cc35138`

## Ownership rules

Each durable fact has one authoritative writer. Read models, adapters, plugins and
workers may propose or project state, but they do not become competing writers.
Filesystem blobs and relational metadata can use different stores while one core
command owns their consistency. Migration changes ownership operation by
operation after reconciliation and rollback checks.

## Target component boundaries

| Component | Owns and writes | Reads or exposes | Must not own |
| --- | --- | --- | --- |
| Protocol Gateway | Device sessions, protocol-local cache and connection telemetry | Device TLS/protobuf/RTNL; compatibility commands/events | Catalog truth, UI state, user assignment policy |
| Tag Registry | Physical Tag identity, normalized UID/rUID, ownership/claim and auth-state facts | Tag queries and lifecycle state | Content metadata or preferred-library grouping |
| Content Store | TAF/cover blob lifecycle, digest, staging and blob references | Range/content reads and validated imports | Catalog matching or tag assignment |
| Catalog Service | Content, ContentVersion metadata, Set relations, provenance and review candidates | Exact/conflicting lookup outcomes and catalog queries | Physical-card classification from metadata alone |
| Assignment Service | Current and historical Tag-to-ContentVersion assignment | Idempotent assign/unassign/readback commands | TAF bytes, catalog enrichment or UI filtering |
| Library Query | Stable paginated projection, search, filters and preferred-record policy | Read-only joins of Tag, Content, Assignment and blob availability | Canonical mutations or destructive deduplication |
| Event Hub | Event vocabulary, delivery cursor and subscriber lifecycle | SSE/SDK adapters and invalidation | Authoritative domain state |
| Settings/Secrets | Validated settings, overlay ownership, secret references and audit metadata | Redacted settings API; scoped credential handles | Returning private keys to UI/plugins |
| Extension Registry/SDK | Manifests, commands, semantic slots and namespaced extension state | Typed core clients and events | Internal React state or direct core/database mutation |
| Media Worker | Media job state, temporary inputs and conversion checkpoints | YouTube/media sources; validated import API | Core blobs after import, assignments or global credentials |
| Connector Worker | Sync schedules, source cursors, retry/rate-limit checkpoints | NFC/MyTonies/catalog sources; candidate/import APIs | Core database, authoritative catalog or assignments |
| Installer/Release | Package manifest, migration orchestration and rollback procedure | Health/readiness and backup/export commands | Runtime domain state |

## Authoritative mutation matrix

| Operation | Target writer | Other participants | Migration constraint |
| --- | --- | --- | --- |
| Register or update physical Tag | Tag Registry | Gateway reports observations | UID/rUID byte order and claim semantics require fixtures |
| Store cloud-auth eligibility/facts | Tag Registry | Gateway/connector supplies evidence; Secrets supplies handles | `hasCloudAuth`, auth bytes and `cloud_override` need explicit semantic mapping |
| Create/update ContentVersion metadata | Catalog Service | Connector and review workflow propose provenance-bearing data | Exact audio-ID/hash conflicts remain explicit |
| Add/change Set membership | Catalog Service review command | Connector proposes candidates | Track titles and array position cannot authorize membership |
| Import/delete TAF or cover blob | Content Store | Gateway/media/connector streams staged input | Deletion requires reference and rollback checks; partial files never become authoritative |
| Assign/unassign Tag | Assignment Service | UI/extension/worker invokes command; Content Store validates availability | One idempotent writer; legacy JSON projection is reconciled |
| Select preferred Original | Library Query policy | Tag, Assignment, Catalog and Content facts | Read-only choice; never deletes Custom Cards or unresolved alternatives |
| Upsert custom catalog metadata | Catalog Service review command | Unknown-Tonie UI submits correction | Custom catalog metadata is not proof of Custom Card hardware |
| Change settings/overlay | Settings Service | Administration UI | Validate and audit; isolate secrets from ordinary settings |
| Publish domain event | Owning core service through Event Hub | Gateway/workers report input events | Event publication follows authoritative commit |
| Store media/sync job progress | Respective worker | Event Hub mirrors progress | Worker checkpoint may be rebuilt without changing core truth |
| Install/upgrade/rollback | Installer/Release | Core exposes backup, migration and health gates | No domain writes outside versioned migration commands |

## Legacy API ownership map

This map is provisional until PI-02 captures contracts and side effects.

| Current interface | Target contract | Target owner | Compatibility concern |
| --- | --- | --- | --- |
| `/api/getTagIndex`, `/api/getTagInfo` | Paginated library/tag queries | Library Query plus Tag Registry | Preserve wrappers and UID/rUID format; remove implicit write behavior only behind a tested adapter |
| `/content/json/get/` | Assignment query | Assignment Service | Preserve source/model projection while separating assigned and source metadata |
| `/content/json/set/` | Idempotent assignment command | Assignment Service | Preserve existing clients and readback; prevent dual writers |
| `toniesJson*` | Catalog import/query | Catalog Service | Report ambiguity; preserve V1 facade during V3 migration |
| `toniesCustomJson*` | Reviewed custom-metadata commands | Catalog Service | Preserve precedence and user-curated corrections |
| `/api/sse` | Typed event subscription with SSE adapter | Event Hub | Preserve placed/removed/playback envelopes until clients migrate |
| Content upload/download and cloud fetch | Blob import/range read | Content Store | Staging, range semantics, auth handles and interrupted-write recovery |
| Settings and overlays | Typed settings commands/queries | Settings/Secrets | Preserve overlay selection; redact secret values |
| Plugin discovery/navigation | Manifest V2 and legacy iframe adapter | Extension Registry | Semantic slots are public contracts; DOM selectors are not |

The legacy `getTagInfoJson()` call to `saveTonieInfo()` is an observed ownership
violation from the target perspective. While legacy JSON remains authoritative,
the legacy handler may retain that behavior. Before the core becomes writer, PI-02
must capture the mutation and a migration adapter must turn it into an explicit,
idempotent reconciliation command or make the target query pure.

## Enhancement repository mapping

| Repository | Target location | Supported integration | Mutation path |
| --- | --- | --- | --- |
| `teddycloud-plugin-common` | Public SDK and generated clients | UID/rUID normalization, events and typed commands | Calls owner services; contains no independent domain writer |
| `teddycloud-tonie-manager` | Standard Tonies/Library UI | Preferred view, Custom Cards/details toggles, all-card IDs | Queries Library; Copy invokes Assignment Service |
| `teddycloud-current-tonie` | Current-tag UI extension | Typed placed/removed/playback events and Copy command | Event reads; Assignment Service handles Copy |
| `teddycloud-unknown-tonie` | Catalog review workflow and UI extension | Candidate ranking, provenance and curated correction | Catalog Service review commands only |
| `teddycloud-youtube2tonie` | Media Worker plus UI extension | Conversion jobs, playlists, thumbnail and metadata proposals | Content Store import, then explicit Assignment command |
| `teddycloud-nfc-sync` | Connector Worker | NFC/MyTonies discovery, TAF sync and reconciliation | Catalog candidate and Content import APIs; no direct database access |
| `teddycloud-enhancements` | Installer/Release | Versioned bundle, backup, migration and rollback | Versioned operational commands only |

## Acceptance walkthroughs

The [system context](system-context.md) documents the complete successful and
interrupted Copy flow and the offline/interrupted Connector flow. Ownership is
unambiguous in both:

- Assignment Service is the only assignment writer; Content Store only proves
  availability and Event Hub publishes after commit.
- Connector Worker owns retry state; Catalog Service and Content Store own the
  accepted metadata and TAF. A downloaded file does not create an assignment.
- Failed legacy projection is reported as reconciliation debt and cannot cause a
  second component to rewrite core truth opportunistically.

## Open decisions and evidence

- PI-01/B decides process/repository topology, persistence and cutover ordering.
- PI-02 inventories every legacy mutation, status code and event envelope.
- PI-04/PI-13 decides Set cardinality and the final versioned schema.
- PI-09 defines the assignment transaction/history contract and legacy adapter.
- PI-34/PI-38 prove import, dual-run reconciliation and rollback on snapshots.
- Hardware and credential ownership remain proposals until deployment and device
  evidence are recorded; this document does not certify them.
