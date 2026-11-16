"""Four-stack controlled runtime trials; reuse audited synthetic protocol server."""
import importlib.util, pathlib, json, time, os, subprocess, tempfile, shutil, statistics, platform, hashlib, signal
from zoneinfo import ZoneInfo
ROOT=pathlib.Path(__file__).resolve().parents[2]
OUT=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('baseline',ROOT/'experiments/nodejs/measurements/measure.py')
b=importlib.util.module_from_spec(spec); spec.loader.exec_module(b)
IMAGES={'go':'case-watch-case-watch:latest','rust':'case-watch-rust:0.1.0-alpine','kotlin':'case-watch-kotlin:0.1.0-alpine','node':'case-watch-node:24.22.3-alpine'}
def sample(pid,cgroot):
 row=b.sample(pid)
 counters={key:int(value) for key,value in (line.split() for line in (cgroot/'cpu.stat').read_text().splitlines())}
 for key in ['usage_usec','user_usec','system_usec','nr_throttled','throttled_usec']:
  row['cgroup_'+key]=counters[key]
 row['monotonic_s']=time.monotonic()
 return row

def summarize(rows,start,end):
 tick=b.summarize(rows,start,end)
 highres=b.summarize([{**row,'cpu_time_s':row['cgroup_usage_usec']/1e6} for row in rows],start,end)
 highres['process_tick_cpu_time_s']=tick['cpu_time_s']
 highres['process_tick_cpu_percent_one_core']=tick['cpu_percent_one_core']
 assert highres['cpu_time_s']>=0
 return highres

def avoid_wall_clock_slot():
 # Leave enough room for readiness timeout, warmup and idle measurement.
 while True:
  now=b.dt.datetime.now(ZoneInfo('Europe/Warsaw'))
  pending=[(now.replace(hour=hour,minute=0,second=0,microsecond=0)-now).total_seconds() for hour in [8,10,12,14,16,18]] if now.weekday()<5 else []
  imminent=[delta for delta in pending if 0<=delta<=150]
  if not imminent: return
  pause=min(min(imminent)+2,45)
  print(f'Waiting {pause:.1f}s outside real Warsaw schedule slot; no trial running',flush=True)
  time.sleep(pause)

def trial(tech,scenario,rep,idle=60):
 avoid_wall_clock_slot()
 fake=b.Fake(scenario); rows=[]; data=tempfile.mkdtemp(prefix='case-watch-final-',dir=OUT/'.work'); name=f'case-watch-final-{tech}'
 created=False
 try:
  cmd=['docker','create','--name',name,'--network','host','--cpus','.25','--memory','128m','--pids-limit','64','-v',data+':/data','-e','BENCH_ENDPOINT='+fake.endpoint,'-e','BENCH_SCENARIO='+scenario,'-e','CASE_WATCH_TELEGRAM_BOT_TOKEN='+b.TOKEN,'-e','CASE_WATCH_DB_PATH=/data/bench.db']
  if tech in ['go','rust']:
   cmd+=['-v',str(OUT/f'{tech}-probe-bin')+':/probe:ro','--entrypoint','/probe',IMAGES[tech]]
  elif tech=='node':
   cmd+=['-v',str(ROOT/'experiments/nodejs/measurements/probe.js')+':/app/measurements/probe.js:ro','--entrypoint','node',IMAGES[tech],'/app/measurements/probe.js']
  else:
   cmd+=['-v',str(OUT/'kotlin-probe.jar')+':/opt/case-watch/lib/case-watch-kotlin.jar:ro',IMAGES[tech]]
  b.command(cmd); created=True
  b.command(['docker','start',name]); info=b.inspect(name); pid=info['State']['Pid']
  cg=next(x.split(':',2)[2] for x in pathlib.Path(f'/proc/{pid}/cgroup').read_text().splitlines() if x.startswith('0:'))
  cgroot=pathlib.Path('/sys/fs/cgroup')/cg.lstrip('/')
  deadline=time.monotonic()+60
  while not fake.ready.is_set():
   rows.append(sample(pid,cgroot))
   if time.monotonic()>deadline: raise RuntimeError('readiness timeout')
   time.sleep(.02)
  warm=fake.ready_at+10; deadline=warm+(idle if scenario=='idle' else 40); triggered=False
  while True:
   row=sample(pid,cgroot); rows.append(row); now=row['monotonic_s']
   if now>=warm and scenario=='manual' and not triggered: fake.update.set(); triggered=True
   if scenario=='idle' and now>=deadline: start,end=warm,now; break
   if scenario!='idle' and fake.finished.is_set() and now>=fake.finished_at+2: start,end=fake.started_at,now; break
   if now>deadline: raise RuntimeError('activity timeout '+str(fake.requests))
   time.sleep(.25)
  if scenario=='idle' and fake.requests.get('getUpdates',0)<2: raise RuntimeError('idle trial did not renew long polling')
  if scenario=='idle' and (fake.rpc or fake.requests.get('portal_get')): raise RuntimeError('idle trial included portal work')
  if fake.failures or (scenario!='idle' and (fake.rpc!=4 or fake.requests.get('portal_get')!=1)): raise RuntimeError('workload audit '+str(fake.requests)+str(fake.failures))
  logs=subprocess.run(['docker','logs',name],capture_output=True,text=True)
  # Successful smoke trials produced no logs; retain and reject diagnostics.
  (OUT/f'{tech}-{scenario}-{rep}.log').write_text(logs.stdout+logs.stderr)
  if logs.stdout.strip() or logs.stderr.strip(): raise RuntimeError('unexpected application diagnostic; inspect trial log')
  current=b.inspect(name)
  if not current['State']['Running'] or current['RestartCount']: raise RuntimeError('restart/stopped')
  cg=next(x.split(':',2)[2] for x in pathlib.Path(f'/proc/{pid}/cgroup').read_text().splitlines() if x.startswith('0:'))
  cgroot=pathlib.Path('/sys/fs/cgroup')/cg.lstrip('/')
  effective={k:(cgroot/k).read_text().strip() if (cgroot/k).exists() else None for k in ['cpu.max','memory.max','pids.max']}
  import datetime
  started=datetime.datetime.fromisoformat(info['State']['StartedAt'].replace('Z','+00:00')).timestamp()
  result={'technology':tech,'scenario':scenario,'repetition':rep,'started_utc':datetime.datetime.fromtimestamp(started,datetime.timezone.utc).isoformat(),'readiness_s':fake.ready_wall-started,'ready_monotonic_s':fake.ready_at,'window_start_monotonic_s':start,'window_end_monotonic_s':end,'warmup_s':10,'request_counts':fake.requests.copy(),'metrics':summarize(rows,start,end),'cgroup':effective,'host_loadavg':os.getloadavg(),'children':pathlib.Path(f'/proc/{pid}/task/{pid}/children').read_text().split()}
  (OUT/f'{tech}-{scenario}-{rep}.jsonl').write_text(''.join(json.dumps(x)+'\n' for x in rows))
  print(json.dumps(result),flush=True); return result
 finally:
  fake.closing.set()
  if created:
   subprocess.run(['docker','stop','--time','15',name],capture_output=True)
   subprocess.run(['docker','rm','-f',name],capture_output=True)
  fake.close(); shutil.rmtree(data)
def main():
 import sys
 (OUT/'.work').mkdir(exist_ok=True)
 for tech in IMAGES:
  IMAGES[tech]=json.loads(b.command(['docker','image','inspect',IMAGES[tech]]))[0]['Id']
 local={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in (ROOT/'experiments/nodejs/src').glob('*') if p.is_file()}
 remote=json.loads(b.command(['docker','run','--rm','--network','none','--entrypoint','node',IMAGES['node'],'-e',"const fs=require('fs'),c=require('crypto'); console.log(JSON.stringify(Object.fromEntries(fs.readdirSync('/app/src').map(n=>[n,c.createHash('sha256').update(fs.readFileSync('/app/src/'+n)).digest('hex')]))))"]))
 if local != remote: raise RuntimeError('Node runtime image differs from repository source')
 if '--smoke' in sys.argv:
  smoke=[]
  for tech in IMAGES:
   for scenario in ['manual','scheduled']: smoke.append(trial(tech,scenario,0))
  (OUT/'smoke-results.json').write_text(json.dumps(smoke,indent=2)+'\n')
  return
 metadata={'utc':b.dt.datetime.now(b.dt.timezone.utc).isoformat(),'revision':b.command(['git','rev-parse','HEAD']).strip(),'host':platform.uname()._asdict(),'cpu_count':os.cpu_count(),'clock_ticks_per_second':b.HZ,'cpu_measurement':'cgroup v2 cpu.stat usage_usec; process ticks retained separately','memory':pathlib.Path('/proc/meminfo').read_text().splitlines()[0],'cgroup_controllers':pathlib.Path('/sys/fs/cgroup/cgroup.controllers').read_text(),'images':{tech:{k:v for k,v in json.loads(b.command(['docker','image','inspect',image]))[0].items() if k in ['Id','Size','Architecture','Os','RepoTags','RepoDigests']} for tech,image in IMAGES.items()},'source_sha256':{str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(set(OUT.glob('*.py')) | set(OUT.glob('*.rs')) | set(OUT.glob('*.kt')) | set((ROOT/'internal').rglob('*.go')) | set((ROOT/'internal/clients/testdata').glob('*')) | {ROOT/'main.go',ROOT/'go.mod',ROOT/'go.sum',ROOT/'experiments/nodejs/measurements/go-probe/main.go',ROOT/'experiments/nodejs/measurements/probe.js',ROOT/'experiments/nodejs/measurements/measure.py'} | set((ROOT/'experiments/rust/src').rglob('*.*')) | set((ROOT/'experiments/kotlin/src/main').rglob('*.*')) | set((ROOT/'experiments/nodejs/src').glob('*.*'))) if p.is_file()},'probe_sha256':{name:hashlib.sha256((OUT/name).read_bytes()).hexdigest() for name in ['go-probe-bin','rust-probe-bin','kotlin-probe.jar']},'parameters':{'repetitions':10,'warmup_s':10,'idle_s':60,'sample_interval_s':.25}}
 if (OUT/'runtime-results.json').exists():
  previous=json.loads((OUT/'host.json').read_text())
  for key in ['revision','source_sha256','probe_sha256','parameters']:
   if previous[key]!=metadata[key]: raise RuntimeError('resume environment changed: '+key)
  if {t:x['Id'] for t,x in previous['images'].items()}!={t:x['Id'] for t,x in metadata['images'].items()}: raise RuntimeError('resume images changed')
 else:
  (OUT/'host.json').write_text(json.dumps(metadata,indent=2)+'\n')
 results=json.loads((OUT/'runtime-results.json').read_text()) if (OUT/'runtime-results.json').exists() else []
 completed={(x['technology'],x['scenario'],x['repetition']) for x in results}
 for rep in range(1,11):
  order=list(IMAGES); order=order[(rep-1)%4:]+order[:(rep-1)%4]
  for scenario in ['idle','manual','scheduled']:
   for tech in order:
    if (tech,scenario,rep) in completed: continue
    results.append(trial(tech,scenario,rep))
    (OUT/'runtime-results.json').write_text(json.dumps(results,indent=2)+'\n')
if __name__=='__main__':
 signal.signal(signal.SIGTERM,lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
 main()
