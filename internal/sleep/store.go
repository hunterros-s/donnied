package sleep

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const (
	metaLastAttempt = "last_attempt_ms"
	metaLastSuccess = "last_success_ms"
	metaLastSource  = "last_source"
	metaLastError   = "last_error"
)

// Store persists normalized sleep data in SQLite.
type Store struct {
	db *sql.DB
}

// NewStore opens or creates the SQLite sleep store at path.
func NewStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.init(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) init(ctx context.Context) error {
	stmts := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS sleep_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sleep_observations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			observed_ms INTEGER NOT NULL,
			device_addr TEXT NOT NULL,
			source TEXT NOT NULL,
			session_count INTEGER NOT NULL,
			raw_json TEXT NOT NULL,
			error TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS sleep_sessions (
			id TEXT PRIMARY KEY,
			device_addr TEXT NOT NULL,
			start_ms INTEGER NOT NULL,
			end_ms INTEGER NOT NULL,
			total_minutes INTEGER NOT NULL,
			source TEXT NOT NULL,
			first_seen_ms INTEGER NOT NULL,
			last_seen_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sleep_sessions_time ON sleep_sessions(start_ms, end_ms)`,
		`CREATE INDEX IF NOT EXISTS idx_sleep_sessions_device_time ON sleep_sessions(device_addr, start_ms, end_ms)`,
		`CREATE TABLE IF NOT EXISTS sleep_segments (
			session_id TEXT NOT NULL REFERENCES sleep_sessions(id) ON DELETE CASCADE,
			segment_index INTEGER NOT NULL,
			start_ms INTEGER NOT NULL,
			end_ms INTEGER NOT NULL,
			minutes INTEGER NOT NULL,
			stage_raw INTEGER NOT NULL,
			stage_name TEXT NOT NULL,
			state TEXT NOT NULL,
			source TEXT NOT NULL,
			PRIMARY KEY(session_id, segment_index)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sleep_segments_time ON sleep_segments(start_ms, end_ms)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// SaveObservation stores a raw watch observation plus normalized session rows.
func (s *Store) SaveObservation(ctx context.Context, deviceAddr string, source Source, observedAt time.Time, raw []ObservedSession, sessions []Session) error {
	rawJSON, err := json.Marshal(raw)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	observedMS := ms(observedAt)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sleep_observations(observed_ms, device_addr, source, session_count, raw_json) VALUES (?, ?, ?, ?, ?)`,
		observedMS, deviceAddr, string(source), len(raw), string(rawJSON)); err != nil {
		return err
	}

	for _, sess := range sessions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sleep_sessions(
			id, device_addr, start_ms, end_ms, total_minutes, source, first_seen_ms, last_seen_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			device_addr=excluded.device_addr,
			start_ms=excluded.start_ms,
			end_ms=excluded.end_ms,
			total_minutes=excluded.total_minutes,
			source=excluded.source,
			last_seen_ms=excluded.last_seen_ms`,
			sess.ID, sess.DeviceAddr, ms(sess.Start), ms(sess.End), sess.TotalMinutes, string(sess.Source), ms(sess.FirstSeen), ms(sess.LastSeen)); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM sleep_segments WHERE session_id = ?`, sess.ID); err != nil {
			return err
		}
		for _, seg := range sess.Segments {
			if _, err := tx.ExecContext(ctx, `INSERT INTO sleep_segments(
				session_id, segment_index, start_ms, end_ms, minutes, stage_raw, stage_name, state, source
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				sess.ID, seg.Index, ms(seg.Start), ms(seg.End), seg.Minutes, seg.StageRaw, seg.StageName, string(seg.State), string(seg.Source)); err != nil {
				return err
			}
		}
	}

	if err := setMeta(ctx, tx, metaLastAttempt, fmt.Sprintf("%d", observedMS)); err != nil {
		return err
	}
	if err := setMeta(ctx, tx, metaLastSuccess, fmt.Sprintf("%d", observedMS)); err != nil {
		return err
	}
	if err := setMeta(ctx, tx, metaLastSource, string(source)); err != nil {
		return err
	}
	if err := setMeta(ctx, tx, metaLastError, ""); err != nil {
		return err
	}

	return tx.Commit()
}

// RecordSyncError records a failed poll attempt without changing last success.
func (s *Store) RecordSyncError(ctx context.Context, observedAt time.Time, err error) error {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	tx, txErr := s.db.BeginTx(ctx, nil)
	if txErr != nil {
		return txErr
	}
	defer tx.Rollback()
	if txErr := setMeta(ctx, tx, metaLastAttempt, fmt.Sprintf("%d", ms(observedAt))); txErr != nil {
		return txErr
	}
	if txErr := setMeta(ctx, tx, metaLastError, msg); txErr != nil {
		return txErr
	}
	return tx.Commit()
}

// Sessions returns persisted sessions that overlap the optional time range.
func (s *Store) Sessions(ctx context.Context, from, to time.Time) ([]Session, error) {
	fromMS, toMS := int64(0), int64(0)
	if !from.IsZero() {
		fromMS = ms(from)
	}
	if !to.IsZero() {
		toMS = ms(to)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id, device_addr, start_ms, end_ms, total_minutes, source, first_seen_ms, last_seen_ms
		FROM sleep_sessions
		WHERE (? = 0 OR end_ms >= ?) AND (? = 0 OR start_ms <= ?)
		ORDER BY start_ms DESC`, fromMS, fromMS, toMS, toMS)
	if err != nil {
		return nil, err
	}

	var sessions []Session
	for rows.Next() {
		var sess Session
		var startMS, endMS, firstMS, lastMS int64
		var source string
		if err := rows.Scan(&sess.ID, &sess.DeviceAddr, &startMS, &endMS, &sess.TotalMinutes, &source, &firstMS, &lastMS); err != nil {
			rows.Close()
			return nil, err
		}
		sess.Start = fromMSValue(startMS)
		sess.End = fromMSValue(endMS)
		sess.Source = Source(source)
		sess.FirstSeen = fromMSValue(firstMS)
		sess.LastSeen = fromMSValue(lastMS)
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// Load segments after closing the session cursor. The store intentionally
	// uses one SQLite connection, so nested queries while rows is still open will
	// block waiting for that same connection.
	for i := range sessions {
		segments, err := s.segments(ctx, sessions[i].ID)
		if err != nil {
			return nil, err
		}
		sessions[i].Segments = segments
	}
	return sessions, nil
}

func (s *Store) segments(ctx context.Context, sessionID string) ([]Segment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT segment_index, start_ms, end_ms, minutes, stage_raw, stage_name, state, source
		FROM sleep_segments WHERE session_id = ? ORDER BY segment_index`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Segment
	for rows.Next() {
		var seg Segment
		var startMS, endMS int64
		var state, source string
		if err := rows.Scan(&seg.Index, &startMS, &endMS, &seg.Minutes, &seg.StageRaw, &seg.StageName, &state, &source); err != nil {
			return nil, err
		}
		seg.Start = fromMSValue(startMS)
		seg.End = fromMSValue(endMS)
		seg.State = State(state)
		seg.Source = Source(source)
		out = append(out, seg)
	}
	return out, rows.Err()
}

// Status returns a high-level current awake/asleep state. Recent successful
// sync with no segment covering now is interpreted as awake; stale or missing
// data is unknown.
func (s *Store) Status(ctx context.Context, now time.Time, staleAfter time.Duration) (*Status, error) {
	lastSync, ok, err := s.metaTime(ctx, metaLastSuccess)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &Status{State: StateUnknown, Stale: true}, nil
	}
	lastSource, _, err := s.metaString(ctx, metaLastSource)
	if err != nil {
		return nil, err
	}
	status := &Status{State: StateUnknown, LastSync: &lastSync, Source: Source(lastSource)}
	if now.Sub(lastSync) > staleAfter {
		status.Stale = true
		return status, nil
	}

	// Realtime/current sleep frames commonly end at the current minute. Allow a
	// small grace window so seconds-level clock drift does not flip asleep→awake.
	graceMS := int64((2 * time.Minute) / time.Millisecond)
	nowMS := ms(now)
	row := s.db.QueryRowContext(ctx, `SELECT start_ms, end_ms, stage_raw, stage_name, state, source
		FROM sleep_segments
		WHERE start_ms <= ? AND end_ms >= ?
		ORDER BY end_ms DESC LIMIT 1`, nowMS, nowMS-graceMS)
	var startMS, endMS int64
	var stageRaw int
	var stageName, state, source string
	if err := row.Scan(&startMS, &endMS, &stageRaw, &stageName, &state, &source); err == nil {
		since := fromMSValue(startMS)
		return &Status{
			State:    State(state),
			Stage:    stageName,
			StageRaw: stageRaw,
			Since:    &since,
			LastSync: &lastSync,
			Source:   Source(source),
			Stale:    false,
		}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	status.State = StateAwake
	status.Stale = false
	latestEnd, ok, err := s.latestSessionEnd(ctx, now)
	if err != nil {
		return nil, err
	}
	if ok {
		status.Since = &latestEnd
	}
	return status, nil
}

func (s *Store) latestSessionEnd(ctx context.Context, now time.Time) (time.Time, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT end_ms FROM sleep_sessions WHERE end_ms <= ? ORDER BY end_ms DESC LIMIT 1`, ms(now))
	var endMS int64
	if err := row.Scan(&endMS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, err
	}
	return fromMSValue(endMS), true, nil
}

func (s *Store) metaTime(ctx context.Context, key string) (time.Time, bool, error) {
	v, ok, err := s.metaString(ctx, key)
	if err != nil || !ok {
		return time.Time{}, ok, err
	}
	var milli int64
	if _, err := fmt.Sscanf(v, "%d", &milli); err != nil {
		return time.Time{}, false, err
	}
	return fromMSValue(milli), true, nil
}

func (s *Store) metaString(ctx context.Context, key string) (string, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT value FROM sleep_meta WHERE key = ?`, key)
	var v string
	if err := row.Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return v, true, nil
}

func setMeta(ctx context.Context, tx *sql.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO sleep_meta(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func ms(t time.Time) int64 {
	return t.UTC().UnixMilli()
}

func fromMSValue(v int64) time.Time {
	return time.UnixMilli(v).Local()
}
