from dataclasses import dataclass

from events import Event


@dataclass(slots=True)
class Snapshot:
    connected: bool = False
    battery: int | None = None
    charging: bool = False
    last_hr: int | None = None
    last_spo2: int | None = None
    last_steps: int | None = None
    last_calories: float | None = None
    last_distance: int | None = None
    last_sleep: list | None = None
    last_error: str | None = None

    def apply(self, ev: Event) -> None:
        match ev.kind:
            case "connected":
                self.connected = True
                self.last_error = None
            case "disconnected":
                self.connected = False
            case "battery":
                self.battery = ev.payload.level
                self.charging = ev.payload.charging
            case "hr":
                self.last_hr = ev.payload
            case "spo2":
                self.last_spo2 = ev.payload
            case "activity":
                self.last_steps = ev.payload.steps
                self.last_calories = ev.payload.calories
                self.last_distance = ev.payload.distance
            case "sleep":
                self.last_sleep = ev.payload
            case "error":
                self.last_error = str(ev.payload)
