#!/usr/bin/env python3
"""Minimal Colmi/Y25/RS25 raw sensor logger.

Connects to the watch/ring UART BLE service, starts the silent raw sensor mode
with A1 payload 0101, pretty-prints raw sensor notifications, and persists each
parsed event to SQLite.

Install:
    python3 -m pip install bleak

Examples:
    python3 scripts/raw0101_logger.py --address AA:BB:CC:DD:EE:FF
    python3 scripts/raw0101_logger.py --name Y25 --db raw_sensor.db
    python3 scripts/raw0101_logger.py --seconds 300

Stop with Ctrl-C. The script sends A1 02 on exit.
"""

from __future__ import annotations

import argparse
import asyncio
import math
import sqlite3
import sys
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Optional

try:
    from bleak import BleakClient, BleakScanner
except ImportError:  # pragma: no cover
    BleakClient = None
    BleakScanner = None

UART_SERVICE = "6e40fff0-b5a3-f393-e0a9-e50e24dcca9e"
UART_WRITE = "6e400002-b5a3-f393-e0a9-e50e24dcca9e"  # phone/computer -> watch
UART_NOTIFY = "6e400003-b5a3-f393-e0a9-e50e24dcca9e"  # watch -> phone/computer
ADV_SERVICE = "0000fe00-0000-1000-8000-00805f9b34fb"

PACKET_LEN = 16
CMD_RAW_SENSOR = 0xA1

# Silent raw accel-ish stream discovered on this watch.
RAW_START_0101 = bytes.fromhex("0101")
RAW_STOP = bytes.fromhex("02")


def checksum(packet_without_crc: bytes) -> int:
    return sum(packet_without_crc) & 0xFF


def build_packet(command: int, payload: bytes = b"") -> bytes:
    if len(payload) > PACKET_LEN - 2:
        raise ValueError("payload too long")
    buf = bytearray(PACKET_LEN)
    buf[0] = command & 0xFF
    buf[1 : 1 + len(payload)] = payload
    buf[-1] = checksum(buf[:-1])
    return bytes(buf)


def signed12(high: int, low: int) -> int:
    v = ((high & 0xFF) << 4) | (low & 0x0F)
    if v & 0x800:
        v -= 0x1000
    return v


@dataclass
class RawReading:
    time: datetime
    kind: str
    payload: bytes
    acc_x: Optional[int] = None
    acc_y: Optional[int] = None
    acc_z: Optional[int] = None
    ppg: Optional[int] = None
    ppg_max: Optional[int] = None
    ppg_min: Optional[int] = None
    ppg_diff: Optional[int] = None
    spo2_raw: Optional[int] = None
    spo2_max: Optional[int] = None
    spo2_min: Optional[int] = None
    spo2_diff: Optional[int] = None
    mag: Optional[float] = None
    move: Optional[float] = None


def parse_raw_sensor(packet: bytes, previous_accel: Optional[RawReading]) -> Optional[RawReading]:
    if len(packet) < 10 or packet[0] != CMD_RAW_SENSOR:
        return None

    r = RawReading(time=datetime.now(timezone.utc), kind="unknown", payload=bytes(packet))
    subtype = packet[1]

    if subtype == 0x01:
        r.kind = "spo2_raw"
        r.spo2_raw = (packet[2] << 8) | packet[3]
        r.spo2_max = packet[5]
        r.spo2_min = packet[7]
        r.spo2_diff = packet[9]
    elif subtype == 0x02:
        r.kind = "ppg"
        r.ppg = (packet[2] << 8) | packet[3]
        r.ppg_max = (packet[4] << 8) | packet[5]
        r.ppg_min = (packet[6] << 8) | packet[7]
        r.ppg_diff = (packet[8] << 8) | packet[9]
    elif subtype == 0x03:
        r.kind = "accelerometer"
        # Matches the Go parser after fixing signed 12-bit decoding.
        r.acc_x = signed12(packet[6], packet[7])
        r.acc_y = signed12(packet[2], packet[3])
        r.acc_z = signed12(packet[4], packet[5])
        r.mag = math.sqrt(r.acc_x * r.acc_x + r.acc_y * r.acc_y + r.acc_z * r.acc_z)
        if previous_accel and previous_accel.acc_x is not None:
            dx = r.acc_x - (previous_accel.acc_x or 0)
            dy = r.acc_y - (previous_accel.acc_y or 0)
            dz = r.acc_z - (previous_accel.acc_z or 0)
            r.move = math.sqrt(dx * dx + dy * dy + dz * dz)
        else:
            r.move = 0.0
    else:
        return None

    return r


def init_db(path: str) -> sqlite3.Connection:
    conn = sqlite3.connect(path)
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute(
        """
        CREATE TABLE IF NOT EXISTS raw_sensor_events (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            time_utc TEXT NOT NULL,
            device_address TEXT,
            kind TEXT NOT NULL,
            payload_hex TEXT NOT NULL,
            acc_x INTEGER,
            acc_y INTEGER,
            acc_z INTEGER,
            mag REAL,
            move REAL,
            ppg INTEGER,
            ppg_max INTEGER,
            ppg_min INTEGER,
            ppg_diff INTEGER,
            spo2_raw INTEGER,
            spo2_max INTEGER,
            spo2_min INTEGER,
            spo2_diff INTEGER
        )
        """
    )
    conn.execute("CREATE INDEX IF NOT EXISTS idx_raw_sensor_events_time ON raw_sensor_events(time_utc)")
    conn.execute("CREATE INDEX IF NOT EXISTS idx_raw_sensor_events_kind ON raw_sensor_events(kind)")
    conn.commit()
    return conn


def save_reading(conn: sqlite3.Connection, device_address: str, r: RawReading) -> None:
    conn.execute(
        """
        INSERT INTO raw_sensor_events (
            time_utc, device_address, kind, payload_hex,
            acc_x, acc_y, acc_z, mag, move,
            ppg, ppg_max, ppg_min, ppg_diff,
            spo2_raw, spo2_max, spo2_min, spo2_diff
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        """,
        (
            r.time.isoformat(timespec="milliseconds"),
            device_address,
            r.kind,
            r.payload.hex(),
            r.acc_x,
            r.acc_y,
            r.acc_z,
            r.mag,
            r.move,
            r.ppg,
            r.ppg_max,
            r.ppg_min,
            r.ppg_diff,
            r.spo2_raw,
            r.spo2_max,
            r.spo2_min,
            r.spo2_diff,
        ),
    )
    conn.commit()


def print_reading(r: RawReading) -> None:
    ts = r.time.astimezone().strftime("%H:%M:%S.%f")[:-3]
    payload = r.payload.hex()
    if r.kind == "accelerometer":
        print(
            f"{ts} accel x={r.acc_x:5d} y={r.acc_y:5d} z={r.acc_z:5d} "
            f"mag={r.mag or 0:5.0f} move={r.move or 0:5.0f} payload={payload}",
            flush=True,
        )
    elif r.kind == "ppg":
        print(
            f"{ts} ppg raw={r.ppg} max={r.ppg_max} min={r.ppg_min} "
            f"diff={r.ppg_diff} payload={payload}",
            flush=True,
        )
    elif r.kind == "spo2_raw":
        print(
            f"{ts} spo2_raw raw={r.spo2_raw} max={r.spo2_max} min={r.spo2_min} "
            f"diff={r.spo2_diff} payload={payload}",
            flush=True,
        )
    else:
        print(f"{ts} {r.kind} payload={payload}", flush=True)


async def find_device(address: Optional[str], name: Optional[str], scan_timeout: float):
    if BleakScanner is None:
        raise RuntimeError("Missing dependency: bleak. Install with: python3 -m pip install bleak")
    if address:
        return address

    print(f"Scanning for watch/ring for {scan_timeout:g}s...", flush=True)
    name_lc = name.lower() if name else None

    def match(device, adv):
        if name_lc:
            dev_name = (device.name or adv.local_name or "").lower()
            return name_lc in dev_name
        service_uuids = [u.lower() for u in (adv.service_uuids or [])]
        return UART_SERVICE in service_uuids or ADV_SERVICE in service_uuids

    dev = await BleakScanner.find_device_by_filter(match, timeout=scan_timeout)
    if not dev:
        raise RuntimeError("No matching device found. Pass --address or --name.")
    print(f"Found {dev.address} name={dev.name!r}", flush=True)
    return dev.address


@dataclass
class RuntimeState:
    previous_accel: Optional[RawReading] = None
    seen: int = 0
    last_seen: float = 0.0


def make_notify_handler(conn: sqlite3.Connection, address: str, args: argparse.Namespace, state: RuntimeState):
    def on_notify(_sender, data: bytearray):
        raw = bytes(data)
        # Usually exactly one 16-byte packet. Handle multiples defensively.
        packets = [raw[i : i + PACKET_LEN] for i in range(0, len(raw), PACKET_LEN)]
        for packet in packets:
            if len(packet) < PACKET_LEN:
                continue
            r = parse_raw_sensor(packet, state.previous_accel)
            if not r:
                if args.print_unknown:
                    ts = datetime.now().astimezone().strftime("%H:%M:%S.%f")[:-3]
                    print(f"{ts} unknown payload={packet.hex()}", flush=True)
                continue
            if r.kind == "accelerometer":
                state.previous_accel = r
            save_reading(conn, address, r)
            print_reading(r)
            state.seen += 1
            state.last_seen = time.monotonic()

    return on_notify


def describe_error(e: Exception) -> str:
    text = str(e)
    if text:
        return f"{type(e).__name__}: {text}"
    return repr(e) or type(e).__name__


async def maybe_wait_until(deadline: Optional[float], seconds: float) -> None:
    if deadline is None:
        await asyncio.sleep(seconds)
        return
    remaining = deadline - time.monotonic()
    if remaining > 0:
        await asyncio.sleep(min(seconds, remaining))


async def cleanup_client(client, args: argparse.Namespace, stream_started: bool, notify_started: bool) -> None:
    if not getattr(client, "is_connected", False):
        return

    stop_packet = build_packet(CMD_RAW_SENSOR, RAW_STOP)

    if stream_started:
        try:
            await asyncio.wait_for(client.write_gatt_char(UART_WRITE, stop_packet, response=False), timeout=args.op_timeout)
        except Exception as e:
            print(f"Raw stop skipped/failed: {describe_error(e)}", flush=True)

    if notify_started:
        try:
            await asyncio.wait_for(client.stop_notify(UART_NOTIFY), timeout=args.op_timeout)
        except Exception as e:
            print(f"Stop notify skipped/failed: {describe_error(e)}", flush=True)

    try:
        await asyncio.wait_for(client.disconnect(), timeout=args.disconnect_timeout)
    except Exception as e:
        print(f"Disconnect skipped/failed: {describe_error(e)}", flush=True)


async def run_session(address: str, conn: sqlite3.Connection, args: argparse.Namespace, state: RuntimeState, deadline: Optional[float]) -> str:
    """Run one BLE connection. Return why it ended: disconnected/stalled/done/error."""
    start_packet = build_packet(CMD_RAW_SENSOR, bytes.fromhex(args.payload))
    disconnected = asyncio.Event()
    loop = asyncio.get_running_loop()

    def on_disconnect(_client):
        loop.call_soon_threadsafe(disconnected.set)

    client = BleakClient(address, disconnected_callback=on_disconnect)
    notify_started = False
    stream_started = False

    try:
        print(f"Connecting to {address}...", flush=True)
        await asyncio.wait_for(client.connect(), timeout=args.connect_timeout)
        if not client.is_connected:
            raise RuntimeError("BLE connect returned but client is not connected")

        print("Connected. Enabling notifications...", flush=True)
        await asyncio.wait_for(
            client.start_notify(UART_NOTIFY, make_notify_handler(conn, address, args, state)),
            timeout=args.op_timeout,
        )
        notify_started = True

        print(f"Starting raw sensors: {start_packet.hex()}  db={args.db}", flush=True)
        await asyncio.wait_for(client.write_gatt_char(UART_WRITE, start_packet, response=False), timeout=args.op_timeout)
        stream_started = True
        state.last_seen = time.monotonic()

        while True:
            if deadline is not None and time.monotonic() >= deadline:
                return "done"

            try:
                await asyncio.wait_for(disconnected.wait(), timeout=1.0)
                return "disconnected"
            except asyncio.TimeoutError:
                pass

            if args.watchdog > 0 and time.monotonic() - state.last_seen > args.watchdog:
                print(f"No raw packets for {args.watchdog:g}s; reconnecting BLE...", flush=True)
                return "stalled"
    finally:
        await cleanup_client(client, args, stream_started, notify_started)


async def run(args: argparse.Namespace) -> None:
    if BleakClient is None:
        raise RuntimeError("Missing dependency: bleak. Install with: python3 -m pip install bleak")

    address = await find_device(args.address, args.name, args.scan_timeout)
    conn = init_db(args.db)
    state = RuntimeState(last_seen=time.monotonic())
    deadline = time.monotonic() + args.seconds if args.seconds and args.seconds > 0 else None

    try:
        while deadline is None or time.monotonic() < deadline:
            try:
                reason = await run_session(address, conn, args, state, deadline)
                print(f"Session ended: {reason}; reconnecting in {args.reconnect_delay:g}s...", flush=True)
            except Exception as e:
                print(f"Session error: {describe_error(e)}; reconnecting in {args.reconnect_delay:g}s...", flush=True)
            await maybe_wait_until(deadline, args.reconnect_delay)
    finally:
        conn.close()
        print(f"Stopped. Saved {state.seen} parsed events to {args.db}", flush=True)

def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Log Colmi/Y25/RS25 A1 0101 raw sensor packets to SQLite")
    p.add_argument("--address", help="BLE address/identifier. If omitted, scan for matching service/name.")
    p.add_argument("--name", help="Substring of device name to scan for, e.g. Y25, R02, COLMI")
    p.add_argument("--db", default="raw_sensor.db", help="SQLite DB path, default: raw_sensor.db")
    p.add_argument("--seconds", type=float, default=0, help="Run duration. 0 means until Ctrl-C.")
    p.add_argument("--scan-timeout", type=float, default=10, help="Scan timeout when --address omitted")
    p.add_argument("--payload", default="0101", help="Raw A1 payload hex. Default: 0101")
    p.add_argument("--watchdog", type=float, default=10, help="Reconnect BLE after N seconds without packets. 0 disables.")
    p.add_argument("--reconnect-delay", type=float, default=5, help="Seconds to wait before reconnecting")
    p.add_argument("--connect-timeout", type=float, default=30, help="Timeout for BLE connect")
    p.add_argument("--op-timeout", type=float, default=5, help="Timeout for BLE write/notify operations")
    p.add_argument("--disconnect-timeout", type=float, default=3, help="Timeout for BLE disconnect")
    p.add_argument("--print-unknown", action="store_true", help="Print non-raw/unknown V1 notifications")
    return p.parse_args()


def main() -> None:
    args = parse_args()
    try:
        asyncio.run(run(args))
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
