# PI-08/A2.5a catalog-state schema checkpoint

Status: accepted schema prerequisite; PI-08/A2 and the PI milestone remain incomplete.

## Accepted implementation

- Initial checkpoint: `f9c6da02ecf56b2e40432e3d6be7e95ba65947a2`
- Accepted repair head: `bb35810d79c42c035c8e59f15b59b3b986975e82`
- Migration: `0006-catalog-aggregate-state`

The additive v6 migration stores bounded immutable review and order persistence
envelopes while retaining v5 normalized evidence and provenance. Existing v1-v5
migration bytes and checksums are unchanged. Existing catalog queries remain
available on clean v5 and v6 schemas; dirty, older unsupported and future schema
states fail closed. The new state-restoration ports remain unsupported until the
adapter checkpoint.

The schema binds review state to catalog subjects and order state to managed
content and version manifests. It checks encoding identity, subject/content and
revision envelope fields, byte bounds, relation integrity, monotonic append
rules and update/delete rejection. SQLite validates digest shape only; the
future adapter must compare SHA-256 before decoding or trusting state.

## Verification

A digest-pinned Go 1.27.1 Docker run passed focused SQLite, architecture,
catalog-domain/application, repeated and race tests. Next CI run 38832541071
passed all four required jobs:

- deterministic build and smoke checks;
- persistence/domain regression and race evidence;
- two-work-directory reproducibility;
- network advisory checks.

## Recovery and deferred scope

The rejected transient v6 checkpoint changed before acceptance. Any database
created from that unaccepted checksum would require explicit recovery rather
than silent adoption; no production deployment is claimed. Durable adapter
writes, SHA-256 verification, `LoadReview`, `LoadOrder`, lookup ports,
mutation and recovery evidence remain open.
