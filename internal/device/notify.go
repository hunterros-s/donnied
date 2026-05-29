package device

import (
	"bytes"
	"fmt"
	"time"

	"smartwatch/protocol"
)

// handleV1 parses a V1 (UART) notification and updates device data.
// Called only from the owner loop.
func (m *Manager) handleV1(data *DeviceData, packet []byte) {
	data.LastSeen = time.Now()

	if len(packet) == 0 {
		return
	}

	m.logger.Debug("v1 notification", "cmd", fmt.Sprintf("0x%02X", packet[0]), "len", len(packet), "packet", fmt.Sprintf("% X", packet))

	switch protocol.Cmd(packet[0]) {
	case protocol.CmdBattery:
		if info, ok := protocol.ParseBattery(packet); ok {
			changed := data.Battery == nil || data.Battery.Level != info.Level || data.Battery.Charging != info.Charging
			data.Battery = info
			if changed {
				m.logger.Info("battery updated", "level", info.Level, "charging", info.Charging)
			} else {
				m.logger.Debug("battery unchanged", "level", info.Level, "charging", info.Charging)
			}
		}

	case protocol.CmdManualHR:
		reading, ok := protocol.ParseRealtime(packet)
		if !ok {
			return
		}
		switch v := reading.(type) {
		case protocol.RtReading:
			m.logger.Info("realtime notification", "type", v.Kind.String(), "value", v.Value)
		case protocol.RtError:
			m.logger.Warn("realtime notification error", "type", v.Kind.String(), "code", v.Code)
		}

	case protocol.CmdNotification:
		ntype, extra, ok := protocol.ParseNotification(packet)
		if !ok {
			m.logger.Warn("invalid device notification", "packet", fmt.Sprintf("% X", packet))
			return
		}
		m.logNotification(ntype, extra, packet)
		applyNotification(data, ntype, extra)
	}
}

func (m *Manager) logNotification(ntype protocol.Notify, extra any, packet []byte) {
	switch ntype {
	case protocol.NotifyNewHR:
		if v, ok := extra.(int); ok {
			if v == 0 {
				m.logger.Debug("heart-rate notification", "bpm", v)
			} else {
				m.logger.Info("heart-rate notification", "bpm", v)
			}
			return
		}
	case protocol.NotifyNewSpO2:
		if v, ok := extra.(int); ok {
			m.logger.Info("SpO2 notification", "percent", v)
			return
		}
	case protocol.NotifyNewSteps:
		if v, ok := extra.(int); ok {
			m.logger.Info("steps notification", "steps", v)
			return
		}
	case protocol.NotifyBattery:
		if info, ok := extra.(*protocol.BatteryInfo); ok {
			m.logger.Info("battery notification", "level", info.Level, "charging", info.Charging)
			return
		}
	case protocol.NotifyLiveActivity:
		if act, ok := extra.(protocol.LiveActivity); ok {
			m.logger.Info("live activity notification", "steps", act.Steps, "calories", act.Calories, "distance", act.Distance)
			return
		}
	}
	m.logger.Debug("unparsed device notification", "type", fmt.Sprintf("0x%02X", byte(ntype)), "packet", fmt.Sprintf("% X", packet))
}

func applyNotification(data *DeviceData, ntype protocol.Notify, extra any) {
	switch ntype {
	case protocol.NotifyNewHR:
		if v, ok := extra.(int); ok && v > 0 {
			data.HeartRate = v
		}
	case protocol.NotifyNewSpO2:
		if v, ok := extra.(int); ok {
			data.SpO2 = v
		}
	case protocol.NotifyNewSteps:
		if v, ok := extra.(int); ok {
			data.Steps = v
		}
	case protocol.NotifyBattery:
		if info, ok := extra.(*protocol.BatteryInfo); ok {
			data.Battery = info
		}
	case protocol.NotifyLiveActivity:
		if act, ok := extra.(protocol.LiveActivity); ok {
			data.LiveActivity = &act
			data.Steps = act.Steps
			data.Calories = act.Calories
			data.Distance = act.Distance
		}
	}
}

// handleV2 processes a complete V2 (Big Data) frame and updates device data.
// Called only from the owner loop for unsolicited frames.
func (m *Manager) handleV2(data *DeviceData, frame []byte, deviceAddr string) {
	receivedAt := time.Now()
	data.LastSeen = receivedAt

	if len(frame) < 2 {
		return
	}
	cmd := protocol.V2Cmd(frame[1])
	m.logger.Info("v2 frame (unsolicited)", "cmd", fmt.Sprintf("0x%02X", byte(cmd)), "len", len(frame))

	select {
	case m.events <- Event{Type: EventV2Frame, DeviceAddr: deviceAddr, ReceivedAt: receivedAt, V2Cmd: cmd, Data: bytes.Clone(frame)}:
	default:
		m.logger.Warn("dropping device event; queue full", "type", EventV2Frame, "cmd", fmt.Sprintf("0x%02X", byte(cmd)))
	}
}
