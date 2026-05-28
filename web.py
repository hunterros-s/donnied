import asyncio
import json
from pathlib import Path
from typing import AsyncIterator

from starlette.applications import Starlette
from starlette.responses import FileResponse, JSONResponse, StreamingResponse
from starlette.routing import Route

from events import Event, HOST, PORT, serialize_payload

_INDEX = Path(__file__).parent / "index.html"


class Broadcaster:
    __slots__ = ("_clients",)

    def __init__(self) -> None:
        self._clients: list[asyncio.Queue[Event]] = []

    def publish(self, ev: Event) -> None:
        self._clients = [q for q in self._clients if _try_put(q, ev)]

    def subscribe(self) -> asyncio.Queue[Event]:
        q: asyncio.Queue[Event] = asyncio.Queue(maxsize=100)
        self._clients.append(q)
        return q

    def unsubscribe(self, q: asyncio.Queue[Event]) -> None:
        if q in self._clients:
            self._clients.remove(q)


def _try_put(q: asyncio.Queue[Event], ev: Event) -> bool:
    try:
        q.put_nowait(ev)
        return True
    except asyncio.QueueFull:
        return False


async def _index(request) -> FileResponse:
    return FileResponse(_INDEX)


async def _stream(request) -> StreamingResponse:
    broadcaster: Broadcaster = request.app.state.broadcaster
    queue = broadcaster.subscribe()

    async def gen() -> AsyncIterator[bytes]:
        try:
            while True:
                ev = await queue.get()
                payload = json.dumps({
                    "ts": ev.ts.isoformat(),
                    "payload": serialize_payload(ev.payload),
                })
                yield f"event: {ev.kind}\ndata: {payload}\n\n".encode()
        finally:
            broadcaster.unsubscribe(queue)

    return StreamingResponse(gen(), media_type="text/event-stream")


async def _state(request) -> JSONResponse:
    snap = request.app.state.service.get_snapshot()
    return JSONResponse(
        {
            "connected": snap.connected,
            "battery": snap.battery,
            "charging": snap.charging,
            "last_hr": snap.last_hr,
            "last_spo2": snap.last_spo2,
            "last_steps": snap.last_steps,
            "last_calories": snap.last_calories,
            "last_distance": snap.last_distance,
            "last_sleep": snap.last_sleep,
            "last_error": snap.last_error,
        }
    )


async def _command(request) -> JSONResponse:
    body = await request.json()
    request.app.state.cmd_bus.put_nowait(body)
    return JSONResponse({"ok": True})


async def _events(request) -> JSONResponse:
    svc = request.app.state.service
    kind = request.query_params.get("kind")
    limit = min(int(request.query_params.get("limit", "100")), 1000)
    rows = await svc.query_events(kind=kind, limit=limit)
    return JSONResponse(rows)


async def run(service, broadcaster: Broadcaster, cmd_bus: asyncio.Queue[dict]) -> None:
    app = Starlette(
        routes=[
            Route("/", _index),
            Route("/stream", _stream),
            Route("/state", _state),
            Route("/command", _command, methods=["POST"]),
            Route("/events", _events),
        ]
    )
    app.state.service = service
    app.state.broadcaster = broadcaster
    app.state.cmd_bus = cmd_bus

    import uvicorn

    config = uvicorn.Config(
        app,
        host=HOST,
        port=PORT,
        log_level="warning",
        timeout_graceful_shutdown=5,
    )
    server = uvicorn.Server(config)
    await server.serve()
