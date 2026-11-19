package sqlite

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	ev "github.com/shentschel/teddycloud/next/backend/internal/application/eventhub"
)

func eventCursor(s *eventState, scope, epoch string, sequence uint64) string {
	raw := []byte{1}
	tenant, _ := hex.DecodeString(s.tenant)
	raw = append(raw, tenant...)
	box := make([]byte, 16)
	kind := byte(0)
	if scope != "control" {
		kind = 1
		box, _ = hex.DecodeString(scope)
	}
	raw = append(raw, kind)
	raw = append(raw, box...)
	ep, _ := hex.DecodeString(epoch)
	raw = append(raw, ep...)
	raw = binary.BigEndian.AppendUint64(raw, sequence)
	raw = binary.BigEndian.AppendUint64(raw, s.generation)
	raw = append(raw, 1)
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte("pi11-cursor/1"))
	mac.Write(raw)
	raw = append(raw, mac.Sum(nil)...)
	return "ec1." + base64.RawURLEncoding.EncodeToString(raw)
}
func decodeEventCursor(s *eventState, token, scope string) (epoch string, n uint64, err error) {
	if len(token) != 136 || !strings.HasPrefix(token, "ec1.") {
		return "", 0, ev.ErrInvalidCursor
	}
	raw, e := base64.RawURLEncoding.Strict().DecodeString(token[4:])
	if e != nil || len(raw) != 99 || "ec1."+base64.RawURLEncoding.EncodeToString(raw) != token {
		return "", 0, ev.ErrInvalidCursor
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte("pi11-cursor/1"))
	mac.Write(raw[:67])
	if !hmac.Equal(mac.Sum(nil), raw[67:]) {
		return "", 0, ev.ErrInvalidCursor
	}
	kind := raw[17]
	box := hex.EncodeToString(raw[18:34])
	tenant := hex.EncodeToString(raw[1:17])
	epoch = hex.EncodeToString(raw[34:50])
	n = binary.BigEndian.Uint64(raw[50:58])
	generation := binary.BigEndian.Uint64(raw[58:66])
	if raw[0] != 1 || raw[66] != 1 || kind > 1 || !ev.ValidOpaqueID(tenant) || !ev.ValidOpaqueID(epoch) || n > 1<<63-1 || generation == 0 || generation > 1<<63-1 || kind == 0 && box != strings.Repeat("0", 32) || kind == 1 && !ev.ValidOpaqueID(box) {
		return "", 0, ev.ErrInvalidCursor
	}
	if tenant != s.tenant || scope == "control" && kind != 0 || scope != "control" && (kind != 1 || scope != box) {
		return "", 0, ev.ErrInvalidCursor
	}
	if generation != s.generation {
		return "", 0, ev.ErrReconcileRequired
	}
	return epoch, n, nil
}
func (o *LifecycleOwner) Replay(ctx context.Context, cap ev.ReadCapability, token string, limit int) (page ev.ReplayPage, err error) {
	bounded, finish, boundErr := eventOperationContext(ctx)
	if boundErr != nil {
		return page, boundErr
	}
	defer finish()
	ctx = bounded

	if err = cap.Authorize(); err != nil {
		return
	}
	if limit < 0 || limit > ev.MaxTurnEvents {
		return page, ev.ErrCapacityExceeded
	}
	if o == nil || o.gate == nil {
		return page, ev.ErrUnavailable
	}
	if err = o.gate.enterOperation(ctx); err != nil {
		return page, ev.ErrUnavailable
	}
	defer o.gate.leave()
	db := o.database
	if db == nil || db.events == nil {
		return page, ev.ErrUnsupported
	}
	s := db.events
	if !s.authority.OwnsRead(cap) {
		return page, ev.ErrRevoked
	}
	tenant, kind, box, e := cap.Scope()
	if e != nil {
		return page, e
	}
	if tenant != s.tenant {
		return page, ev.ErrRevoked
	}
	if s.fenced.Load() {
		return page, ev.ErrUnavailable
	}
	scope := "control"
	if kind == "box" {
		scope = box
	}
	if err = db.withinSQLTransaction(ctx, func(tx *sql.Tx) error { return eventPruneAge(ctx, tx, time.Now().UTC().Format(ev.TimestampLayout)) }); err != nil {
		return page, ev.ErrUnavailable
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return page, ev.ErrUnavailable
	}
	defer tx.Rollback()
	var epoch string
	var high, floor uint64
	err = tx.QueryRowContext(ctx, "SELECT epoch,high_water,floor FROM tc_event_streams WHERE scope=?", scope).Scan(&epoch, &high, &floor)
	if err == sql.ErrNoRows {
		return page, ev.ErrReconcileRequired
	}
	if err != nil {
		return page, ev.ErrUnavailable
	}
	n := high
	if token != "" {
		var ep string
		ep, n, err = decodeEventCursor(s, token, scope)
		if err != nil {
			return page, err
		}
		if ep != epoch || n < floor || n > high {
			return page, ev.ErrReconcileRequired
		}
	}
	var count uint64
	var min, max sql.NullInt64
	if err = tx.QueryRowContext(ctx, "SELECT count(*),min(sequence),max(sequence) FROM tc_event_journal WHERE scope=?", scope).Scan(&count, &min, &max); err != nil {
		return page, ev.ErrUnavailable
	}
	if count != high-floor || count > 0 && (!min.Valid || uint64(min.Int64) != floor+1 || !max.Valid || uint64(max.Int64) != high) {
		s.fenced.Store(true)
		return page, ev.ErrUnavailable
	}
	page.Barrier = eventCursor(s, scope, epoch, high)
	page.Complete = n == high
	rows, err := tx.QueryContext(ctx, "SELECT sequence,CASE WHEN length(envelope)<=8192 THEN envelope END FROM tc_event_journal WHERE scope=? AND sequence>? ORDER BY sequence LIMIT ?", scope, n, limit)
	if err != nil {
		return ev.ReplayPage{}, ev.ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var seq uint64
		var raw []byte
		if err = rows.Scan(&seq, &raw); err != nil {
			break
		}
		envelope, de := ev.Decode(raw)
		if de != nil || seq != n+1 || envelope.Stream.Epoch != epoch || envelope.Stream.TenantID != tenant || envelope.Source.InstanceID != s.instance || envelope.Stream.Kind != kind || kind == "box" && (envelope.Stream.BoxID == nil || *envelope.Stream.BoxID != box) || envelope.Sequence != strconv.FormatUint(seq, 10) {
			s.fenced.Store(true)
			return ev.ReplayPage{}, ev.ErrUnavailable
		}
		n = seq
		page.Records = append(page.Records, ev.Record{Type: envelope.Type, Bytes: raw, Cursor: eventCursor(s, scope, epoch, n)})
	}
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		return ev.ReplayPage{}, ev.ErrUnavailable
	}
	page.Complete = n == high
	if err = cap.Authorize(); err != nil {
		return ev.ReplayPage{}, err
	}
	return page, nil
}
