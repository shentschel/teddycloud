# PI-07/B1 transactional content metadata checkpoint

Status: accepted; PI-07/B remains open for B2 filesystem reconciliation and
verified reads.

Code commit `c8eb4d68bd9d883c04c12371c974d36766f355e0` adds additive
migration 4 (`0004-content-blobs`), callback-scoped content-operation ownership
and atomic DB-only import metadata. Migrations 1..3 and their ledger checksums
remain unchanged. Blob, immutable ContentVersion, version binding and import
receipt are committed together after existing Content validation.

Exact command replay returns the recorded result; changed commands, version
facts or bindings conflict. Reads reconstruct domain values and reject corrupt
or dangling rows. Ambiguous commit and lost-response tests require readback and
never retry a transaction callback automatically. Lookup results describe only
committed metadata, not current media availability.

The existing lifecycle gate fences content operations against restore and
close. Sessions and repositories are generation-scoped, revoked on callback
exit and cannot re-enter Catalog, Tag or content admission. SQL transactions
remain short and contain no filesystem I/O.

Parent verification passed the full backend suite, vet, ten focused SQLite
repetitions, formatting and whitespace checks. The delegated worker also passed
focused race tests. [Next CI run 37201527034](https://github.com/shentschel/teddycloud/actions/runs/37201527034)
passed all jobs, including persistence regression and race evidence.

B2 still owns bounded inventory, verified ranges, quarantine and process-kill
recovery across the filesystem/DB boundary. This checkpoint does not claim
present media, playback, production migration or deployment.
