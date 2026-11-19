package eventhub

import (
	"context"
	"time"
)

// OperationTimeout bounds new event-port work without extending inherited deadlines.
const OperationTimeout = 5 * time.Second

// Record owns canonical bytes and a checkpoint for this complete record.
type Record struct {
	Type   Type
	Bytes  []byte
	Cursor string
}
type ReplayPage struct {
	Records  []Record
	Barrier  string
	Complete bool
}
type Journal interface {
	Replay(context.Context, ReadCapability, string, int) (ReplayPage, error)
}
