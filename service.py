from state import Snapshot
from store import query


class Service:
    __slots__ = ("_snapshot",)

    def __init__(self, snapshot: Snapshot) -> None:
        self._snapshot = snapshot

    def get_snapshot(self) -> Snapshot:
        return self._snapshot

    async def query_events(self, kind: str | None = None, limit: int = 100) -> list[dict]:
        if kind:
            sql = "SELECT id, kind, hex, payload, timestamp FROM events WHERE kind = ? ORDER BY timestamp DESC LIMIT ?"
            rows = await query(sql, (kind, limit))
        else:
            sql = "SELECT id, kind, hex, payload, timestamp FROM events ORDER BY timestamp DESC LIMIT ?"
            rows = await query(sql, (limit,))
        return rows
