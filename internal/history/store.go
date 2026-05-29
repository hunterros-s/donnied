package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"smartwatch/protocol"

	_ "modernc.org/sqlite"
)

const (
	metaLastAttempt = "last_attempt_ms"
	metaLastSuccess = "last_success_ms"
	metaLastError   = "last_error"
)

const (
	KindHR    = "hr"
	KindSteps = "steps"
	KindSpO2  = "spo2"
)

// Store persists watch metric history such as HR, steps, and SpO2.
type Store struct {
	db *sql.DB
}

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
		`CREATE TABLE IF NOT EXISTS metric_meta (
			kind TEXT NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			PRIMARY KEY(kind, key)
		)`,
		`CREATE TABLE IF NOT EXISTS metric_observations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			kind TEXT NOT NULL,
			observed_ms INTEGER NOT NULL,
			device_addr TEXT NOT NULL,
			source TEXT NOT NULL,
			day TEXT NOT NULL DEFAULT '',
			record_count INTEGER NOT NULL,
			raw_json TEXT NOT NULL,
			error TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_metric_observations_kind_time ON metric_observations(kind, observed_ms)`,
		`CREATE TABLE IF NOT EXISTS hr_samples (
			device_addr TEXT NOT NULL,
			time_ms INTEGER NOT NULL,
			bpm INTEGER NOT NULL,
			interval_min INTEGER NOT NULL,
			source TEXT NOT NULL,
			first_seen_ms INTEGER NOT NULL,
			last_seen_ms INTEGER NOT NULL,
			PRIMARY KEY(device_addr, time_ms)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hr_samples_time ON hr_samples(time_ms)`,
		`CREATE TABLE IF NOT EXISTS step_buckets (
			device_addr TEXT NOT NULL,
			start_ms INTEGER NOT NULL,
			end_ms INTEGER NOT NULL,
			year INTEGER NOT NULL,
			month INTEGER NOT NULL,
			day INTEGER NOT NULL,
			hour INTEGER NOT NULL,
			minute INTEGER NOT NULL,
			steps INTEGER NOT NULL,
			calories REAL NOT NULL,
			distance INTEGER NOT NULL,
			source TEXT NOT NULL,
			first_seen_ms INTEGER NOT NULL,
			last_seen_ms INTEGER NOT NULL,
			PRIMARY KEY(device_addr, start_ms)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_step_buckets_time ON step_buckets(start_ms, end_ms)`,
		`CREATE TABLE IF NOT EXISTS spo2_samples (
			device_addr TEXT NOT NULL,
			day TEXT NOT NULL,
			sample_index INTEGER NOT NULL,
			min INTEGER NOT NULL,
			max INTEGER NOT NULL,
			source TEXT NOT NULL,
			first_seen_ms INTEGER NOT NULL,
			last_seen_ms INTEGER NOT NULL,
			PRIMARY KEY(device_addr, day, sample_index)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_spo2_samples_day ON spo2_samples(day)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SaveHR(ctx context.Context, deviceAddr string, source Source, observedAt time.Time, samples []protocol.HRSample) error {
	rawJSON, err := json.Marshal(samples)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	observedMS := ms(observedAt)
	if _, err := tx.ExecContext(ctx, `INSERT INTO metric_observations(kind, observed_ms, device_addr, source, record_count, raw_json) VALUES (?, ?, ?, ?, ?, ?)`,
		KindHR, observedMS, deviceAddr, string(source), len(samples), string(rawJSON)); err != nil {
		return err
	}
	for _, sample := range samples {
		if sample.Time.IsZero() || sample.BPM <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO hr_samples(
			device_addr, time_ms, bpm, interval_min, source, first_seen_ms, last_seen_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_addr, time_ms) DO UPDATE SET
			bpm=excluded.bpm,
			interval_min=excluded.interval_min,
			source=excluded.source,
			last_seen_ms=excluded.last_seen_ms`,
			deviceAddr, ms(sample.Time), sample.BPM, sample.Interval, string(source), observedMS, observedMS); err != nil {
			return err
		}
	}
	if err := setMeta(ctx, tx, KindHR, metaLastAttempt, fmt.Sprintf("%d", observedMS)); err != nil {
		return err
	}
	if err := setMeta(ctx, tx, KindHR, metaLastSuccess, fmt.Sprintf("%d", observedMS)); err != nil {
		return err
	}
	if err := setMeta(ctx, tx, KindHR, metaLastError, ""); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SaveSteps(ctx context.Context, deviceAddr string, source Source, observedAt time.Time, details []protocol.SportDetail) error {
	rawJSON, err := json.Marshal(details)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	observedMS := ms(observedAt)
	if _, err := tx.ExecContext(ctx, `INSERT INTO metric_observations(kind, observed_ms, device_addr, source, record_count, raw_json) VALUES (?, ?, ?, ?, ?, ?)`,
		KindSteps, observedMS, deviceAddr, string(source), len(details), string(rawJSON)); err != nil {
		return err
	}
	for _, d := range details {
		start := d.Time()
		if start.IsZero() {
			continue
		}
		end := start.Add(15 * time.Minute)
		if _, err := tx.ExecContext(ctx, `INSERT INTO step_buckets(
			device_addr, start_ms, end_ms, year, month, day, hour, minute, steps, calories, distance, source, first_seen_ms, last_seen_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_addr, start_ms) DO UPDATE SET
			end_ms=excluded.end_ms,
			year=excluded.year,
			month=excluded.month,
			day=excluded.day,
			hour=excluded.hour,
			minute=excluded.minute,
			steps=excluded.steps,
			calories=excluded.calories,
			distance=excluded.distance,
			source=excluded.source,
			last_seen_ms=excluded.last_seen_ms`,
			deviceAddr, ms(start), ms(end), d.Year, d.Month, d.Day, d.Hour, d.Minute, d.Steps, d.Calories, d.Distance, string(source), observedMS, observedMS); err != nil {
			return err
		}
	}
	if err := setMeta(ctx, tx, KindSteps, metaLastAttempt, fmt.Sprintf("%d", observedMS)); err != nil {
		return err
	}
	if err := setMeta(ctx, tx, KindSteps, metaLastSuccess, fmt.Sprintf("%d", observedMS)); err != nil {
		return err
	}
	if err := setMeta(ctx, tx, KindSteps, metaLastError, ""); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SaveSpO2(ctx context.Context, deviceAddr string, source Source, observedAt time.Time, days []protocol.SpO2Day) error {
	rawJSON, err := json.Marshal(days)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	observedMS := ms(observedAt)
	if _, err := tx.ExecContext(ctx, `INSERT INTO metric_observations(kind, observed_ms, device_addr, source, record_count, raw_json) VALUES (?, ?, ?, ?, ?, ?)`,
		KindSpO2, observedMS, deviceAddr, string(source), len(days), string(rawJSON)); err != nil {
		return err
	}
	for _, day := range days {
		dayString := localDay(observedAt).AddDate(0, 0, -day.DaysAgo).Format("2006-01-02")
		if _, err := tx.ExecContext(ctx, `DELETE FROM spo2_samples WHERE device_addr = ? AND day = ?`, deviceAddr, dayString); err != nil {
			return err
		}
		for i, sample := range day.Samples {
			if sample.Min <= 0 || sample.Max <= 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO spo2_samples(
				device_addr, day, sample_index, min, max, source, first_seen_ms, last_seen_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				deviceAddr, dayString, i, sample.Min, sample.Max, string(source), observedMS, observedMS); err != nil {
				return err
			}
		}
	}
	if err := setMeta(ctx, tx, KindSpO2, metaLastAttempt, fmt.Sprintf("%d", observedMS)); err != nil {
		return err
	}
	if err := setMeta(ctx, tx, KindSpO2, metaLastSuccess, fmt.Sprintf("%d", observedMS)); err != nil {
		return err
	}
	if err := setMeta(ctx, tx, KindSpO2, metaLastError, ""); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) HRSamples(ctx context.Context, from, to time.Time) ([]protocol.HRSample, error) {
	fromMS, toMS := rangeMS(from, to)
	rows, err := s.db.QueryContext(ctx, `SELECT time_ms, bpm, interval_min FROM hr_samples
		WHERE (? = 0 OR time_ms >= ?) AND (? = 0 OR time_ms < ?)
		ORDER BY time_ms`, fromMS, fromMS, toMS, toMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []protocol.HRSample
	for rows.Next() {
		var tMS int64
		var sample protocol.HRSample
		if err := rows.Scan(&tMS, &sample.BPM, &sample.Interval); err != nil {
			return nil, err
		}
		sample.Time = fromMSValue(tMS)
		out = append(out, sample)
	}
	return out, rows.Err()
}

func (s *Store) StepDetails(ctx context.Context, from, to time.Time) ([]protocol.SportDetail, error) {
	fromMS, toMS := rangeMS(from, to)
	rows, err := s.db.QueryContext(ctx, `SELECT year, month, day, hour, minute, steps, calories, distance FROM step_buckets
		WHERE (? = 0 OR end_ms > ?) AND (? = 0 OR start_ms < ?)
		ORDER BY start_ms`, fromMS, fromMS, toMS, toMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []protocol.SportDetail
	for rows.Next() {
		var d protocol.SportDetail
		if err := rows.Scan(&d.Year, &d.Month, &d.Day, &d.Hour, &d.Minute, &d.Steps, &d.Calories, &d.Distance); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) SpO2Days(ctx context.Context, from, to time.Time) ([]protocol.SpO2Day, error) {
	fromDay, toDay := dayRange(from, to)
	rows, err := s.db.QueryContext(ctx, `SELECT day, sample_index, min, max FROM spo2_samples
		WHERE (? = '' OR day >= ?) AND (? = '' OR day < ?)
		ORDER BY day DESC, sample_index`, fromDay, fromDay, toDay, toDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byDay := make(map[string][]protocol.SpO2Sample)
	for rows.Next() {
		var day string
		var idx int
		var sample protocol.SpO2Sample
		if err := rows.Scan(&day, &idx, &sample.Min, &sample.Max); err != nil {
			return nil, err
		}
		byDay[day] = append(byDay[day], sample)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	days := make([]string, 0, len(byDay))
	for day := range byDay {
		days = append(days, day)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))

	out := make([]protocol.SpO2Day, 0, len(days))
	today := localDay(time.Now())
	for _, day := range days {
		t, err := time.ParseInLocation("2006-01-02", day, time.Local)
		if err != nil {
			continue
		}
		out = append(out, protocol.SpO2Day{DaysAgo: int(today.Sub(t).Hours() / 24), Samples: byDay[day]})
	}
	return out, nil
}

func (s *Store) RecordSyncError(ctx context.Context, kind string, observedAt time.Time, err error) error {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	tx, txErr := s.db.BeginTx(ctx, nil)
	if txErr != nil {
		return txErr
	}
	defer tx.Rollback()
	if txErr := setMeta(ctx, tx, kind, metaLastAttempt, fmt.Sprintf("%d", ms(observedAt))); txErr != nil {
		return txErr
	}
	if txErr := setMeta(ctx, tx, kind, metaLastError, msg); txErr != nil {
		return txErr
	}
	return tx.Commit()
}

func (s *Store) Status(ctx context.Context) (*Status, error) {
	status := &Status{Errors: map[string]string{}}
	for _, kind := range []string{KindHR, KindSteps, KindSpO2} {
		if lastSync, ok, err := s.metaTime(ctx, kind, metaLastSuccess); err != nil {
			return nil, err
		} else if ok && (status.LastSync == nil || lastSync.After(*status.LastSync)) {
			v := lastSync
			status.LastSync = &v
		}
		if msg, ok, err := s.metaString(ctx, kind, metaLastError); err != nil {
			return nil, err
		} else if ok && msg != "" {
			status.Errors[kind] = msg
		}
	}
	if len(status.Errors) == 0 {
		status.Errors = nil
	}
	return status, nil
}

func (s *Store) metaTime(ctx context.Context, kind, key string) (time.Time, bool, error) {
	v, ok, err := s.metaString(ctx, kind, key)
	if err != nil || !ok {
		return time.Time{}, ok, err
	}
	var milli int64
	if _, err := fmt.Sscanf(v, "%d", &milli); err != nil {
		return time.Time{}, false, err
	}
	return fromMSValue(milli), true, nil
}

func (s *Store) metaString(ctx context.Context, kind, key string) (string, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT value FROM metric_meta WHERE kind = ? AND key = ?`, kind, key)
	var v string
	if err := row.Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return v, true, nil
}

func setMeta(ctx context.Context, tx *sql.Tx, kind, key, value string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO metric_meta(kind, key, value) VALUES (?, ?, ?)
		ON CONFLICT(kind, key) DO UPDATE SET value=excluded.value`, kind, key, value)
	return err
}

func rangeMS(from, to time.Time) (int64, int64) {
	fromMS, toMS := int64(0), int64(0)
	if !from.IsZero() {
		fromMS = ms(from)
	}
	if !to.IsZero() {
		toMS = ms(to)
	}
	return fromMS, toMS
}

func dayRange(from, to time.Time) (string, string) {
	fromDay, toDay := "", ""
	if !from.IsZero() {
		fromDay = localDay(from).Format("2006-01-02")
	}
	if !to.IsZero() {
		toDay = localDay(to).Format("2006-01-02")
	}
	return fromDay, toDay
}

func ms(t time.Time) int64 {
	return t.UTC().UnixMilli()
}

func fromMSValue(v int64) time.Time {
	return time.UnixMilli(v).Local()
}

func localDay(t time.Time) time.Time {
	lt := t.In(time.Local)
	return time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, time.Local)
}
