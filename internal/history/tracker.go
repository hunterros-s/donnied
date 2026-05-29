package history

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"smartwatch/protocol"
)

const (
	DefaultPollInterval   = 20 * time.Minute
	DefaultSyncDays       = 7
	connectionCheckPeriod = 5 * time.Second
	syncTimeout           = 5 * time.Minute
)

type Device interface {
	IsConnected() bool
	PairedDevice() (addr, name string)
	GetHRLog(ctx context.Context, day protocol.Day) ([]protocol.HRSample, error)
	GetSteps(ctx context.Context, dayOffset int) ([]protocol.SportDetail, error)
	GetSpO2(ctx context.Context, daysAgo int) ([]protocol.SpO2Day, error)
}

// Tracker periodically syncs watch metric history into SQLite. It intentionally
// polls less often than sleep because these records are historical aggregates
// and BLE history transactions are more expensive than reading cached state.
type Tracker struct {
	device       Device
	store        *Store
	logger       *slog.Logger
	pollInterval time.Duration
	syncDays     int
}

func NewTracker(device Device, store *Store, logger *slog.Logger) *Tracker {
	return &Tracker{
		device:       device,
		store:        store,
		logger:       logger,
		pollInterval: DefaultPollInterval,
		syncDays:     DefaultSyncDays,
	}
}

func (t *Tracker) Run(ctx context.Context) error {
	t.logger.Info("metric history tracker started", "poll_interval", t.pollInterval, "sync_days", t.syncDays)
	defer t.logger.Info("metric history tracker stopped")

	pollTicker := time.NewTicker(t.pollInterval)
	defer pollTicker.Stop()
	connTicker := time.NewTicker(connectionCheckPeriod)
	defer connTicker.Stop()

	connected := t.device.IsConnected()
	if connected {
		t.syncAndLog(ctx)
	} else {
		t.logger.Debug("metric history tracker waiting for watch connection")
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-connTicker.C:
			nowConnected := t.device.IsConnected()
			if nowConnected && !connected {
				t.logger.Info("watch connected; syncing metric history")
				t.syncAndLog(ctx)
			} else if !nowConnected && connected {
				t.logger.Info("watch disconnected; pausing metric history sync")
			}
			connected = nowConnected

		case <-pollTicker.C:
			if !t.device.IsConnected() {
				t.logger.Debug("metric history sync skipped; watch not connected")
				connected = false
				continue
			}
			connected = true
			t.syncAndLog(ctx)
		}
	}
}

func (t *Tracker) SyncNow(ctx context.Context) error {
	return t.syncWindow(ctx)
}

func (t *Tracker) HRSamples(ctx context.Context, from, to time.Time) ([]protocol.HRSample, error) {
	return t.store.HRSamples(ctx, from, to)
}

func (t *Tracker) StepDetails(ctx context.Context, from, to time.Time) ([]protocol.SportDetail, error) {
	return t.store.StepDetails(ctx, from, to)
}

func (t *Tracker) SpO2Days(ctx context.Context, from, to time.Time) ([]protocol.SpO2Day, error) {
	return t.store.SpO2Days(ctx, from, to)
}

func (t *Tracker) Status(ctx context.Context) (*Status, error) {
	return t.store.Status(ctx)
}

func (t *Tracker) syncAndLog(ctx context.Context) {
	if err := t.syncWindow(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		t.logger.Warn("metric history sync failed", "err", err)
	}
}

func (t *Tracker) syncWindow(ctx context.Context) error {
	if !t.device.IsConnected() {
		return errors.New("watch not connected")
	}
	start := time.Now()
	t.logger.Info("metric history sync window starting", "days", t.syncDays)
	var errs []error
	for daysAgo := 0; daysAgo < t.syncDays; daysAgo++ {
		if err := t.syncDay(ctx, daysAgo); err != nil {
			errs = append(errs, fmt.Errorf("day %d: %w", daysAgo, err))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	t.logger.Info("metric history sync window completed", "days", t.syncDays, "duration", time.Since(start).Round(time.Millisecond))
	return nil
}

func (t *Tracker) syncDay(ctx context.Context, daysAgo int) error {
	if !t.device.IsConnected() {
		err := errors.New("watch not connected")
		_ = t.store.RecordSyncError(ctx, KindHR, time.Now(), err)
		_ = t.store.RecordSyncError(ctx, KindSteps, time.Now(), err)
		_ = t.store.RecordSyncError(ctx, KindSpO2, time.Now(), err)
		return err
	}

	addr, _ := t.device.PairedDevice()
	if addr == "" {
		addr = "unknown"
	}

	syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()

	dayTime := localDay(time.Now()).AddDate(0, 0, -daysAgo)
	day := protocol.Day{Year: dayTime.Year(), Month: dayTime.Month(), Day: dayTime.Day()}
	source := SourcePoll
	observedAt := time.Now()

	t.logger.Info("metric history sync starting", "days_ago", daysAgo, "device_addr", addr)
	var errs []error

	if samples, err := t.device.GetHRLog(syncCtx, day); err != nil {
		_ = t.store.RecordSyncError(context.Background(), KindHR, observedAt, err)
		errs = append(errs, fmt.Errorf("hr: %w", err))
	} else if err := t.store.SaveHR(ctx, addr, source, observedAt, samples); err != nil {
		errs = append(errs, fmt.Errorf("save hr: %w", err))
	} else {
		t.logger.Info("HR history stored", "days_ago", daysAgo, "samples", len(samples))
	}

	if details, err := t.device.GetSteps(syncCtx, daysAgo); err != nil {
		_ = t.store.RecordSyncError(context.Background(), KindSteps, observedAt, err)
		errs = append(errs, fmt.Errorf("steps: %w", err))
	} else if err := t.store.SaveSteps(ctx, addr, source, observedAt, details); err != nil {
		errs = append(errs, fmt.Errorf("save steps: %w", err))
	} else {
		t.logger.Info("steps history stored", "days_ago", daysAgo, "records", len(details))
	}

	if days, err := t.device.GetSpO2(syncCtx, daysAgo); err != nil {
		_ = t.store.RecordSyncError(context.Background(), KindSpO2, observedAt, err)
		errs = append(errs, fmt.Errorf("spo2: %w", err))
	} else if err := t.store.SaveSpO2(ctx, addr, source, observedAt, normalizeSpO2Days(days, daysAgo)); err != nil {
		errs = append(errs, fmt.Errorf("save spo2: %w", err))
	} else {
		records := 0
		for _, day := range days {
			records += len(day.Samples)
		}
		t.logger.Info("SpO2 history stored", "days_ago", daysAgo, "days", len(days), "samples", records)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	t.logger.Info("metric history sync completed", "days_ago", daysAgo, "device_addr", addr)
	return nil
}

func normalizeSpO2Days(days []protocol.SpO2Day, requestedDaysAgo int) []protocol.SpO2Day {
	// The SpO2 payload's embedded day byte is not reliable on this watch/firmware;
	// the request is explicitly for one day, so persist against the requested day.
	out := make([]protocol.SpO2Day, len(days))
	copy(out, days)
	for i := range out {
		out[i].DaysAgo = requestedDaysAgo
	}
	return out
}
