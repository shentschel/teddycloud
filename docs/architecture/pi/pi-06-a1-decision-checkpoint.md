# PI-06/A1 decision identity checkpoint

Status: internal codec implemented; aggregate-limit acceptance remains open.

The Tag domain now has a distinct immutable DecisionID with the fixed internal
`dec_` prefix and the existing 26-character opaque-ID alphabet. Parsing checks
length before slicing or validating; it does not trim, normalize, derive identity
from UID or allocate identifiers. Errors never echo the input. The empty Go value
is absent; a valid all-zero-character opaque payload is still a real identifier.

This is a value codec only, not a decision command, resolution record, public
wire schema or authenticated operation. It does not prove cumulative-support,
history or encoded-aggregate bounds. Existing identity registration and other
opaque-ID codecs remain unchanged.

Parent focused codec/application/architecture tests passed 25 repetitions.
Full backend tests/vet, docs (57 files), format/diff checks passed. Publication
acceptance still requires actual green remote CI for this commit. Existing Next CI
covers domain/application race evidence and deterministic/reproducible artifacts.
No production state or deployment is modified.

## Remaining A1

Implement real aggregate preflight and retained-history limits with checked size
accounting before copying/decoding. Establish the bounded internal representation
and its actual overhead/escaping; do not claim compliance from a caller-provided
byte count or an empty Tag. Keep the fixed 16384 support links and 8 MiB limits,
and prove exact-boundary/one-over/overflow/worst-case behavior. If the representation
requires a new architectural decision, refine it under the routing policy before
implementation; no silent public-wire or B1 state-machine expansion is admitted.
