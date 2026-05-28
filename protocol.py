"""
Y25/RS25 (Colmi R02 family) BLE protocol implementation.

Everything needed to talk to the watch without external dependencies.
Based on Gadgetbridge source + our own packet captures.
"""

from __future__ import annotations

import struct
from dataclasses import dataclass
from datetime import datetime, timedelta
from enum import IntEnum

# ---------------------------------------------------------------------------
# BLE UUIDs
# ---------------------------------------------------------------------------

UART_SERVICE = "6e40fff0-b5a3-f393-e0a9-e50e24dcca9e"
UART_RX = "6e400002-b5a3-f393-e0a9-e50e24dcca9e"
UART_TX = "6e400003-b5a3-f393-e0a9-e50e24dcca9e"

SERVICE_V2 = "de5bf728-d711-4e47-af26-65e3012a5dc7"
CHAR_COMMAND_V2 = "de5bf72a-d711-4e47-af26-65e3012a5dc7"
CHAR_NOTIFY_V2 = "de5bf729-d711-4e47-af26-65e3012a5dc7"

DEVICE_INFO_SERVICE = "0000180a-0000-1000-8000-00805f9b34fb"
HW_CHAR = "00002a27-0000-1000-8000-00805f9b34fb"
FW_CHAR = "00002a26-0000-1000-8000-00805f9b34fb"

# ---------------------------------------------------------------------------
# Big Data V2 (de5bf728 service)
# ---------------------------------------------------------------------------

MAGIC_V2 = 0xBC

class V2Cmd(IntEnum):
    """Known V2 command IDs from decompiled oudmon SDK."""
    SLEEP = 0x27
    BT_MAC = 0x2E
    SYNC_TIME = 0x40
    DEVICE_CONTROL = 0x41
    BATTERY = 0x42
    DEVICE_INFO = 0x43
    AI_VOICE = 0x44
    HEARTBEAT = 0x45
    DEVICE_WEAR = 0x46
    WEAR_SUPPORT = 0x47
    VOICE_STATUS = 0x48
    BT_CONNECT = 0x49
    VOLUME_CONTROL = 0x51
    SPEAK_SOUND_SWITCH = 0x52
    GPT_UPLOAD = 0x59
    DATA_REPORTING = 0x73
    OTA_SOC = 0xFC
    PICTURE_THUMBNAILS = 0xFD


def crc16_modbus(data: bytes) -> int:
    """CRC-16/MODBUS: initial 0xFFFF, poly 0xA001 (reflected)."""
    crc = 0xFFFF
    for b in data:
        crc ^= b
        for _ in range(8):
            if crc & 1:
                crc = (crc >> 1) ^ 0xA001
            else:
                crc >>= 1
    return crc & 0xFFFF


def build_v2_packet(cmd_id: int, payload: bytes = b"") -> bytes:
    """
    Build a Big Data V2 framed packet for the de5bf728 service.

    Format:
      [0xBC] [cmd_id] [len_lo] [len_hi] [crc16_lo] [crc16_hi] [payload...]
    """
    length = len(payload)
    crc = crc16_modbus(payload) if payload else 0xFFFF
    header = bytes([
        MAGIC_V2,
        cmd_id & 0xFF,
        length & 0xFF, (length >> 8) & 0xFF,
        crc & 0xFF, (crc >> 8) & 0xFF,
    ])
    return header + payload


class V2Reassembler:
    """Buffer fragmented Big Data V2 frames across BLE notifications."""

    def __init__(self) -> None:
        self._buf = bytearray()
        self._expected = 0

    def feed(self, data: bytes) -> list[bytes]:
        self._buf.extend(data)
        frames: list[bytes] = []
        while True:
            if self._expected == 0:
                if len(self._buf) < 6:
                    break
                if self._buf[0] != MAGIC_V2:
                    # Out of sync — drop and resync on next notification
                    self._buf.clear()
                    break
                payload_len = self._buf[2] | (self._buf[3] << 8)
                self._expected = 6 + payload_len

            if len(self._buf) < self._expected:
                break

            frames.append(bytes(self._buf[:self._expected]))
            del self._buf[:self._expected]
            self._expected = 0
        return frames


# ---------------------------------------------------------------------------
# Command bytes (V1 service)
# ---------------------------------------------------------------------------

class Cmd(IntEnum):
    SET_TIME = 0x01
    BATTERY = 0x03
    PHONE_NAME = 0x04
    PREFERENCES = 0x0A
    SYNC_HR = 0x15
    AUTO_HR_PREF = 0x16
    GOALS = 0x21
    AUTO_SPO2_PREF = 0x2C
    PACKET_SIZE = 0x2F
    AUTO_STRESS_PREF = 0x36
    SYNC_STRESS = 0x37
    AUTO_HRV_PREF = 0x38
    SYNC_HRV = 0x39
    AUTO_TEMP_PREF = 0x3A
    SYNC_ACTIVITY = 0x43
    FIND_DEVICE = 0x50
    MANUAL_HR = 0x69
    NOTIFICATION = 0x73
    REALTIME_HR = 0x1E
    BIG_DATA_V2 = 0xBC
    FACTORY_RESET = 0xFF

# ---------------------------------------------------------------------------
# Notification sub-types (CMD 0x73)
# ---------------------------------------------------------------------------

class Notify(IntEnum):
    NEW_HR = 0x01
    NEW_SPO2 = 0x03
    NEW_STEPS = 0x04
    BATTERY = 0x0C
    LIVE_ACTIVITY = 0x12

# ---------------------------------------------------------------------------
# Sleep stages
# ---------------------------------------------------------------------------

class SleepStage(IntEnum):
    LIGHT = 0x02
    DEEP = 0x03
    REM = 0x04
    AWAKE = 0x05

SLEEP_STAGE_NAMES = {
    SleepStage.LIGHT: "light",
    SleepStage.DEEP: "deep",
    SleepStage.REM: "REM",
    SleepStage.AWAKE: "awake",
}

# ---------------------------------------------------------------------------
# Packet helpers
# ---------------------------------------------------------------------------

PACKET_LEN = 16


def build_packet(command: int, payload: bytes | None = None) -> bytearray:
    """Build a 16-byte packet: [cmd][payload...][checksum]."""
    if not (0 <= command <= 255):
        raise ValueError(f"command must be 0-255, got {command}")
    buf = bytearray(PACKET_LEN)
    buf[0] = command
    if payload:
        assert len(payload) <= PACKET_LEN - 2
        buf[1:1 + len(payload)] = payload
    buf[PACKET_LEN - 1] = checksum(buf)
    return buf


def checksum(buf: bytearray) -> int:
    """Sum of bytes 0..14 mod 256."""
    return sum(buf[:-1]) & 0xFF


def bcd_byte(val: int) -> int:
    return ((val // 10) << 4) | (val % 10)


def uint16_le(b0: int, b1: int) -> int:
    return b0 | (b1 << 8)


def bcd_to_decimal(b: int) -> int:
    return (((b >> 4) & 0x0F) * 10) + (b & 0x0F)

# ---------------------------------------------------------------------------
# Battery
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class BatteryInfo:
    level: int
    charging: bool


def parse_battery(packet: bytearray) -> BatteryInfo | None:
    if len(packet) < 3 or packet[0] != Cmd.BATTERY:
        return None
    return BatteryInfo(level=packet[1], charging=packet[2] == 1)

# ---------------------------------------------------------------------------
# Device notifications (CMD 0x73)
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class LiveActivity:
    steps: int
    calories: float
    distance: int


def parse_notification(packet: bytearray) -> tuple[int, object] | None:
    """
    Parse a CMD 0x73 notification.
    Returns (notify_type, extra_data) or None if not a notification.
    """
    if len(packet) < 2 or packet[0] != Cmd.NOTIFICATION:
        return None
    ntype = packet[1]
    if ntype == Notify.NEW_HR and len(packet) >= 3:
        return (ntype, packet[2])
    if ntype == Notify.NEW_SPO2 and len(packet) >= 3:
        return (ntype, packet[2])
    if ntype == Notify.BATTERY and len(packet) >= 4:
        return (ntype, BatteryInfo(level=packet[2], charging=packet[3] == 1))
    if ntype == Notify.LIVE_ACTIVITY and len(packet) >= 11:
        steps = (packet[2] << 16) | (packet[3] << 8) | packet[4]
        calories = ((packet[5] << 16) | (packet[6] << 8) | packet[7]) / 10.0
        distance = (packet[8] << 16) | (packet[9] << 8) | packet[10]
        return (ntype, LiveActivity(steps, calories, distance))
    return (ntype, None)

# ---------------------------------------------------------------------------
# Real-time HR (CMD 105 / DataRequest)
# ---------------------------------------------------------------------------

class RtAction(IntEnum):
    START = 1
    PAUSE = 2
    CONTINUE = 3
    STOP = 4


class RtType(IntEnum):
    HEART_RATE = 1
    BLOOD_PRESSURE = 2
    SPO2 = 3
    FATIGUE = 4
    HEALTH_CHECK = 5
    ECG = 7
    PRESSURE = 8
    BLOOD_SUGAR = 9
    HRV = 10


def rt_start_packet(reading: RtType) -> bytearray:
    return build_packet(105, bytes([reading, RtAction.START]))


def rt_continue_packet(reading: RtType) -> bytearray:
    return build_packet(105, bytes([reading, RtAction.CONTINUE]))


def rt_stop_packet(reading: RtType) -> bytearray:
    return build_packet(106, bytes([reading, 0, 0]))


@dataclass(frozen=True)
class RtReading:
    kind: RtType
    value: int


@dataclass(frozen=True)
class RtError:
    kind: RtType
    code: int


def parse_realtime(packet: bytearray) -> RtReading | RtError | None:
    if packet[0] != Cmd.MANUAL_HR or len(packet) < 4:
        return None
    kind = RtType(packet[1])
    error = packet[2]
    if error != 0:
        return RtError(kind=kind, code=error)
    return RtReading(kind=kind, value=packet[3])

# ---------------------------------------------------------------------------
# Sleep data (Big Data V2, CMD 0xBC / type 0x27)
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class SleepSession:
    start: datetime
    end: datetime
    stages: list[tuple[int, int]]  # (SleepStage, minutes)


def read_hr_log_packet(target: datetime) -> bytearray:
    """Build a CMD 0x15 request for a given day (midnight)."""
    ts = int(target.timestamp())
    return build_packet(Cmd.SYNC_HR, struct.pack("<I", ts))


def read_steps_packet(day_offset: int) -> bytearray:
    """Build a CMD 0x43 request for N days ago."""
    sub = bytearray(b"\x00\x0f\x00\x5f\x01")
    sub[0] = day_offset
    return build_packet(Cmd.SYNC_ACTIVITY, sub)


def parse_sleep_data(raw: bytearray) -> list[SleepSession]:
    """
    Parse a Big Data V2 sleep response.
    Pass the fully concatenated response bytes (including header).
    """
    if len(raw) < 7 or raw[0] != Cmd.BIG_DATA_V2 or raw[1] != 0x27:
        return []

    payload_len = uint16_le(raw[2], raw[3])
    if payload_len < 2:
        return []

    days = raw[6]
    sessions: list[SleepSession] = []
    idx = 7
    base_today = datetime.now().replace(hour=0, minute=0, second=0, microsecond=0)

    for _ in range(days):
        if idx >= len(raw):
            break
        days_ago = raw[idx]
        idx += 1
        day_bytes = raw[idx]
        idx += 1
        s_start = uint16_le(raw[idx], raw[idx + 1])
        idx += 2
        s_end = uint16_le(raw[idx], raw[idx + 1])
        idx += 2

        base_day = base_today - timedelta(days=days_ago)
        if s_start > s_end:
            start = base_day - timedelta(minutes=1440 - s_start)
        else:
            start = base_day + timedelta(minutes=s_start)
        end = base_day + timedelta(minutes=s_end)

        stages: list[tuple[int, int]] = []
        for _ in range((day_bytes - 4) // 2):
            if idx + 1 >= len(raw):
                break
            st, dur = raw[idx], raw[idx + 1]
            idx += 2
            if dur > 0:
                stages.append((st, dur))

        sessions.append(SleepSession(start=start, end=end, stages=stages))

    return sessions

# ---------------------------------------------------------------------------
# High-level request builders
# ---------------------------------------------------------------------------

def set_time_packet(now: datetime | None = None) -> bytearray:
    if now is None:
        now = datetime.now()
    return build_packet(Cmd.SET_TIME, bytes([
        bcd_byte(now.year % 2000),
        bcd_byte(now.month),
        bcd_byte(now.day),
        bcd_byte(now.hour),
        bcd_byte(now.minute),
        bcd_byte(now.second),
    ]))


def battery_packet() -> bytearray:
    return build_packet(Cmd.BATTERY)


def phone_name_packet(name: str = "GB") -> bytearray:
    payload = bytes([0x02, 0x0A]) + name.encode("ascii")[:10]
    return build_packet(Cmd.PHONE_NAME, payload)


def find_device_packet() -> bytearray:
    return build_packet(Cmd.FIND_DEVICE, bytes([0x55, 0xAA]))


def sleep_request_packet() -> bytes:
    """Build a V2 sleep request packet (returns all sleep sessions)."""
    # Request 1 byte payload [0xFF] to get all sessions
    return build_v2_packet(V2Cmd.SLEEP, bytes([0xFF]))


def battery_v2_packet() -> bytes:
    """Build a V2 battery request packet (may or may not be supported on watch)."""
    return build_v2_packet(V2Cmd.BATTERY, b"")


def device_info_v2_packet() -> bytes:
    """Build a V2 device-info request packet (may or may not be supported on watch)."""
    return build_v2_packet(V2Cmd.DEVICE_INFO, b"")
