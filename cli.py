#!/usr/bin/env python3
"""
CLI for the smartwatch daemon. Talks to the local HTTP API.
"""

import argparse
import json
import sys
from urllib.error import URLError
from urllib.request import Request, urlopen

from events import HOST, PORT

_BASE = f"http://{HOST}:{PORT}"

COMMANDS = {
    "battery": "battery",
    "hr-start": "hr_start",
    "hr-stop": "hr_stop",
    "spo2-start": "spo2_start",
    "spo2-stop": "spo2_stop",
    "sleep": "sleep_fetch",
}


def _api(path: str, method: str = "GET", data: dict | None = None) -> dict:
    req = Request(
        _BASE + path,
        method=method,
        data=json.dumps(data).encode() if data else None,
        headers={"Content-Type": "application/json"} if data else {},
    )
    try:
        with urlopen(req) as resp:
            return json.loads(resp.read())
    except URLError as e:
        print(f"daemon not running: {e}", file=sys.stderr)
        raise SystemExit(1)


def _status(_args: argparse.Namespace) -> None:
    s = _api("/state")
    print(f"connected: {s['connected']}")
    print(f"battery:   {s['battery']}% {'(charging)' if s['charging'] else ''}")
    print(f"last hr:   {s['last_hr']}")
    print(f"last spo2: {s['last_spo2']}")
    if s.get("last_steps") is not None:
        print(f"activity:  {s['last_steps']} steps, {s['last_calories']} kcal, {s['last_distance']} m")
    if s["last_error"]:
        print(f"error:     {s['last_error']}")


def _events(args: argparse.Namespace) -> None:
    qs = f"?limit={args.limit}"
    if args.kind:
        qs += f"&kind={args.kind}"
    rows = _api("/events" + qs)
    for e in rows:
        ts = e.get("timestamp") or "?"
        payload = e.get("payload") or e.get("hex") or ""
        print(f"{ts}  [{e['kind']}]  {str(payload)[:80]}")


def _daemon(_args: argparse.Namespace) -> None:
    import asyncio
    from daemon import main

    asyncio.run(main())


def main() -> None:
    parser = argparse.ArgumentParser(description="Smartwatch CLI")
    sub = parser.add_subparsers(dest="cmd", required=True)

    for name in COMMANDS:
        sub.add_parser(name)

    sub.add_parser("status", help="Show daemon state")
    sub.add_parser("daemon", help="Run the daemon")

    p = sub.add_parser("events", help="Show recent events")
    p.add_argument("-n", "--limit", type=int, default=20)
    p.add_argument("-k", "--kind", default=None)

    args = parser.parse_args()
    if args.cmd in COMMANDS:
        _api("/command", method="POST", data={"action": COMMANDS[args.cmd]})
        print("ok")
    elif args.cmd == "status":
        _status(args)
    elif args.cmd == "events":
        _events(args)
    elif args.cmd == "daemon":
        _daemon(args)


if __name__ == "__main__":
    main()
