# PI-07/B2a verified ranges checkpoint

Status: accepted as a bounded B2 checkpoint; inventory, quarantine and crash
reconciliation remain open.

Code commit `1a3ee4c49567adcbf9cf2c18d7a3c900a06b0c3e` adds scoped,
context-aware complete-file range delivery without exposing paths or file
descriptors. Every request verifies exact size, the full SHA256 BlobID and the
TAF envelope on the same descriptor before emitting bytes, then checks identity
again after delivery. Missing and corrupt media remain distinct typed results.

Ranges use checked half-open arithmetic, permit an empty range at EOF and have
validated 8 MiB/60 second defaults with 64 MiB/120 second hard ceilings. Tests
cover exact and one-over limits, missing/corrupt/same-size-mutated content,
short I/O, cancellation, blocked sinks and descriptor confinement.

Parent focused repetitions, full backend tests, vet, formatting and whitespace
checks passed. [Next CI run 37202334719](https://github.com/shentschel/teddycloud/actions/runs/37202334719)
passed all jobs including Linux race, filesystem qualification and repetition.
No HTTP range semantics, availability cache, inventory, quarantine, production
deployment or playback claim is included.
