#!/usr/bin/env python3
"""
Daemon orchestrator. Runs BLE, store, and web workers and shuts them down
cleanly on SIGINT/SIGTERM.
"""

import asyncio
import signal

from events import Event
from ble import run as ble_run
from service import Service
from state import Snapshot
from store import run as store_run
from web import run as web_run, Broadcaster


class _Shutdown(Exception):
    """Raised inside the TaskGroup to trigger sibling cancellation."""


async def _dispatch(
    raw_bus: asyncio.Queue[Event],
    store_bus: asyncio.Queue[Event],
    snapshot: Snapshot,
    broadcaster: Broadcaster,
) -> None:
    while True:
        ev = await raw_bus.get()
        snapshot.apply(ev)
        broadcaster.publish(ev)
        store_bus.put_nowait(ev)


async def _watch_signals(stop: asyncio.Event) -> None:
    await stop.wait()
    raise _Shutdown


async def main() -> None:
    raw_bus: asyncio.Queue[Event] = asyncio.Queue()
    store_bus: asyncio.Queue[Event] = asyncio.Queue()
    cmd_bus: asyncio.Queue[dict] = asyncio.Queue()

    snapshot = Snapshot()
    broadcaster = Broadcaster()
    service = Service(snapshot)

    stop = asyncio.Event()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, stop.set)

    try:
        async with asyncio.TaskGroup() as tg:
            tg.create_task(_watch_signals(stop))
            tg.create_task(_dispatch(raw_bus, store_bus, snapshot, broadcaster))
            tg.create_task(ble_run(raw_bus, cmd_bus))
            tg.create_task(store_run(store_bus))
            tg.create_task(web_run(service, broadcaster, cmd_bus))
    except* _Shutdown:
        pass


if __name__ == "__main__":
    asyncio.run(main())
