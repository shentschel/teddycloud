# PI-07/A1 TAF domain checkpoint

Status: accepted; PI-07/A remains open for A2 filesystem storage.

Code commit `90c4960902228e4b13153d559182e110aa55d32e` adds strict
`taf-envelope-v1` validation, versioned complete-file SHA256 BlobIDs, canonical
import-command bytes and deterministic synthetic fixtures for zero, one and 99
tracks. The implementation retains exact source bytes, streams payload hashing
without whole-file buffering and keeps AudioID, payload SHA1, blob identity and
catalog identity separate.

Parent verification passed focused package tests, the full backend suite, vet,
bounded `FuzzTAFEnvelopeHeader` and whitespace checks. Local race execution was
unavailable because the host lacks a C compiler. Commit
`fe969a2d16182490bdfe866844be4a6058511de1` extends remote race coverage to the
new domain and fixture packages; [Next CI run 37180869682](https://github.com/shentschel/teddycloud/actions/runs/37180869682)
passed all four jobs including that Linux race check.

Envelope validation is structural and cryptographic, not proof of playable
audio. A2 still owns qualified Linux descriptor storage, capability probes,
atomic no-replace publication and filesystem failure evidence. No database,
production media, deployment or migration was changed.
