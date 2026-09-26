# PI-05/B restart and bounded-access checkpoint

Status: **Test checkpoint; PI-05/B remains open**

A subprocess opens a temporary database, replaces a committed Content value
inside an uncommitted transaction, then signals readiness. The parent terminates
only that subprocess, waits for it, and reopens the database. The original
committed value, schema version and migration ledger must remain unchanged.
Helper startup and shutdown have bounded failure paths and test cleanup.
The readiness-pipe fixture is Unix-only; Windows restart evidence and physical
power-loss/hardware durability remain unproven.

The single connection bounds both write and read transactions. Additional
operations wait rather than observing uncommitted writes. A controlled deadline
ends a queued operation without entering its callback, and the pool remains
usable after the holder commits or rolls back. Existing isolation tests plus
these wait tests prove serialized bounded access, not a parallel reader pool.

Independent verification requires 25 repetitions of restart/pool tests, the
complete backend test suite and vet, formatting, architecture-document and diff
checks, plus successful remote CI. No production data or process is touched.

Remaining B acceptance: verified pre-upgrade backup failure must fence schema
mutation, and coordinated closed/fenced restore to the active path. T retains
failure hardening, error-boundary audit and final milestone evidence.
