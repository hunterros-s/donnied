#!/usr/bin/env python3
"""Plot movement features from raw0101_logger.py SQLite output.

Install/use:
    uv run --with matplotlib python3 scripts/plot_movement.py --db raw0101.db --hours 6 --out movement.png

Shows:
  1. raw 3-axis accelerometer values
  2. per-sample movement delta with thresholds
  3. acceleration magnitude + orientation angle change
  4. bucketed still/active fractions
  5. bucketed p95/max movement + reposition count
"""

from __future__ import annotations

import argparse
import math
import sqlite3
import statistics
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import Optional



@dataclass
class Sample:
    t: datetime
    x: int
    y: int
    z: int
    mag: float
    move: float
    angle: float = 0.0


@dataclass
class Bucket:
    t: datetime
    n: int
    mean_move: float
    median_move: float
    p95_move: float
    max_move: float
    still_fraction: float
    active_fraction: float
    max_angle: float
    reposition_count: int
    activity_count: float


def parse_time(s: str) -> datetime:
    dt = datetime.fromisoformat(s)
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt.astimezone()


def percentile(vals: list[float], p: float) -> float:
    if not vals:
        return 0.0
    vals = sorted(vals)
    if len(vals) == 1:
        return vals[0]
    k = (len(vals) - 1) * p
    lo = math.floor(k)
    hi = math.ceil(k)
    if lo == hi:
        return vals[int(k)]
    return vals[lo] * (hi - k) + vals[hi] * (k - lo)


def vector_angle_deg(a: Sample, b: Sample) -> float:
    amag = math.sqrt(a.x * a.x + a.y * a.y + a.z * a.z)
    bmag = math.sqrt(b.x * b.x + b.y * b.y + b.z * b.z)
    if amag <= 0 or bmag <= 0:
        return 0.0
    dot = a.x * b.x + a.y * b.y + a.z * b.z
    c = max(-1.0, min(1.0, dot / (amag * bmag)))
    return math.degrees(math.acos(c))


def load_samples(db: str, hours: Optional[float], since: Optional[str], limit: Optional[int]) -> list[Sample]:
    where = "kind = 'accelerometer' AND acc_x IS NOT NULL"
    params: list[object] = []
    if since:
        # Accept either an ISO timestamp or a local-ish datetime; compare as stored text.
        dt = parse_time(since).astimezone(timezone.utc)
        where += " AND time_utc >= ?"
        params.append(dt.isoformat(timespec="milliseconds"))
    elif hours and hours > 0:
        dt = datetime.now(timezone.utc) - timedelta(hours=hours)
        where += " AND time_utc >= ?"
        params.append(dt.isoformat(timespec="milliseconds"))

    sql = f"""
        SELECT time_utc, acc_x, acc_y, acc_z, mag, move
        FROM raw_sensor_events
        WHERE {where}
        ORDER BY time_utc ASC
    """
    if limit and limit > 0:
        sql += " LIMIT ?"
        params.append(limit)

    rows = sqlite3.connect(db).execute(sql, params).fetchall()
    samples: list[Sample] = []
    prev: Optional[Sample] = None
    for time_utc, x, y, z, mag, move in rows:
        if mag is None:
            mag = math.sqrt(x * x + y * y + z * z)
        if move is None and prev is not None:
            dx, dy, dz = x - prev.x, y - prev.y, z - prev.z
            move = math.sqrt(dx * dx + dy * dy + dz * dz)
        elif move is None:
            move = 0.0
        s = Sample(parse_time(time_utc), int(x), int(y), int(z), float(mag), float(move))
        if prev is not None:
            s.angle = vector_angle_deg(prev, s)
        samples.append(s)
        prev = s
    return samples


def make_buckets(samples: list[Sample], bucket_seconds: int, still: float, active: float, angle_reposition: float) -> list[Bucket]:
    grouped: dict[int, list[Sample]] = {}
    for s in samples:
        key = int(s.t.timestamp()) // bucket_seconds * bucket_seconds
        grouped.setdefault(key, []).append(s)

    out: list[Bucket] = []
    for key in sorted(grouped):
        ss = grouped[key]
        moves = [s.move for s in ss]
        angles = [s.angle for s in ss]
        reposition_count = sum(1 for s in ss if s.move >= active or s.angle >= angle_reposition)
        out.append(
            Bucket(
                t=datetime.fromtimestamp(key, tz=samples[0].t.tzinfo),
                n=len(ss),
                mean_move=statistics.fmean(moves),
                median_move=statistics.median(moves),
                p95_move=percentile(moves, 0.95),
                max_move=max(moves),
                still_fraction=sum(1 for m in moves if m < still) / len(moves),
                active_fraction=sum(1 for m in moves if m >= active) / len(moves),
                max_angle=max(angles) if angles else 0.0,
                reposition_count=reposition_count,
                activity_count=sum(min(m, 1000.0) for m in moves),
            )
        )
    return out


def plot(samples: list[Sample], buckets: list[Bucket], args: argparse.Namespace) -> None:
    try:
        import matplotlib.dates as mdates
        import matplotlib.pyplot as plt
    except ImportError as e:  # pragma: no cover
        raise SystemExit("Missing dependency: matplotlib. Try: uv run --with matplotlib python3 scripts/plot_movement.py ...") from e

    if not samples:
        raise SystemExit("No accelerometer samples found in DB/time range")

    ts = [s.t for s in samples]
    xs = [s.x for s in samples]
    ys = [s.y for s in samples]
    zs = [s.z for s in samples]
    mags = [s.mag for s in samples]
    moves = [s.move for s in samples]
    angles = [s.angle for s in samples]

    bt = [b.t for b in buckets]
    still_frac = [b.still_fraction for b in buckets]
    active_frac = [b.active_fraction for b in buckets]
    p95 = [b.p95_move for b in buckets]
    max_move = [b.max_move for b in buckets]
    repos = [b.reposition_count for b in buckets]

    fig, axes = plt.subplots(5, 1, figsize=(15, 12), sharex=True, constrained_layout=True)
    fig.suptitle(f"Movement features from {args.db}  ({len(samples)} accel samples, {len(buckets)} buckets)")

    ax = axes[0]
    ax.plot(ts, xs, label="x", linewidth=0.8)
    ax.plot(ts, ys, label="y", linewidth=0.8)
    ax.plot(ts, zs, label="z", linewidth=0.8)
    ax.set_ylabel("accel axes")
    ax.legend(loc="upper right", ncols=3)
    ax.grid(True, alpha=0.25)

    ax = axes[1]
    ax.plot(ts, moves, color="tab:red", linewidth=0.9, label="move = sqrt(dx²+dy²+dz²)")
    ax.axhline(args.still_threshold, color="tab:green", linestyle="--", linewidth=1, label=f"still < {args.still_threshold:g}")
    ax.axhline(args.active_threshold, color="tab:orange", linestyle="--", linewidth=1, label=f"active/reposition ≥ {args.active_threshold:g}")
    ax.set_ylabel("move")
    ax.legend(loc="upper right")
    ax.grid(True, alpha=0.25)

    ax = axes[2]
    ax.plot(ts, mags, color="tab:blue", linewidth=0.8, label="|accel| magnitude")
    ax.set_ylabel("magnitude")
    ax2 = ax.twinx()
    ax2.plot(ts, angles, color="tab:purple", linewidth=0.7, alpha=0.65, label="orientation Δ angle")
    ax2.set_ylabel("angle deg")
    lines, labels = ax.get_legend_handles_labels()
    lines2, labels2 = ax2.get_legend_handles_labels()
    ax.legend(lines + lines2, labels + labels2, loc="upper right")
    ax.grid(True, alpha=0.25)

    ax = axes[3]
    ax.plot(bt, still_frac, color="tab:green", marker=".", label="still fraction")
    ax.plot(bt, active_frac, color="tab:orange", marker=".", label="active fraction")
    ax.set_ylim(-0.05, 1.05)
    ax.set_ylabel(f"bucket\n{args.bucket}s")
    ax.legend(loc="upper right")
    ax.grid(True, alpha=0.25)

    ax = axes[4]
    ax.plot(bt, p95, color="tab:red", marker=".", label="p95 move")
    ax.plot(bt, max_move, color="tab:pink", marker=".", alpha=0.7, label="max move")
    ax.set_ylabel("bucket move")
    ax2 = ax.twinx()
    ax2.bar(bt, repos, width=args.bucket / 86400 * 0.75, color="tab:gray", alpha=0.35, label="reposition count")
    ax2.set_ylabel("repositions")
    lines, labels = ax.get_legend_handles_labels()
    lines2, labels2 = ax2.get_legend_handles_labels()
    ax.legend(lines + lines2, labels + labels2, loc="upper right")
    ax.grid(True, alpha=0.25)

    axes[-1].xaxis.set_major_formatter(mdates.DateFormatter("%H:%M"))
    axes[-1].set_xlabel("local time")

    if args.out:
        fig.savefig(args.out, dpi=args.dpi)
        print(f"wrote {args.out}")
    if args.show or not args.out:
        plt.show()


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Graph movement features from raw0101.db")
    p.add_argument("--db", default="raw0101.db", help="SQLite DB from raw0101_logger.py")
    p.add_argument("--hours", type=float, default=6, help="Plot last N hours. 0 = all. Default: 6")
    p.add_argument("--since", help="ISO timestamp lower bound; overrides --hours")
    p.add_argument("--limit", type=int, help="Limit accelerometer rows after filtering")
    p.add_argument("--bucket", type=int, default=60, help="Bucket size in seconds. Default: 60")
    p.add_argument("--still-threshold", type=float, default=30, help="move below this counts as still")
    p.add_argument("--active-threshold", type=float, default=150, help="move above this counts as reposition/active")
    p.add_argument("--angle-threshold", type=float, default=20, help="angle change above this counts as reposition")
    p.add_argument("--out", default="movement.png", help="Output PNG path. Omit with --out '' to show only")
    p.add_argument("--show", action="store_true", help="Show interactive window after saving")
    p.add_argument("--dpi", type=int, default=150)
    return p.parse_args()


def main() -> None:
    args = parse_args()
    if args.hours == 0:
        args.hours = None
    if args.out == "":
        args.out = None
    samples = load_samples(args.db, args.hours, args.since, args.limit)
    buckets = make_buckets(samples, args.bucket, args.still_threshold, args.active_threshold, args.angle_threshold)
    plot(samples, buckets, args)


if __name__ == "__main__":
    main()
