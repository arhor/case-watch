#!/usr/bin/env python3
"""Isolated, synthetic ARM64 comparison. Never accesses the live bot or database."""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import platform
import signal
from email.parser import BytesParser
from email.policy import default as email_policy
from urllib.parse import parse_qs
import shutil
import statistics
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = Path(__file__).resolve().parents[3]
OUT = Path(__file__).resolve().parent
TOKEN = '123:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
COMMANDS = [
    {'command': name, 'description': description} for name, description in [
        ('start', 'Добавить дело для отслеживания'), ('status', 'Проверить статус дела'),
        ('remove', 'Удалить дело и связанные с ним данные'),
        ('cancel', 'Отменить текущее действие'), ('help', 'Показать эту справку')]]
FIXTURES = ROOT / 'internal/clients/testdata'
HZ = os.sysconf('SC_CLK_TCK')


def command(args):
    result = subprocess.run(args, cwd=ROOT, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(f'command failed: {args[:3]}: {result.stderr[:500]}')
    return result.stdout


def inspect(name):
    return json.loads(command(['docker', 'inspect', name]))[0]


def sample(pid):
    root = Path(f'/proc/{pid}')
    status = {}
    for line in (root / 'status').read_text().splitlines():
        key, _, value = line.partition(':')
        if key in {'VmRSS', 'VmHWM', 'VmSwap', 'Threads', 'RssAnon', 'RssFile'}:
            status[key] = int(value.strip().split()[0])
    fields = (root / 'stat').read_text().rsplit(')', 1)[1].split()
    return {'monotonic_s': time.monotonic(), 'cpu_time_s': (int(fields[11]) + int(fields[12])) / HZ, **status}


class Fake:
    def __init__(self, scenario):
        self.scenario = scenario
        self.ready = threading.Event()
        self.update = threading.Event()
        self.finished = threading.Event()
        self.closing = threading.Event()
        self.ready_at = None
        self.started_at = None
        self.finished_at = None
        self.dispatched = False
        self.rpc = 0
        self.requests = {}
        self.lock = threading.Lock()
        self.failures = []
        self.server = ThreadingHTTPServer(('127.0.0.1', 0), self.handler())
        self.server.daemon_threads = True
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.endpoint = f'http://127.0.0.1:{self.server.server_port}'

    def handler(self):
        fake = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = 'HTTP/1.1'

            def log_message(self, *_args):
                pass

            def respond(self, body, cookie=False):
                if not isinstance(body, bytes):
                    body = json.dumps(body, ensure_ascii=False).encode()
                self.send_response(200)
                self.send_header('Content-Length', str(len(body)))
                if cookie:
                    self.send_header('Set-Cookie', 'session=synthetic; Path=/')
                self.end_headers()
                try:
                    self.wfile.write(body)
                except (BrokenPipeError, ConnectionResetError):
                    pass

            def do_GET(self):
                if self.headers.get('Transfer-Encoding', '').lower() == 'chunked':
                    while True:
                        size = int(self.rfile.readline().strip().split(b';')[0], 16)
                        if not size:
                            self.rfile.readline()
                            break
                        self.rfile.read(size + 2)
                with fake.lock:
                    fake.requests['portal_get'] = fake.requests.get('portal_get', 0) + 1
                    fake.started_at = time.monotonic()
                self.respond((FIXTURES / 'bootstrap.html').read_bytes(), cookie=True)

            def do_POST(self):
                raw = self.rfile.read(int(self.headers.get('Content-Length', '0')))
                # Go deliberately uses non-replayable chunked portal bodies.
                if self.headers.get('Transfer-Encoding', '').lower() == 'chunked':
                    chunks = []
                    while True:
                        size = int(self.rfile.readline().strip().split(b';')[0], 16)
                        if not size:
                            self.rfile.readline()
                            break
                        chunks.append(self.rfile.read(size))
                        self.rfile.read(2)
                    raw = b''.join(chunks)
                content_type = self.headers.get('Content-Type', '')
                if raw and content_type.startswith('multipart/form-data'):
                    message = BytesParser(policy=email_policy).parsebytes(('Content-Type: ' + content_type + '\r\nMIME-Version: 1.0\r\n\r\n').encode() + raw)
                    body = {part.get_param('name', header='content-disposition'): part.get_payload(decode=True).decode('utf-8') for part in message.iter_parts()}
                elif raw and content_type.startswith('application/x-www-form-urlencoded'):
                    body = {key: values[0] for key, values in parse_qs(raw.decode()).items()}
                else:
                    body = json.loads(raw) if raw else {}
                method = self.path.rsplit('/', 1)[-1]
                if self.path.startswith('/?v-r=uidl'):
                    with fake.lock:
                        step = fake.rpc
                        fake.rpc += 1
                        fake.requests['portal_rpc'] = fake.rpc
                    if step >= 4:
                        fake.failures.append('extra_portal_rpc')
                        self.send_error(500)
                        return
                    self.respond((FIXTURES / f'rpc{step}.json').read_bytes())
                    return
                with fake.lock:
                    fake.requests[method] = fake.requests.get(method, 0) + 1
                result = True
                if method == 'getMe':
                    result = {'id': 123, 'is_bot': True, 'first_name': 'Synthetic'}
                elif method == 'getMyCommands':
                    result = COMMANDS
                elif method == 'getUpdates':
                    with fake.lock:
                        if fake.ready_at is None:
                            fake.ready_at = time.monotonic()
                            fake.ready_wall = time.time()
                            fake.ready.set()
                    deadline = time.monotonic() + 59
                    result = []
                    while not fake.closing.is_set() and time.monotonic() < deadline:
                        if fake.scenario == 'manual' and fake.update.is_set() and not fake.dispatched:
                            fake.dispatched = True
                            result = [{'update_id': 1, 'message': {'message_id': 1, 'from': {'id': 1, 'is_bot': False, 'first_name': 'Synthetic'}, 'chat': {'id': 1, 'type': 'private'}, 'text': '/status'}}]
                            break
                        time.sleep(.02)
                elif method in {'sendMessage', 'scheduled-done'}:
                    if method == 'sendMessage':
                        if 'Данные успешно обновлены.' not in body.get('text', ''):
                            fake.failures.append('unexpected_status_reply')
                        result = {'message_id': 2, 'date': int(time.time()), 'chat': {'id': 1, 'type': 'private'}, 'text': body.get('text', '')}
                    fake.finished_at = time.monotonic()
                    fake.finished.set()
                self.respond({'ok': True, 'result': result})

        return Handler

    def close(self):
        self.closing.set()
        self.server.shutdown()
        self.server.server_close()


def summarize(rows, start, end):
    before = [r for r in rows if r['monotonic_s'] <= start]
    boundary = before[-1]['monotonic_s'] if before else start
    selected = [r for r in rows if boundary <= r['monotonic_s'] <= end]
    if len(selected) < 2:
        raise RuntimeError('insufficient samples')
    seconds = selected[-1]['monotonic_s'] - selected[0]['monotonic_s']
    cpu = selected[-1]['cpu_time_s'] - selected[0]['cpu_time_s']
    return {'sample_duration_s': seconds, 'samples': len(selected), 'cpu_time_s': cpu,
            'cpu_percent_one_core': 100 * cpu / seconds,
            'rss_median_mib': statistics.median(r['VmRSS'] for r in selected) / 1024,
            'rss_min_mib': min(r['VmRSS'] for r in selected) / 1024,
            'rss_max_mib': max(r['VmRSS'] for r in selected) / 1024,
            'process_lifetime_peak_rss_mib': max(r['VmHWM'] for r in selected) / 1024,
            'threads_min': min(r['Threads'] for r in selected),
            'threads_max': max(r['Threads'] for r in selected),
            'swap_max_mib': max(r['VmSwap'] for r in selected) / 1024}


def trial(technology, image, scenario, repetition, args):
    fake = Fake(scenario)
    name = f'case-watch-synthetic-{technology}-{os.getpid()}'
    data = tempfile.mkdtemp(prefix='case-watch-synthetic-')
    rows = []
    load_start = os.getloadavg()
    created = False
    try:
        launch = ['docker', 'create', '--name', name, '--network', 'host', '--cpus', '0.25', '--memory', '128m', '--pids-limit', '64',
                  '-v', data + ':/data', '-e', 'BENCH_ENDPOINT=' + fake.endpoint, '-e', 'BENCH_SCENARIO=' + scenario]
        if technology == 'node':
            launch += ['-v', str(OUT / 'probe.js') + ':/app/measurements/probe.js:ro', '--entrypoint', 'node', image, '/app/measurements/probe.js']
        else:
            launch += ['-v', str(OUT / 'go-probe-bin') + ':/probe:ro', '--entrypoint', '/probe', image]
        command(launch)
        created = True
        command(['docker', 'start', name])
        info = inspect(name)
        pid = info['State']['Pid']
        startup_deadline = time.monotonic() + 20
        while not fake.ready.is_set():
            rows.append(sample(pid))
            if time.monotonic() > startup_deadline:
                raise RuntimeError('readiness timeout')
            time.sleep(.02)
        ready = fake.ready_at
        warm_end = ready + args.warmup
        deadline = warm_end + (args.idle if scenario == 'idle' else 20)
        triggered = False
        while True:
            row = sample(pid)
            rows.append(row)
            now = row['monotonic_s']
            if now >= warm_end and scenario == 'manual' and not triggered:
                fake.update.set()
                triggered = True
            if scenario == 'idle' and now >= deadline:
                window_start, window_end = warm_end, now
                break
            if scenario != 'idle' and fake.finished.is_set() and now >= fake.finished_at + 2:
                window_start, window_end = fake.started_at, now
                break
            if now > deadline:
                raise RuntimeError('activity timeout')
            time.sleep(args.interval)
        current = inspect(name)
        if not current['State']['Running'] or current['RestartCount']:
            raise RuntimeError('container stopped or restarted')
        # Inspect stderr separately; never persist unrestricted logs.
        captured = subprocess.run(['docker', 'logs', name], capture_output=True, text=True)
        diagnostic_lines = len((captured.stdout + captured.stderr).splitlines())
        if diagnostic_lines or fake.failures:
            raise RuntimeError('workload audit: ' + json.dumps({'counts':fake.requests, 'diagnostic_lines':diagnostic_lines, 'failures':fake.failures, 'known_diagnostics':[category for category in ['check_failed','telegram polling failure','telegram reply failure','probe_failed'] if category in captured.stdout + captured.stderr]}))
        if scenario != 'idle' and (fake.rpc != 4 or fake.requests.get('portal_get') != 1):
            raise RuntimeError('incorrect portal workload')
        cgroup = next(line.split(':', 2)[2] for line in Path(f'/proc/{pid}/cgroup').read_text().splitlines() if line.startswith('0:'))
        cgroot = Path('/sys/fs/cgroup') / cgroup.lstrip('/')
        effective = {key: (cgroot / key).read_text().strip() if (cgroot / key).exists() else None for key in ['cpu.max', 'pids.max', 'memory.max', 'memory.peak']}
        started = dt.datetime.fromisoformat(info['State']['StartedAt'].replace('Z', '+00:00')).timestamp()
        result = {'technology': technology, 'scenario': scenario, 'repetition': repetition, 'pid': pid,
                  'image': info['Image'], 'host_loadavg_start':load_start, 'host_loadavg_end':os.getloadavg(), 'child_pids':Path(f'/proc/{pid}/task/{pid}/children').read_text().split(), 'readiness_s': fake.ready_wall - started,
                  'window_start_monotonic_s': window_start, 'window_end_monotonic_s': window_end,
                  'warmup_s': args.warmup, 'request_counts': fake.requests, 'diagnostic_lines': diagnostic_lines,
                  'cgroup': effective, 'requested_memory_bytes': 128 * 1024 * 1024, 'docker_recorded_memory_bytes': info['HostConfig']['Memory'],
                  'metrics': summarize(rows, window_start, window_end)}
        stem = f'{technology}-{scenario}-{repetition}'
        (OUT / (stem + '.jsonl')).write_text(''.join(json.dumps(r) + '\n' for r in rows))
        print(json.dumps(result), flush=True)
        return result
    finally:
        fake.closing.set()
        if created:
            subprocess.run(['docker', 'stop', '--time', '15', name], capture_output=True)
            subprocess.run(['docker', 'rm', '-f', name], capture_output=True)
        fake.close()
        shutil.rmtree(data)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--repetitions', type=int, default=2)
    parser.add_argument('--warmup', type=float, default=10)
    parser.add_argument('--idle', type=float, default=60)
    parser.add_argument('--interval', type=float, default=.25)
    parser.add_argument('--scenarios', nargs='+', choices=['idle', 'manual', 'scheduled'], default=['idle', 'manual', 'scheduled'])
    parser.add_argument('--go-image', default='case-watch-case-watch:latest')
    parser.add_argument('--node-image', default='case-watch-node:24.22.3')
    args = parser.parse_args()
    if args.warmup != 10:
        raise RuntimeError('scheduled probes use a fixed ten-second trigger')
    images = {}
    for technology, image in [('go', args.go_image), ('node', args.node_image)]:
        data = json.loads(command(['docker', 'image', 'inspect', image]))[0]
        images[technology] = {key: data.get(key) for key in ['Id', 'RepoTags', 'RepoDigests', 'Size', 'Architecture', 'Os']}
    sources = sorted([* (ROOT / 'internal').rglob('*.go'), * (ROOT / 'experiments/nodejs/src').glob('*'), * OUT.glob('*.py'), * OUT.glob('*.js'), * (OUT / 'go-probe').glob('*.go')])
    metadata = {'utc': dt.datetime.now(dt.timezone.utc).isoformat(), 'revision': command(['git', 'rev-parse', 'HEAD']).strip(),
                'go_probe_binary_sha256':hashlib.sha256((OUT/'go-probe-bin').read_bytes()).hexdigest(),
                'git_status': command(['git', 'status', '--short']).splitlines(), 'host': platform.uname()._asdict(),
                'go_toolchain':command(['go','version']).strip(), 'node_runtime': json.loads(command(['docker','run','--rm','--network','none','--entrypoint','node',args.node_image,'-p','JSON.stringify(process.versions)'])),
                'cpu_count': os.cpu_count(), 'clock_ticks_per_second': HZ,
                'memory_total_kib': int(next(line.split()[1] for line in Path('/proc/meminfo').read_text().splitlines() if line.startswith('MemTotal:'))),
                'cgroup_controllers': Path('/sys/fs/cgroup/cgroup.controllers').read_text().strip(),
                'parameters': vars(args), 'images': images,
                'source_sha256': {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sources}}
    (OUT / 'host.json').write_text(json.dumps(metadata, indent=2) + '\n')
    results = []
    (OUT / 'results.json').write_text('[]\n')
    for repetition in range(1, args.repetitions + 1):
        # Reverse order to reduce systematic startup/temperature order bias.
        stacks = [('go', args.go_image), ('node', args.node_image)]
        if repetition % 2 == 0:
            stacks.reverse()
        for scenario in args.scenarios:
            for technology, image in stacks:
                result = trial(technology, image, scenario, repetition, args)
                results.append(result)
                (OUT / 'results.json').write_text(json.dumps(results, indent=2) + '\n')


if __name__ == '__main__':
    signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
    main()
