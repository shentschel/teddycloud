# PI-08/A2.5b catalog-state read checkpoint

Status: accepted read-side adapter checkpoint; PI-08/A2 and the PI milestone remain incomplete.

## Accepted implementation

- Implementation: `f77185213626c0abf38b36028e385964fd4c57aa`
- Prerequisite schema: `bb35810d79c42c035c8e59f15b59b3b986975e82`

The v6 SQLite adapter now loads bounded Review and Order states and looks up
their exact retained operation results. It verifies the declared byte length and
SHA-256 before decoding, rehydrates through the accepted domain contract using
detached persistence bindings, and cross-checks subjects, content, revisions,
sources, evidence, decisions, support and order-version manifests against the
normalized relational state. Corruption and incomplete relations fail closed;
clean absence remains an explicit miss.

Existing catalog queries remain available on v5 and v6. State reads require v6.
Review and order commit methods remain unsupported, so this checkpoint adds no
mutation path.

## Verification

Digest-pinned Go 1.27.1 Docker checks passed the complete SQLite/catalog/
architecture suites and ten repeated catalog runs. Next CI run 38835217458
passed all four required jobs, including the complete persistence/domain race
and focused SQLite repetition evidence.

## Deferred

Review/order commit transactions, revision-race mutation evidence, recovery and
the remaining A2 acceptance matrix are still open. The retained-state parser is
internal to the SQLite adapter and must continue to track the versioned domain
encoding through explicit tests.
