package sqlite

import (
	"context"
	"database/sql"
	"sort"
	"sync"

	ev "github.com/shentschel/teddycloud/next/backend/internal/application/eventhub"
)

type eventFact struct {
	owner                  ev.Owner
	typ                    ev.Type
	kind, id               string
	revision               *string
	payload                ev.Payload
	box                    string
	occurred               *string
	causation, correlation *string
	sourceEpoch            string
	sourceSequence         uint64
}
type eventCollector struct {
	appended, prunedRows, prunedBytes uint64
	database                          *Database
	facts                             []eventFact
	failure                           error
}

var eventTransactions sync.Map // *sql.Tx -> *eventCollector, removed before finalization.
func eventBegin(db *Database, tx *sql.Tx) (*eventCollector, error) {
	if db.events == nil {
		return nil, nil
	}

	c := &eventCollector{database: db}
	eventTransactions.Store(tx, c)
	return c, nil
}
func eventEnd(tx *sql.Tx) { eventTransactions.Delete(tx) }
func recordEvent(tx *sql.Tx, f eventFact) error {
	value, ok := eventTransactions.Load(tx)
	if !ok {
		return nil
	}
	c := value.(*eventCollector)
	if c.failure != nil {
		return c.failure
	}
	// Catalog transaction mutations on the same Content collapse into one fact.
	for i, old := range c.facts {
		if old.owner != f.owner || old.typ != f.typ || old.kind != f.kind || old.id != f.id || old.box != f.box {
			continue
		}
		if f.typ == ev.CatalogChanged {
			p, ok := f.payload.(ev.CatalogPayload)
			if !ok {
				c.failure = ev.ErrInvalidEvent
				return c.failure
			}
			p.Change = "multiple"
			f.payload = p
			c.facts[i] = f
			return nil
		}
		if f.typ == ev.StorageInvalidated || f.typ == ev.CatalogInvalidated || f.typ == ev.BoxInvalidated {
			a, ok := old.payload.(ev.InvalidationPayload)
			b, other := f.payload.(ev.InvalidationPayload)
			if ok && other && a == b {
				return nil
			}
		}
	}
	if len(c.facts) >= ev.MaxTransactionEvents {
		c.failure = ev.ErrCapacityExceeded
		return c.failure
	}
	c.facts = append(c.facts, f)
	return nil
}
func (c *eventCollector) flush(ctx context.Context, tx *sql.Tx) error {
	if c == nil {
		return nil
	}
	if c.failure != nil {
		return c.failure
	}
	sort.SliceStable(c.facts, func(i, j int) bool {
		a, b := c.facts[i], c.facts[j]
		return string(a.owner)+"\x00"+string(a.typ)+"\x00"+a.kind+"\x00"+a.id < string(b.owner)+"\x00"+string(b.typ)+"\x00"+b.kind+"\x00"+b.id
	})
	if err := c.database.appendEvents(ctx, tx, c.facts); err != nil {
		return err
	}
	c.facts = nil
	return nil
}

func catalogEvent(tx *sql.Tx, id string, revision uint64, change string) error {
	return recordEvent(tx, eventFact{owner: ev.Catalog, typ: ev.CatalogChanged, kind: "content", id: id, revision: eventRevision(revision), payload: ev.CatalogPayload{ContentID: id, Revision: eventRevision(revision), Change: change}})
}
func catalogScopeEvent(tx *sql.Tx, reason string) error {
	value, ok := eventTransactions.Load(tx)
	if !ok {
		return nil
	}
	c := value.(*eventCollector)
	return recordEvent(tx, eventFact{owner: ev.Catalog, typ: ev.CatalogInvalidated, kind: "catalog-scope", id: c.database.events.tenant, payload: ev.InvalidationPayload{Reason: reason}})
}

func (c *eventCollector) committed() {
	if c == nil {
		return
	}
	s := c.database.events
	s.appends.Add(c.appended)
	s.prunedRows.Add(c.prunedRows)
	s.prunedBytes.Add(c.prunedBytes)
}
func eventPruned(tx *sql.Tx, rows, bytes uint64) {
	if value, ok := eventTransactions.Load(tx); ok {
		c := value.(*eventCollector)
		c.prunedRows += rows
		c.prunedBytes += bytes
	}
}
