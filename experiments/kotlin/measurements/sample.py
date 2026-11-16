#!/usr/bin/env python3
"""Sample one Linux process using procfs; CPU is percent of one core."""
import argparse
import datetime
import json
import os
import time
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("--pid", type=int, required=True)
parser.add_argument("--seconds", type=float, required=True)
parser.add_argument("--interval", type=float, default=5)
parser.add_argument("--output", required=True)
args = parser.parse_args()
root = Path(f"/proc/{args.pid}")
hz = os.sysconf("SC_CLK_TCK")
start = time.monotonic()
previous = None
with open(args.output, "w") as output:
    while True:
        elapsed = time.monotonic() - start
        status = {}
        for line in (root / "status").read_text().splitlines():
            key, _, value = line.partition(":")
            if key in {"VmRSS", "VmHWM", "VmSwap", "Threads", "RssAnon", "RssFile"}:
                status[key] = int(value.strip().split()[0])
        stat = (root / "stat").read_text().rsplit(")", 1)[1].split()
        cpu = (int(stat[11]) + int(stat[12])) / hz
        record = {"utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
                  "elapsed_s": elapsed, "cpu_time_s": cpu, **status}
        if previous:
            record["cpu_pct_one_core"] = 100 * (cpu - previous[1]) / (elapsed - previous[0])
        previous = elapsed, cpu
        output.write(json.dumps(record) + "\n")
        output.flush()
        if elapsed >= args.seconds:
            break
        time.sleep(min(args.interval, args.seconds - elapsed))
