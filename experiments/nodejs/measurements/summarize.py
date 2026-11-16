#!/usr/bin/env python3
"""Derive summaries from the retained raw trials, without rerunning workloads."""
import json
from pathlib import Path
import statistics

out = Path(__file__).resolve().parent
runs = json.loads((out / 'results.json').read_text())
host = json.loads((out / 'host.json').read_text())
summary = {}
for technology in ['go', 'node']:
    summary[technology] = {}
    for scenario in ['idle', 'manual', 'scheduled']:
        trials = [r for r in runs if r['technology'] == technology and r['scenario'] == scenario]
        if not trials:
            continue
        keys = ['rss_median_mib', 'cpu_percent_one_core', 'cpu_time_s', 'process_lifetime_peak_rss_mib']
        metrics = {key: {'median': statistics.median(r['metrics'][key] for r in trials),
                         'min': min(r['metrics'][key] for r in trials),
                         'max': max(r['metrics'][key] for r in trials)} for key in keys}
        metrics['readiness_s'] = {'median': statistics.median(r['readiness_s'] for r in trials),
                                  'min': min(r['readiness_s'] for r in trials),
                                  'max': max(r['readiness_s'] for r in trials)}
        metrics['repetitions'] = len(trials)
        metrics['threads'] = [min(r['metrics']['threads_min'] for r in trials), max(r['metrics']['threads_max'] for r in trials)]
        summary[technology][scenario] = metrics
    summary[technology]['image'] = host['images'][technology]
(out / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
print('| Scenario | Go RSS MiB | Node RSS MiB | Go CPU % | Node CPU % |')
print('|---|---:|---:|---:|---:|')
for scenario in ['idle', 'manual', 'scheduled']:
    if scenario not in summary['go'] or scenario not in summary['node']:
        continue
    go = summary['go'][scenario]
    node = summary['node'][scenario]
    print(f"| {scenario} | {go['rss_median_mib']['median']:.2f} | {node['rss_median_mib']['median']:.2f} | {go['cpu_percent_one_core']['median']:.3f} | {node['cpu_percent_one_core']['median']:.3f} |")
for technology in ['go', 'node']:
    size = summary[technology]['image']['Size']
    print(f'{technology} image: {size} bytes, {size / 1024**2:.2f} MiB, {size / 10**6:.2f} MB')
