# PI-06/A1 identity checkpoint

Status: identity/service checkpoint implemented; full A1 and PI-06/A remain open.

This checkpoint adds an immutable Tag identity and storage-neutral registration/
point-query service. Existing strict UID/rUID codecs and opaque TagID validation
remain authoritative. Registration starts at revision 1 with four independent
unknown facts; it does not infer Original/Custom, claim, ownership or cloud access.

## Behavior and review

- Optional fields distinguish absence, explicit empty text and a valid zero UID.
- UID/rUID are checked and normalized before a transaction starts; mismatch fails.
- ID and UID lookups share one callback. Exact replay preserves the record;
  collisions do not merge, overwrite or advance its revision.
- Point reads use the same callback-scoped port. Invalid or wrong-key storage
  records return unavailable rather than successful zero/incorrect values.
- Storage errors are sanitized; context cancellation and bounded contention
  remain distinguishable. The test double rolls back failed operations.

Independent review corrected found-but-zero/wrong-key results and strengthened
case tests with synthetic hexadecimal identifiers that actually contain letters.
The service is not connected to SQLite or an HTTP endpoint in this checkpoint.
The application Transactor contract requires a single atomic callback; durable
proof of that implementation belongs to A2/B2, not the test double.

## Remaining acceptance

The [internal decision-ID codec](pi-06-a1-decision-checkpoint.md) is a later
separate checkpoint. Full A1 still requires cumulative support and
encoded-aggregate size limits, exact-boundary/one-over/overflow tests and
worst-case representation proof. This checkpoint does not replace those with
caller-supplied size numbers or infer passing bounds from an empty aggregate.
Evidence-history commands and compare-and-swap remain B1 work.
A2 still owns durable Tag storage, unique indexes and the shared lifecycle gate.

## Verification

Parent full backend tests and vet passed; identity/application/architecture
packages passed 25 repetitions, documentation checks covered 56 files, and
formatting/diff checks passed. The local pinned advisory scan found no vulnerable
backend calls. These are application/test-double results, not SQLite Tag proof.
CI extends its focused race job to the Tag domain and application packages.
Local race execution remains unavailable on the current host without a C compiler;
remote Linux evidence must be checked before publication is accepted.
No production migration, deployment or live-card mutation is authorized.
