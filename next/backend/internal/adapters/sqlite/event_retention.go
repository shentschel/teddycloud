package sqlite

import (
	"context"
	"database/sql"
	ev "github.com/shentschel/teddycloud/next/backend/internal/application/eventhub"
	"time"
)

func eventEvict(ctx context.Context, tx *sql.Tx, scope string) error {
	var n uint64
	var size int
	if err := tx.QueryRowContext(ctx, "SELECT sequence,length(envelope) FROM tc_event_journal WHERE scope=? ORDER BY sequence LIMIT 1", scope).Scan(&n, &size); err != nil {
		return ev.ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM tc_event_journal WHERE scope=? AND sequence=?", scope, n); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE tc_event_streams SET floor=?,retained_rows=retained_rows-1,retained_bytes=retained_bytes-? WHERE scope=? AND floor=?", n, size, scope, n-1)
	if err == nil {
		eventPruned(tx, 1, uint64(size))
	}
	return err
}
func (db *Database) eventRoom(ctx context.Context, tx *sql.Tx, scope string, size int, label string) error {
	if err := eventPruneAge(ctx, tx, label); err != nil {
		return err
	}
	for {
		var rows, bytes int
		if err := tx.QueryRowContext(ctx, "SELECT retained_rows,retained_bytes FROM tc_event_streams WHERE scope=?", scope).Scan(&rows, &bytes); err != nil {
			return err
		}
		if rows+1 <= ev.MaxStreamRows && bytes+size <= ev.MaxStreamBytes {
			break
		}
		if err := eventEvict(ctx, tx, scope); err != nil {
			return err
		}
	}
	for {
		var rows, bytes int
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(sum(retained_rows),0),COALESCE(sum(retained_bytes),0) FROM tc_event_streams").Scan(&rows, &bytes); err != nil {
			return err
		}
		if rows+1 <= ev.MaxOwnerRows && bytes+size <= ev.MaxOwnerBytes {
			return nil
		}
		var largest string
		if err := tx.QueryRowContext(ctx, "SELECT scope FROM tc_event_streams WHERE retained_rows>0 ORDER BY retained_bytes DESC,epoch LIMIT 1").Scan(&largest); err != nil {
			return err
		}
		if err := eventEvict(ctx, tx, largest); err != nil {
			return err
		}
	}
}
func eventPruneAge(ctx context.Context, tx *sql.Tx, label string) error {
	now, err := time.Parse(ev.TimestampLayout, label)
	if err != nil {
		return ev.ErrInvalidEvent
	}
	cutoff := now.Add(-24 * time.Hour).Format(ev.TimestampLayout)
	rows, err := tx.QueryContext(ctx, "SELECT scope FROM tc_event_streams ORDER BY scope LIMIT 66")
	if err != nil {
		return err
	}
	var scopes []string
	for rows.Next() {
		var scope string
		if err = rows.Scan(&scope); err != nil {
			break
		}
		scopes = append(scopes, scope)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil || len(scopes) > 65 {
		return ev.ErrUnavailable
	}
	for _, scope := range scopes {
		var floor, high uint64
		if err = tx.QueryRowContext(ctx, "SELECT floor,high_water FROM tc_event_streams WHERE scope=?", scope).Scan(&floor, &high); err != nil {
			return err
		}
		var first sql.NullInt64
		if err = tx.QueryRowContext(ctx, "SELECT min(sequence) FROM tc_event_journal WHERE scope=? AND committed_at>=?", scope, cutoff).Scan(&first); err != nil {
			return err
		}
		through := high
		if first.Valid {
			through = uint64(first.Int64) - 1
		}
		if through <= floor {
			continue
		}
		var n, bytes uint64
		if err = tx.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(length(envelope)),0) FROM tc_event_journal WHERE scope=? AND sequence<=?", scope, through).Scan(&n, &bytes); err != nil {
			return err
		}
		if n != through-floor {
			return ev.ErrUnavailable
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM tc_event_journal WHERE scope=? AND sequence<=?", scope, through); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE tc_event_streams SET floor=?,retained_rows=retained_rows-?,retained_bytes=retained_bytes-? WHERE scope=?", through, n, bytes, scope); err != nil {
			return err
		}
		eventPruned(tx, n, bytes)
	}
	return nil
}
