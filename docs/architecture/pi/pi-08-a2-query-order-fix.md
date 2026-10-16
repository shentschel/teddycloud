# PI-08/A2 finding closeout: query after Order writes

Status: PI08-A2-F01 fixed and accepted; PI-08/A2 and the PI milestone remain incomplete.

## Finding

Catalog queries validated the shared aggregate revision only against Review
decision history. A valid Order commit advances the same aggregate without
creating a Review decision, so subsequent content/version queries incorrectly
failed as repository corruption.

## Accepted fix

- Implementation: `e306f4c5111632b309ac2ed3f16c7d21839508d2`

For v6 managed catalog data, the query adapter now validates the combined,
contiguous aggregate history through the bounded Order envelopes before
projecting Review fields. Review decisions may therefore contain legitimate
aggregate gaps occupied by verified Order commits. v5 behavior is unchanged.

Regression coverage proves content, version and bounded list queries after
multiple Order commits, stale cursor rejection, exact replay, mixed Review/Order
history and fail-closed gap, duplicate and digest corruption.

## Verification

Digest-pinned Go 1.27.1 Docker checks passed the focused regression, ten
repetitions, the relevant SQLite/catalog/architecture suite and its race-enabled
run. Next CI run 38907480637 passed all four required jobs.

The separate intermittent cancellation-test failure remains open.
