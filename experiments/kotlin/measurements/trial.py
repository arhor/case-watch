#!/usr/bin/env python3
"""Bounded live trial. Always stop Kotlin and restore the existing Go container."""
import datetime
import json
import os
from pathlib import Path
import signal
import sqlite3
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[3]
OUT = Path(__file__).resolve().parent
PRIVATE = Path('/tmp/case-watch-kotlin-trial')
GO = 'case-watch-case-watch-1'
VOLUME = 'case-watch-kotlin-measurement-data'
COMPOSE = ['docker', 'compose', '--env-file', str(ROOT / '.env'), '-p',
           'case-watch-kotlin-experiment', '-f', str(ROOT / 'experiments/kotlin/compose.yaml'), '-f', str(OUT / 'compose.measurement.yaml')]
events = []


def run(command, check=True):
    result = subprocess.run(command, capture_output=True, text=True, cwd=ROOT)
    if check and result.returncode:
        raise RuntimeError('command failed: ' + ' '.join(command[:3]))
    return result


def event(name, **fields):
    record = {'utc': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'event': name, **fields}
    events.append(record)
    (OUT / 'events.json').write_text(json.dumps(events, indent=2) + '\n')
    print(json.dumps(record), flush=True)


def interrupted(signum, frame):
    raise SystemExit('trial interrupted')


signal.signal(signal.SIGTERM, interrupted)
signal.signal(signal.SIGINT, interrupted)
PRIVATE.mkdir(mode=0o700, exist_ok=True)
os.chmod(PRIVATE, 0o700)
kotlin_id = None
try:
    run(['systemctl', '--user', 'stop', 'case-watch-update.timer'])
    run(['systemctl', '--user', 'stop', 'case-watch-update.service'])
    run(['docker', 'stop', '--time', '90', GO])
    event('go_stopped')
    snapshot = PRIVATE / 'go-snapshot'
    snapshot.mkdir(mode=0o700, exist_ok=True)
    run(['docker', 'cp', GO + ':/data/.', str(snapshot)])
    source = sqlite3.connect('file:' + str(snapshot / 'case-watch.db') + '?mode=ro', uri=True)
    assert source.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
    target_path = PRIVATE / 'kotlin.db'
    if target_path.exists():
        raise RuntimeError('trial target already exists; refusing overwrite')
    target = sqlite3.connect(target_path)
    target.executescript((ROOT / 'experiments/kotlin/src/main/resources/migrations/001.sql').read_text())
    counts = {}
    for table in ('enrollments', 'history', 'cooldowns'):
        rows = source.execute('SELECT * FROM ' + table).fetchall()
        if rows:
            target.executemany('INSERT INTO ' + table + ' VALUES (' + ','.join('?' * len(rows[0])) + ')', rows)
        counts[table] = len(rows)
    target.execute('PRAGMA user_version=1')
    target.commit()
    assert target.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
    active = target.execute('SELECT COUNT(*) FROM enrollments WHERE deleted_at IS NULL').fetchone()[0]
    target.close()
    source.close()
    os.chmod(target_path, 0o600)
    run(['docker', 'volume', 'create', VOLUME])
    # Refuse to overwrite a prior measurement database.
    run(['docker', 'run', '--rm', '--network', 'none', '-v', VOLUME + ':/data',
         '-v', str(PRIVATE) + ':/transfer:ro', '--entrypoint', 'sh',
         'case-watch-kotlin:0.1.0-alpine', '-c',
         'test ! -e /data/case-watch.db && cp /transfer/kotlin.db /data/case-watch.db && chmod 600 /data/case-watch.db'])
    event('independent_database_seeded', counts=counts, active_cases=active, volume=VOLUME)
    launch = time.monotonic()
    run(COMPOSE + ['up', '-d', '--no-build'])
    kotlin_id = run(COMPOSE + ['ps', '-q', 'case-watch-kotlin']).stdout.strip()
    state = json.loads(run(['docker', 'inspect', kotlin_id]).stdout)[0]
    pid = state['State']['Pid']
    event('kotlin_started', pid=pid, launch_command_seconds=time.monotonic() - launch,
          image=state['Image'], cpu_quota_nanocpus=state['HostConfig']['NanoCpus'],
          effective_memory_limit_bytes=state['HostConfig']['Memory'],
          pid_limit=state['HostConfig']['PidsLimit'], duration_seconds=300,
          phases={'warmup': [0, 60], 'steady': [60, 180], 'manual_activity_window': [180, 300]})
    sampler = subprocess.Popen([sys.executable, str(OUT / 'sample.py'), '--pid', str(pid),
                               '--seconds', '300', '--interval', '5',
                               '--output', str(OUT / 'kotlin-live.jsonl')])
    sample_start = time.monotonic()
    last_phase = None
    try:
        while sampler.poll() is None:
            info = json.loads(run(['docker', 'inspect', kotlin_id]).stdout)[0]
            if not info['State']['Running'] or info['RestartCount'] != 0 or info['State']['Pid'] != pid:
                raise RuntimeError('Kotlin stopped or restarted during the trial')
            elapsed = time.monotonic() - sample_start
            phase = 'warmup' if elapsed < 60 else 'steady' if elapsed < 180 else 'manual_activity_window'
            if phase != last_phase:
                event('phase', phase=phase)
                last_phase = phase
            time.sleep(5)
        if sampler.returncode != 0:
            raise RuntimeError('resource sampler failed')
    finally:
        if sampler.poll() is None:
            sampler.terminate()
            sampler.wait(timeout=10)
    # Never write logs that might contain unexpected sensitive text to the report.
    logs = run(['docker', 'logs', '--since', state['State']['StartedAt'], kotlin_id], check=False)
    lines = (logs.stdout + logs.stderr).splitlines()
    known = ('service operation failed', 'check_failed', 'telegram polling failure',
             'telegram reply failure', 'telegram deletion failure',
             'telegram command synchronization failure')
    event('trial_completed', diagnostics={key: lines.count(key) for key in known},
          other_diagnostic_lines=sum(line not in known for line in lines))
except BaseException as error:
    event('trial_error', classification=type(error).__name__)
    raise
finally:
    # Stop the trial before restoring Go: never run both pollers with the same token.
    stopped = run(COMPOSE + ['stop', '--timeout', '90'], check=False)
    if stopped.returncode:
        event('rollback_error', action='stop_kotlin')
        if kotlin_id:
            run(['docker', 'stop', '--time', '90', kotlin_id])
    run(['docker', 'start', GO])
    event('go_restored')
    run(['systemctl', '--user', 'start', 'case-watch-update.timer'])
    event('updater_restored')
    # Remove temporary credential-bearing snapshots; retain only independent volumes.
    import shutil
    shutil.rmtree(PRIVATE)
