# PI-08/B2 media compatibility checkpoint

Status: **bounded software checkpoint accepted**.

Commit [`8a95c5b9`](https://github.com/shentschel/teddycloud/commit/8a95c5b944b8da8177c2b95519d500e49898dffd)
implements the media-compatibility portion of PI-08/B2. A verified generated
TAF can attach to an existing catalog-only version with the same immutable
version tuple. Changed content, audio, hash or order tuples fail closed, and
damaged receipt/binding relations are never silently repaired.

`TestCatalogMediaAttachment` and `TestCatalogReceiptAfterCorrection` prove the
required attachment, typed unbound/missing/corrupt/unavailable distinctions,
atomic rollback and unchanged PI-07 receipt replay after later catalog review
and order corrections. Receipt bytes, version facts and the domain Assignment
reference remain unchanged. Persistent Assignment recovery is not claimed
before PI-09.

Parent verification with checksum-verified Go 1.27.1 passed the named tests ten
times, the relevant package suite, full backend tests, vet and whitespace
checks. [Next CI 39490008310](https://github.com/shentschel/teddycloud/actions/runs/39490008310)
completed all four exact-head jobs successfully, including focused SQLite
repetition, race evidence, qualified Linux blob-filesystem tests and
non-Linux fail-closed compilation.

This accepts only the B2 media/receipt slice. Lifecycle fencing, uncertain
commit, helper restart and sanitized-error evidence remain open, as do
independent T and the PI-08 milestone.
