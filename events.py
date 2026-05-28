from dataclasses import dataclass
from datetime import datetime
from typing import Any
import json


HOST = "127.0.0.1"
PORT = 8080


@dataclass(frozen=True, slots=True)
class Event:
    kind: str
    ts: datetime
    payload: Any


def serialize_payload(payload: object) -> object:
    """Convert a payload to a JSON-serializable object."""
    if isinstance(payload, (bytes, bytearray)):
        return payload.hex()
    if hasattr(payload, "__dict__"):
        return payload.__dict__
    return payload


def dumps_payload(payload: object) -> str | None:
    """Serialize payload to a JSON string. Returns None for raw bytes."""
    if isinstance(payload, (bytes, bytearray)):
        return None
    if payload is None:
        return None
    try:
        return json.dumps(serialize_payload(payload))
    except TypeError:
        return str(payload)
