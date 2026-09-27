# PI-06/A1 history-count checkpoint

Status: count-only domain preflight; complete aggregate acceptance remains open.

## Scope and guarantees

This checkpoint adds a composable, allocation-free structural preflight over
actual retained observation slices and actual per-decision support slices.
It checks at most 4096 observations, 4096 retained decisions, 4096 support
references per decision and 16384 cumulative references across the complete
retained history. The command preflight separately limits new observations to
64. Repeated references in different historical decisions still consume one
slot per link; no deduplication, truncation or history pruning is performed.

Length checks and checked cumulative arithmetic run before any aggregate copy.
The gate does not scan or validate observation identities, payloads, support
membership or state transitions. Empty or invalid members passing a structural
count check are not evidence of a semantically valid aggregate. Existing
evidence validation remains unchanged. Errors contain no caller identifiers.

## Verification and limitations

Parent full backend tests/vet, 25 focused Tag/application/architecture repetitions
and all 58 architecture-document checks passed. Exact limits, one-over limits,
cumulative overflow despite individually bounded support lists, repeated
historical links and zero-allocation tests passed. Native-int overflow is tested
without enormous allocation. Parent independently compiled the Tag tests for
386; 32-bit execution is not claimed.
[Next CI](https://github.com/shentschel/teddycloud/actions/runs/36345279370)
and [documentation CI](https://github.com/shentschel/teddycloud/actions/runs/36345279428)
passed for the implementation, including all four Next jobs and Linux race.

This checkpoint is not integrated into registry mutations or a SQLite loader:
neither exists for retained metadata yet. B1 and storage integration must invoke
preflight on complete retained plus proposed history before copying or joining.
Adapter preflight of persisted counts and corrupt-state error mapping is pending.

The complete 8 MiB encoded aggregate remains unimplemented and unaccepted.
Before that work, define the actual internal representation and account for
identity, revision, state, all retained history, escaping and encoding overhead.
No caller-supplied byte count or empty identity aggregate proves this bound.
Re-refine and route to Astra before implementation if representation choice
requires an architectural decision. A1 as a whole, A2, B and T remain open.

No schema, SQL, public wire contract, runtime endpoint, cloud access, credential
handling, production data or deployment is changed.
