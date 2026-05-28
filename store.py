import asyncio
from datetime import datetime
from pathlib import Path

import aiosqlite

from events import Event, dumps_payload

DB_PATH = Path(__file__).parent / "events.db"


async def _init_db(db: aiosqlite.Connection) -> None:
    await db.execute(
        """
        CREATE TABLE IF NOT EXISTS events (
            id INTEGER PRIMARY KEY,
            kind TEXT NOT NULL,
            raw BLOB,
            hex TEXT,
            payload TEXT,
            timestamp TEXT NOT NULL
        )
        """
    )
    await db.execute("CREATE INDEX IF NOT EXISTS idx_ts ON events(timestamp)")
    await db.execute("CREATE INDEX IF NOT EXISTS idx_kind ON events(kind)")


async def run(bus: asyncio.Queue[Event]) -> None:
    async with aiosqlite.connect(DB_PATH) as db:
        await _init_db(db)
        await db.commit()
        while True:
            ev = await bus.get()
            raw = ev.payload if isinstance(ev.payload, (bytes, bytearray)) else None
            hex_val = raw.hex() if raw else None
            payload_val = None if raw else dumps_payload(ev.payload)
            await db.execute(
                "INSERT INTO events (kind, raw, hex, payload, timestamp) VALUES (?, ?, ?, ?, ?)",
                (ev.kind, bytes(raw) if raw else None, hex_val, payload_val, ev.ts.isoformat()),
            )
            await db.commit()


async def query(sql: str, params: tuple = ()) -> list[dict]:
    async with aiosqlite.connect(DB_PATH) as db:
        db.row_factory = aiosqlite.Row
        async with db.execute(sql, params) as cursor:
            rows = await cursor.fetchall()
            return [dict(r) for r in rows]
