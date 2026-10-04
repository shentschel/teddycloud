# PI-07/A2 qualified content filesystem checkpoint

Status: accepted after local parent review and remote Linux qualification.
PI-07/A is complete; PI-07/B remains open.

Code commit `7e86d2bfaa3b6806f327471ed915b59655fea3ff` adds the
Linux-only immutable content store behind descriptor-relative operations. It
admits only an owner-controlled ext4 or XFS root after real `openat2`,
`renameat2(RENAME_NOREPLACE)`, lock and flush probes; holds an exclusive owner
lock; bounds staging capacity and input reads; validates exact TAF bytes before
publication; and verifies an existing immutable destination before reuse.
Unsupported platforms fail closed.

Parent verification passed the full backend suite, vet, ten focused ContentFS
repetitions, formatting, whitespace checks and a Darwin cross-compile. The
worker additionally passed the focused race test with a temporary Linux C
toolchain. [Next CI run 37199532054](https://github.com/shentschel/teddycloud/actions/runs/37199532054)
passed all jobs, including qualified ext4/XFS execution, repetition, race and
the non-Linux build boundary.

Failures retain staging evidence and expose only typed, sanitized errors. Until
PI-07/B2 supplies bounded reconciliation, startup rejects retained staging or
quarantine entries instead of guessing their state. This checkpoint does not
claim physical power-loss durability, playable audio, production deployment,
database binding, migration or garbage collection.
