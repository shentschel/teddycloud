package eventhub

// Diagnostics uses counters only, without entity or cursor labels.
type Diagnostics struct {
	Appends, AppendFailures, Replayed, PrunedRows, PrunedBytes                                                          uint64
	ActiveSubscribers, QueuedBytes, SlowDisconnects, Gaps, DeniedAdmissions, ReconcileFailures, UnfinishedShutdownTasks uint64
}
