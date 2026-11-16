#!/usr/bin/env python3
"""Summarize procfs samples; CPU percentages are relative to one core."""
import json
import statistics
from pathlib import Path

root = Path(__file__).resolve().parent


def read(name):
    return [json.loads(line) for line in (root / name).read_text().splitlines()]


def summary(rows, lo=None, hi=None):
    if lo is not None:
        first = min(range(len(rows)), key=lambda i: abs(rows[i]['elapsed_s'] - lo))
        last = min(range(len(rows)), key=lambda i: abs(rows[i]['elapsed_s'] - hi))
        rows = rows[first:last + 1]
    duration = rows[-1]['elapsed_s'] - rows[0]['elapsed_s']
    cpu = rows[-1]['cpu_time_s'] - rows[0]['cpu_time_s']
    return {
        'samples': len(rows), 'duration_s': duration, 'cpu_seconds_delta': cpu,
        'cpu_pct_one_core': 100 * cpu / duration,
        'RSS_MiB_min': min(x['VmRSS'] for x in rows) / 1024,
        'RSS_MiB_median': statistics.median(x['VmRSS'] for x in rows) / 1024,
        'RSS_MiB_max': max(x['VmRSS'] for x in rows) / 1024,
        'process_lifetime_peak_RSS_MiB': max(x['VmHWM'] for x in rows) / 1024,
        'swap_MiB_max': max(x['VmSwap'] for x in rows) / 1024,
        'threads_min': min(x['Threads'] for x in rows),
        'threads_max': max(x['Threads'] for x in rows),
        'first_utc': rows[0]['utc'], 'last_utc': rows[-1]['utc'],
    }


go = read('go-idle.jsonl')
kotlin = read('kotlin-live.jsonl')
result = {
    'go_idle': summary(go),
    'kotlin_full': summary(kotlin),
    'kotlin_warmup': summary(kotlin, 0, 60),
    'kotlin_steady': summary(kotlin, 60, 180),
    'kotlin_warmed': summary(kotlin, 60, 300),
    'kotlin_late_window': summary(kotlin, 180, 300),
    'kotlin_cpu_lifetime_seconds_at_end': kotlin[-1]['cpu_time_s'],
    'measurement': {'cpu_unit': 'percent of one core', 'memory_unit': 'MiB',
                    'source': 'main-process procfs status/stat, all threads',
                    'sampling_interval_seconds': 5, 'clock_ticks_per_second': 100},
}
(root / 'summary.json').write_text(json.dumps(result, indent=2) + '\n')
print(json.dumps(result, indent=2))
