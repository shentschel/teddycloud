# Set-Tonie metadata exploration

Status: exploration  
Catalog snapshot: `toniebox-reverse-engineering/tonies-json@ecef6768e3fa823c0464bfb8097fa8f8b3ea7095`  
Scope: catalog schema, TeddyCloud lookup behavior, migration and API compatibility

## Decision summary

Set members must be represented as independent content identities. A set is a
grouping relation between those identities, not a Tonie whose tracks happen to
contain member titles.

The legacy `tonies.json` format should remain available during migration. A
new versioned format should model sets, members and audio versions explicitly.
The Tonie Manager may consume the resulting API fields, but must not own catalog
matching or set classification.

## Evidence

The catalog generator currently emits one flat record per article/data element.
All IDs receive the article as `model` and share one metadata object:

- [tonies-json generator](https://github.com/toniebox-reverse-engineering/tonies-json/blob/master/yaml2tonies-json.py)
- [Checker Tobi set 11001135](https://github.com/toniebox-reverse-engineering/tonies-json/blob/master/yaml/11001135.yaml)
- [WAS IST WAS set 11000788](https://github.com/toniebox-reverse-engineering/tonies-json/blob/master/yaml/11000788.yaml)
- [Profiwissen set 11003255](https://github.com/toniebox-reverse-engineering/tonies-json/blob/master/yaml/11003255.yaml)

The analyzed catalog snapshot contains 6,569 metadata records. 363 records have
multiple audio IDs. At least 13 entries are obvious four-member sets; twelve
already contain audio IDs. Four-member sets may contain 4, 5, 8 or 12 IDs
because individual members can have multiple audio versions.

For article `11003255`, member order and ID order differ. Positional inference
is therefore unsafe.

## Findings

### META-001: Parallel arrays do not express identity

`audio_id[]` and `hash[]` encode a pair only through their array position.
They cannot group multiple versions under individual set members.

### META-002: Hash lookup creates a cross product

`tonies_byAudioIdHash_base()` first finds an audio ID and then compares the
requested hash against every hash in the same catalog item. A mismatched
audio-ID/hash combination can therefore match.

Evidence: [src/toniesJson.c](../../src/toniesJson.c)

### META-003: Set members are stored as track descriptions

Set member titles currently live in `track-desc`. TeddyCloud consequently
returns the same model, episode and picture for every member.

### META-004: Model and product article are conflated

The set product article is used as `model` for all member content. Where the
member model is unknown, integrations currently have to invent values such as
`set-...`. Synthetic catalog identities must not be presented as Boxine model
numbers.

### META-005: The existing V2 implementation is incomplete

`toniesV2_readJson()` is empty, the V2 structures do not represent member
relationships, and normal API lookups still use the V1 cache.

### META-006: Heuristics are suitable only for review candidates

Names containing “Set”, matching list lengths or differing track counts can
identify candidates, but cannot prove member-to-version assignments. MyTonies,
known model metadata or verified TAF observations are required as evidence.

## Proposed V3 contract

```json
{
  "schema_version": 3,
  "contents": [
    {
      "content_id": "was-ist-was:erfindungen-bionik",
      "model": "2000002568",
      "series": "WAS IST WAS",
      "episode": "Erfindungen / Bionik",
      "picture": "https://example.invalid/cover.png",
      "set": {
        "article": "11000788",
        "position": 1
      },
      "versions": [
        {
          "audio_id": 1708683988,
          "hash": "9290EFD97572F1BDD302D7F7DF947EAB03CFB72B",
          "size": 57437593,
          "track_count": 25,
          "confidence": 1
        }
      ],
      "tracks": []
    }
  ],
  "sets": [
    {
      "article": "11000788",
      "title": "WAS IST WAS - Set",
      "members": [
        "was-ist-was:erfindungen-bionik"
      ]
    }
  ]
}
```

Contract rules:

1. `content_id` is required, stable and independent of a model number.
2. `model` contains only a verified model number and may be absent.
3. Each `versions[]` object owns its audio ID and hash as an inseparable pair.
4. One version belongs to exactly one content identity.
5. `tracks` contains actual TAF tracks, never set member names.
6. Set membership is explicit and ordered.
7. Provenance and confidence should be recorded for inferred assignments.

## Matching precedence

1. Exact `audio_id + hash` version match.
2. Audio ID alone only when it identifies exactly one content version.
3. Verified model match.
4. Heuristic title/set matching returns review candidates and never performs an
   automatic assignment.

Ambiguous lookups must be represented explicitly instead of returning the first
catalog entry.

## Migration strategy

1. Correct the V1 pair lookup independently of the schema migration.
2. Add a schema and generator for `toniesV3.json`; continue generating V1.
3. Pilot Checker Tobi, WAS IST WAS and article `11003255`.
4. Add a V3 parser and indexes to TeddyCloud with V1 fallback.
5. Extend API responses with optional `contentId` and `setInfo`; preserve
   existing `tonieInfo` fields.
6. Migrate local custom metadata without changing Custom Card sources or tag
   ownership.
7. Consume the optional fields in the Tonie Manager.

## Work packages

### SETMETA-001 — Correct audio-ID/hash pair matching

Scope:

- Compare only the hash paired with the matched audio ID.
- Detect malformed unequal arrays.
- Preserve TeddyBench audio-ID compatibility.

Acceptance criteria:

- A crossed audio-ID/hash combination no longer matches.
- Exact pairs and audio-ID-only lookups remain covered by tests.
- Custom entries retain precedence over the public catalog.

### SETMETA-002 — Specify and validate the V3 schema

Scope:

- Define contents, versions, sets, provenance and optional verified models.
- Publish machine-readable JSON Schema and valid/invalid fixtures.

Acceptance criteria:

- Multiple versions can be assigned to one member explicitly.
- One set can reference members with or without known model numbers.
- Parallel arrays and synthetic model numbers are unnecessary.

### SETMETA-003 — Generate V1 and V3 catalog artifacts

Scope:

- Extend the source YAML model with explicit set members.
- Generate V3 while keeping legacy V1 output stable.
- Reject unassigned or multiply assigned set audio versions.

Acceptance criteria:

- Existing non-set V1 records remain unchanged.
- Pilot sets produce one V3 content item per member.
- Generation is deterministic and tested.

### SETMETA-004 — Implement V3 parsing and indexed lookup

Scope:

- Parse V3 into owned C structures.
- Build indexes for exact version pairs, unique audio IDs and content IDs.
- Fall back to V1 when V3 is unavailable.

Acceptance criteria:

- No linear cross-product lookup is used.
- Ambiguity is reported rather than silently resolved.
- Initialization, reload and deinitialization are leak-tested.

### SETMETA-005 — Extend the TeddyCloud API

Scope:

- Add optional `contentId`, `setInfo` and match-quality information.
- Preserve the existing response contract for older clients.

Acceptance criteria:

- Existing Web UI and plugins continue to work unchanged.
- New clients can distinguish product set, member and audio version.

### SETMETA-006 — Migrate and verify set assignments

Scope:

- Use MyTonies and verified local TAFs as preferred evidence.
- Migrate Checker Tobi, WAS IST WAS and `11003255`.
- Add regression fixtures for 4, 5, 8 and 12-ID sets.

Acceptance criteria:

- Every pilot member has its own title and picture.
- Every known audio version maps to exactly one member.
- Unknown models remain empty rather than synthetic.

## Effort estimate

- Pair lookup fix: 2–4 hours.
- Schema, generator and fixtures: 6–10 hours.
- C parser, indexes and API: 10–16 hours.
- Migration and integration verification: 6–10 hours.

Expected total: 24–40 hours. A read-only generator prototype for the three pilot
sets is feasible in approximately 5–8 hours.

