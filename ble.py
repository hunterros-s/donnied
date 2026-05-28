import asyncio
import json
from datetime import datetime
from pathlib import Path
from typing import Any

from bleak import BleakClient, BleakScanner

from events import Event
import protocol as p

WATCH_NAME = "Y25_41CB"
_STATE_FILE = Path(__file__).parent / ".state.json"


def _load_address() -> str | None:
    if not _STATE_FILE.exists():
        return None
    return json.loads(_STATE_FILE.read_text()).get("address")


def _save_address(addr: str) -> None:
    _STATE_FILE.write_text(json.dumps({"address": addr}))


async def _connect() -> BleakClient:
    addr = _load_address()
    if not addr:
        devices = await BleakScanner.discover(timeout=15)
        dev = next((d for d in devices if d.name == WATCH_NAME), None)
        if not dev:
            raise RuntimeError("Watch not found")
        addr = dev.address
        _save_address(addr)
    client = BleakClient(addr)
    await client.connect()
    return client


def _parse_and_publish(bus: asyncio.Queue[Event], data: bytearray) -> None:
    if len(data) < 1:
        return
    cmd = data[0]
    ts = datetime.now()

    if cmd == p.Cmd.BATTERY and len(data) >= 3:
        b = p.parse_battery(data)
        if b:
            bus.put_nowait(Event("battery", ts, b))
    elif cmd == p.Cmd.NOTIFICATION and len(data) >= 2:
        parsed = p.parse_notification(data)
        if not parsed:
            return
        ntype, extra = parsed
        if ntype == p.Notify.NEW_HR and extra is not None:
            bus.put_nowait(Event("hr", ts, extra))
        elif ntype == p.Notify.NEW_SPO2 and extra is not None:
            bus.put_nowait(Event("spo2", ts, extra))
        elif ntype == p.Notify.BATTERY and extra is not None:
            bus.put_nowait(Event("battery", ts, extra))
        elif ntype == p.Notify.LIVE_ACTIVITY and extra is not None:
            bus.put_nowait(Event("activity", ts, extra))
        else:
            bus.put_nowait(Event("notify", ts, {"type": ntype, "raw": data.hex()}))
    elif cmd == p.Cmd.MANUAL_HR and len(data) >= 4:
        reading = p.parse_realtime(data)
        if isinstance(reading, p.RtReading):
            ev_kind = {p.RtType.HEART_RATE: "hr", p.RtType.SPO2: "spo2"}.get(
                reading.kind, "rt"
            )
            bus.put_nowait(Event(ev_kind, ts, reading.value))
        elif isinstance(reading, p.RtError):
            bus.put_nowait(Event("rt_error", ts, {"kind": reading.kind.name, "code": reading.code}))
    else:
        bus.put_nowait(Event("unknown", ts, {"cmd": cmd, "raw": data.hex()}))


def _sleep_session_dict(s: p.SleepSession) -> dict:
    return {
        "start": s.start.isoformat(),
        "end": s.end.isoformat(),
        "stages": [{"stage": st, "minutes": dur} for st, dur in s.stages],
    }


def _handle_v2_frame(bus: asyncio.Queue[Event], frame: bytes) -> None:
    if len(frame) < 2:
        return
    ts = datetime.now()
    cmd_id = frame[1]
    if cmd_id == p.V2Cmd.SLEEP:
        sessions = p.parse_sleep_data(bytearray(frame))
        bus.put_nowait(
            Event("sleep", ts, [_sleep_session_dict(s) for s in sessions])
        )


def _setup_v2(client: BleakClient) -> tuple[Any, Any]:
    v2 = client.services.get_service(p.SERVICE_V2)
    if not v2:
        return (None, None)
    notify_char = v2.get_characteristic(p.CHAR_NOTIFY_V2)
    cmd_char = v2.get_characteristic(p.CHAR_COMMAND_V2)
    return (notify_char, cmd_char)


async def run(
    event_bus: asyncio.Queue[Event], cmd_bus: asyncio.Queue[dict]
) -> None:
    while True:
        client: BleakClient | None = None
        try:
            client = await _connect()
            event_bus.put_nowait(Event("connected", datetime.now(), {}))

            rx = (
                client.services.get_service(p.UART_SERVICE)
                .get_characteristic(p.UART_RX)
            )

            def on_v1(_c, data: bytearray) -> None:
                event_bus.put_nowait(Event("raw_v1", datetime.now(), bytes(data)))
                _parse_and_publish(event_bus, data)

            await client.start_notify(p.UART_TX, on_v1)

            v2_notify, v2_cmd_char = _setup_v2(client)
            if v2_notify:
                reassembler = p.V2Reassembler()

                def on_v2(_c, data: bytearray) -> None:
                    raw = bytes(data)
                    event_bus.put_nowait(Event("raw_v2", datetime.now(), raw))
                    for frame in reassembler.feed(raw):
                        _handle_v2_frame(event_bus, frame)

                await client.start_notify(v2_notify, on_v2)

            await client.write_gatt_char(rx, p.battery_packet(), response=False)

            while client.is_connected:
                try:
                    cmd = await asyncio.wait_for(cmd_bus.get(), timeout=300.0)
                except asyncio.TimeoutError:
                    await client.write_gatt_char(
                        rx, p.battery_packet(), response=False
                    )
                    continue

                match cmd.get("action"):
                    case "battery":
                        await client.write_gatt_char(
                            rx, p.battery_packet(), response=False
                        )
                    case "hr_start":
                        await client.write_gatt_char(
                            rx,
                            p.rt_start_packet(p.RtType.HEART_RATE),
                            response=False,
                        )
                    case "hr_stop":
                        await client.write_gatt_char(
                            rx,
                            p.rt_stop_packet(p.RtType.HEART_RATE),
                            response=False,
                        )
                    case "spo2_start":
                        await client.write_gatt_char(
                            rx,
                            p.rt_start_packet(p.RtType.SPO2),
                            response=False,
                        )
                    case "spo2_stop":
                        await client.write_gatt_char(
                            rx,
                            p.rt_stop_packet(p.RtType.SPO2),
                            response=False,
                        )
                    case "sleep_fetch" if v2_cmd_char:
                        await client.write_gatt_char(
                            v2_cmd_char, p.sleep_request_packet(), response=False
                        )

        except asyncio.CancelledError:
            raise
        except Exception as e:
            event_bus.put_nowait(Event("error", datetime.now(), str(e)))
        finally:
            if client:
                try:
                    await asyncio.shield(client.disconnect())
                except Exception:
                    pass
            try:
                event_bus.put_nowait(Event("disconnected", datetime.now(), {}))
            except asyncio.QueueFull:
                pass

        await asyncio.sleep(10)
