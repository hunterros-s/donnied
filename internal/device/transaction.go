package device

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"smartwatch/internal/ble"
	"smartwatch/protocol"
)

// ---------------------------------------------------------------------------
// Transaction types
// ---------------------------------------------------------------------------

// txnResult carries the outcome of a request/response transaction.
type txnResult struct {
	data any
	err  error
}

// txnReq is a transactional request: send a packet and wait for
// response(s) matching the given cmd byte. The feed function
// is called for each matching notification until it returns done=true.
// At most one txnReq is in flight at a time (serialised via txnMu).
type txnReq struct {
	ctx      context.Context
	protocol ble.Protocol
	cmd      byte // V1 cmd byte or V2 data ID
	packet   []byte
	feed     func([]byte) (bool, any, error)
	result   chan txnResult
}

func (txnReq) isRequest() {}

// ---------------------------------------------------------------------------
// Public transaction API
// ---------------------------------------------------------------------------

// GetBattery sends a battery request and waits for the response.
func (m *Manager) GetBattery(ctx context.Context) (*protocol.BatteryInfo, error) {
	result, err := m.do(ctx, ble.V1, byte(protocol.CmdBattery), protocol.BatteryPacket(),
		func(pkt []byte) (bool, any, error) {
			if protocol.IsErrorResponse(pkt) {
				return true, nil, fmt.Errorf("device error: 0x%02X", pkt[0])
			}
			info, ok := protocol.ParseBattery(pkt)
			if !ok {
				return true, nil, fmt.Errorf("invalid battery response")
			}
			return true, info, nil
		},
	)
	if err != nil {
		return nil, err
	}
	return result.(*protocol.BatteryInfo), nil
}

// GetHRLog requests heart rate log data for the given day.
func (m *Manager) GetHRLog(ctx context.Context, target protocol.Day) ([]protocol.HRSample, error) {
	start := time.Now()
	day := target.Time().Format("2006-01-02")
	m.logger.Info("requesting HR log", "day", day)

	result, err := m.do(ctx, ble.V1, byte(protocol.CmdSyncHR), protocol.ReadHRLogPacket(target.Time()),
		func(pkt []byte) (bool, any, error) {
			if protocol.IsErrorResponse(pkt) {
				return true, nil, fmt.Errorf("device error: 0x%02X", pkt[0])
			}
			return m.hrLogParser.Feed(pkt)
		},
	)
	if err != nil {
		m.logger.Warn("HR log failed", "day", day, "err", err, "duration", time.Since(start).Round(time.Millisecond))
		return nil, err
	}
	if result == nil {
		m.logger.Info("HR log received", "day", day, "samples", 0, "duration", time.Since(start).Round(time.Millisecond))
		return []protocol.HRSample{}, nil
	}
	samples := result.([]protocol.HRSample)
	m.logger.Info("HR log received", "day", day, "samples", len(samples), "duration", time.Since(start).Round(time.Millisecond))
	return samples, nil
}

// GetSteps requests step/activity data for the given day offset (0=today).
func (m *Manager) GetSteps(ctx context.Context, dayOffset int) ([]protocol.SportDetail, error) {
	start := time.Now()
	m.logger.Info("requesting steps", "offset", dayOffset)

	result, err := m.do(ctx, ble.V1, byte(protocol.CmdSyncActivity), protocol.ReadStepsPacket(dayOffset),
		func(pkt []byte) (bool, any, error) {
			if protocol.IsErrorResponse(pkt) {
				return true, nil, fmt.Errorf("device error: 0x%02X", pkt[0])
			}
			return m.stepsParser.Feed(pkt)
		},
	)
	if err != nil {
		m.logger.Warn("steps failed", "offset", dayOffset, "err", err, "duration", time.Since(start).Round(time.Millisecond))
		return nil, err
	}
	if result == nil {
		m.logger.Info("steps received", "offset", dayOffset, "records", 0, "duration", time.Since(start).Round(time.Millisecond))
		return []protocol.SportDetail{}, nil
	}
	details := result.([]protocol.SportDetail)
	m.logger.Info("steps received", "offset", dayOffset, "records", len(details), "duration", time.Since(start).Round(time.Millisecond))
	return details, nil
}

// GetSleep requests sleep data via the V2 Big Data protocol.
func (m *Manager) GetSleep(ctx context.Context) ([]protocol.SleepSession, error) {
	start := time.Now()
	m.logger.Info("requesting sleep")

	result, err := m.do(ctx, ble.V2, byte(protocol.V2Sleep), protocol.SleepRequestPacket(),
		func(frame []byte) (bool, any, error) {
			sessions := protocol.ParseSleepData(frame)
			if sessions == nil {
				return true, nil, fmt.Errorf("invalid sleep response")
			}
			return true, sessions, nil
		},
	)
	if err != nil {
		m.logger.Warn("sleep failed", "err", err, "duration", time.Since(start).Round(time.Millisecond))
		return nil, err
	}
	if result == nil {
		m.logger.Info("sleep received", "sessions", 0, "duration", time.Since(start).Round(time.Millisecond))
		return []protocol.SleepSession{}, nil
	}
	sessions := result.([]protocol.SleepSession)
	m.logger.Info("sleep received", "sessions", len(sessions), "duration", time.Since(start).Round(time.Millisecond))
	return sessions, nil
}

// GetSpO2 requests SpO2 history data via the V2 Big Data protocol.
func (m *Manager) GetSpO2(ctx context.Context, daysAgo int) ([]protocol.SpO2Day, error) {
	start := time.Now()
	m.logger.Info("requesting SpO2 history", "days_ago", daysAgo)

	result, err := m.do(ctx, ble.V2, byte(protocol.V2SpO2), protocol.SpO2RequestPacket(daysAgo),
		func(frame []byte) (bool, any, error) {
			days := protocol.ParseSpO2Data(frame)
			if days == nil {
				return true, nil, fmt.Errorf("invalid SpO2 response")
			}
			return true, days, nil
		},
	)
	if err != nil {
		m.logger.Warn("SpO2 history failed", "days_ago", daysAgo, "err", err, "duration", time.Since(start).Round(time.Millisecond))
		return nil, err
	}
	if result == nil {
		m.logger.Info("SpO2 history received", "days_ago", daysAgo, "days", 0, "duration", time.Since(start).Round(time.Millisecond))
		return []protocol.SpO2Day{}, nil
	}
	days := result.([]protocol.SpO2Day)
	m.logger.Info("SpO2 history received", "days_ago", daysAgo, "days", len(days), "duration", time.Since(start).Round(time.Millisecond))
	return days, nil
}

// ReadRealtime starts a real-time measurement, waits for the first matching
// non-zero reading, then sends a stop command. HR/SpO2 commonly emit 0 while
// the optical sensor is settling; those calibration samples are ignored.
// Manual readings are returned to the caller; they are not persisted into the
// snapshot cache.
func (m *Manager) ReadRealtime(ctx context.Context, rt protocol.RtType) (*protocol.RtReading, error) {
	start := time.Now()
	calibrationSamples := 0
	m.logger.Info("requesting realtime reading", "type", rt.String())

	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		if err := m.SendV1(stopCtx, protocol.RtStopPacket(rt)); err != nil {
			m.logger.Warn("realtime stop after read failed", "type", rt.String(), "err", err)
		}
	}()

	result, err := m.do(ctx, ble.V1, byte(protocol.CmdManualHR), protocol.RtStartPacket(rt),
		func(pkt []byte) (bool, any, error) {
			if protocol.IsErrorResponse(pkt) {
				return true, nil, fmt.Errorf("device error: 0x%02X", pkt[0])
			}
			reading, ok := protocol.ParseRealtime(pkt)
			if !ok {
				return true, nil, fmt.Errorf("invalid realtime response")
			}
			switch v := reading.(type) {
			case protocol.RtError:
				if v.Kind != rt {
					return false, nil, nil
				}
				return true, nil, fmt.Errorf("realtime %s error: %d", rt.String(), v.Code)
			case protocol.RtReading:
				if v.Kind != rt {
					return false, nil, nil
				}
				if v.Value == 0 && (rt == protocol.RtHeartRate || rt == protocol.RtSpO2) {
					calibrationSamples++
					return false, nil, nil
				}
				return true, &v, nil
			default:
				return true, nil, fmt.Errorf("unknown realtime response")
			}
		},
	)
	if err != nil {
		m.logger.Warn("realtime reading failed", "type", rt.String(), "err", err, "duration", time.Since(start).Round(time.Millisecond))
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("no realtime reading returned")
	}
	reading := result.(*protocol.RtReading)
	m.logger.Info("realtime reading received", "type", rt.String(), "value", reading.Value, "calibration_samples", calibrationSamples, "duration", time.Since(start).Round(time.Millisecond))
	return reading, nil
}

// StartRealtime sends a start command for a real-time reading (HR, SpO2, etc.).
func (m *Manager) StartRealtime(ctx context.Context, rt protocol.RtType) error {
	m.logger.Info("starting realtime stream", "type", rt.String())
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	return m.SendV1(ctx, protocol.RtStartPacket(rt))
}

// StopRealtime sends a stop command for a real-time reading.
func (m *Manager) StopRealtime(ctx context.Context, rt protocol.RtType) error {
	m.logger.Info("stopping realtime stream", "type", rt.String())
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	return m.SendV1(ctx, protocol.RtStopPacket(rt))
}

// ---------------------------------------------------------------------------
// do — send a request and wait for response(s)
// ---------------------------------------------------------------------------

// txnTimeout is the default timeout for request/response transactions.
// Multi-packet history syncs may need longer than the 5s cmdTimeout
// used for fire-and-forget commands.
const txnTimeout = 30 * time.Second

// do sends a packet and waits for matching response(s). It serialises
// via txnMu so that only one transaction is in flight at a time.
// The loop registers the txn, watches its context for cancellation,
// and clears it on completion or timeout — so no entry ever leaks.
func (m *Manager) do(ctx context.Context, proto ble.Protocol, cmd byte, packet []byte,
	feed func([]byte) (bool, any, error)) (any, error) {

	ctx, cancel := context.WithTimeout(ctx, txnTimeout)
	defer cancel()

	m.txnMu.Lock()
	defer m.txnMu.Unlock()

	result := make(chan txnResult, 1)
	req := txnReq{
		ctx:      ctx,
		protocol: proto,
		cmd:      cmd,
		packet:   bytes.Clone(packet),
		feed:     feed,
		result:   result,
	}

	select {
	case m.reqs <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case r := <-result:
		return r.data, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// resetParserFor resets the multi-packet parser for a V1 command.
// Called in the session loop when a new transaction is registered,
// so stale state from a previous (cancelled or completed) request
// never contaminates the next one.
func (m *Manager) resetParserFor(proto ble.Protocol, cmd byte) {
	if proto != ble.V1 {
		return
	}
	switch protocol.Cmd(cmd) {
	case protocol.CmdSyncHR:
		m.hrLogParser.Reset()
	case protocol.CmdSyncActivity:
		m.stepsParser.Reset()
	}
}
