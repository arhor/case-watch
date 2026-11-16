"""Generate the root report only from complete ten-sample datasets."""
import json, statistics, pathlib, collections
ROOT=pathlib.Path(__file__).resolve().parents[2]; OUT=pathlib.Path(__file__).resolve().parent

def fmt(xs,digits=2):
 return f'{statistics.median(xs):.{digits}f} ({min(xs):.{digits}f}–{max(xs):.{digits}f})'
def main():
 builds=json.loads((OUT/'build-results.json').read_text()); trials=json.loads((OUT/'runtime-results.json').read_text()); host=json.loads((OUT/'host.json').read_text())
 assert len(builds)==80 and len(trials)==120, 'incomplete dataset'
 assert json.loads((OUT/'build-host.json').read_text())['revision']==host['revision'], 'source revision mismatch'
 for row in trials:
  assert not row['children'], 'unmeasured child process'
  counts=row['request_counts']
  if row['scenario']=='idle': assert not counts.get('portal_get') and not counts.get('portal_rpc') and counts.get('getUpdates',0)>=2
  else: assert counts.get('portal_get')==1 and counts.get('portal_rpc')==4
 for tech in ['go','rust','kotlin','node']:
  for kind in ['fresh','incremental']:
   rows=[x for x in builds if x['technology']==tech and x['kind']==kind]
   assert sorted(x['repetition'] for x in rows)==list(range(1,11)),(tech,kind)
  for scenario in ['idle','manual','scheduled']:
   rows=[x for x in trials if x['technology']==tech and x['scenario']==scenario]
   assert sorted(x['repetition'] for x in rows)==list(range(1,11)),(tech,scenario)
 labels={'go':'Go','rust':'Rust','kotlin':'Kotlin/JVM','node':'Node.js'}
 idle_rss={t:statistics.median(x['metrics']['rss_median_mib'] for x in trials if x['technology']==t and x['scenario']=='idle') for t in labels}
 incremental_time={t:statistics.median(x['wall_s'] for x in builds if x['technology']==t and x['kind']=='incremental') for t in labels}
 fresh_time={t:statistics.median(x['wall_s'] for x in builds if x['technology']==t and x['kind']=='fresh') for t in labels}
 go_fresh=[x['wall_s'] for x in builds if x['technology']=='go' and x['kind']=='fresh']
 kotlin_fresh=[x['wall_s'] for x in builds if x['technology']=='kotlin' and x['kind']=='fresh']
 fresh_overlap='Go and Kotlin/JVM fresh-build ranges overlap. ' if max(min(go_fresh),min(kotlin_fresh))<=min(max(go_fresh),max(kotlin_fresh)) else ''
 smallest=min(idle_rss,key=idle_rss.get)
 fastest_incremental=min(['go','rust','kotlin'],key=incremental_time.get)
 fastest_compiled=min(['go','rust','kotlin'],key=fresh_time.get)
 lines=['# Final technology comparison','',f"Source revision: `{host['revision']}`. Each sampled metric reports the median and observed minimum–maximum across **10 repeated trials**. Image size is fixed production-image metadata, not a repeated timing sample. All four implementations ran sequentially on the same host; no historical measurements are mixed into these results.",'',f"{labels[smallest]} had the lowest median idle RSS at {idle_rss[smallest]:.2f} MiB. Among the compiled versions, {labels[fastest_compiled]} had the shortest median fresh build at {fresh_time[fastest_compiled]:.2f} seconds. {labels[fastest_incremental]} had the shortest median incremental compiled build at {incremental_time[fastest_incremental]:.2f} seconds. {fresh_overlap}Node.js build timings cover validation and packaging. The tables below show the tradeoffs and observed variation; these results describe one synthetic case on this host.",'','## Release build and startup comparison','','| Version | Fresh build, s | Incremental build, s | Startup readiness, s | Runtime image, MiB |','| --- | ---: | ---: | ---: | ---: |']
 for tech,label in [('go','Go'),('rust','Rust'),('kotlin','Kotlin/JVM'),('node','Node.js')]:
  fresh=[x['wall_s'] for x in builds if x['technology']==tech and x['kind']=='fresh']; inc=[x['wall_s'] for x in builds if x['technology']==tech and x['kind']=='incremental']; startup=[x['readiness_s'] for x in trials if x['technology']==tech and x['scenario']=='idle']
  lines.append(f"| {label} | {fmt(fresh)} | {fmt(inc)} | {fmt(startup,3)} | {host['images'][tech]['Size']/1024**2:.2f} |")
 for scenario,title in [('idle','Idle long polling'),('manual','Manual status refresh'),('scheduled','Scheduled case refresh')]:
  lines+=['',f'## {title}','','| Version | Median RSS, MiB | Lifetime peak RSS, MiB | CPU, % of one core | CPU time, s | Threads, maximum | Window, s |','| --- | ---: | ---: | ---: | ---: | ---: | ---: |']
  for tech in ['go','rust','kotlin','node']:
   rows=[x['metrics'] for x in trials if x['technology']==tech and x['scenario']==scenario]
   values=[fmt([x[key] for x in rows],digits) for key,digits in [('rss_median_mib',2),('process_lifetime_peak_rss_mib',2),('cpu_percent_one_core',5 if scenario=='idle' else 3),('cpu_time_s',6 if scenario=='idle' else 3),('threads_max',1),('sample_duration_s',2)]]
   lines.append('| '+labels[tech]+' | '+' | '.join(values)+' |')
 lines+=['','## Method and interpretation','',
 'Fresh builds remove compiler outputs and compiler caches while retaining downloaded dependencies and toolchains. They exclude network installation, tests, container construction and OS page-cache eviction. Incremental builds follow each fresh build after changing an error diagnostic literal in the entrypoint source; dependency artifacts remain available. Kotlin uses in-process compilation within a single-use Gradle daemon, retaining dependency/transform caches while clearing project outputs and state for fresh builds. Wall time includes the Docker exec launch. Node.js is interpreted: its build validates JavaScript and creates an npm package, so its build cost is packaging rather than compilation.',
 '', 'Runtime trials use production adapters with measurement-only launch wiring, a fresh SQLite database and one synthetic case whose last observation is two minutes old. The local Telegram/portal server serves the same sanitized fixtures to all versions. Each successful active trial audits exactly one bootstrap GET, four portal RPC calls and a completion reply/marker. Real two-second portal request spacing remains enabled. Scheduled results include the measurement-only completion HTTP request and its client initialization/allocation costs; manual results include the normal Telegram reply. Small CPU differences therefore also contain probe overhead. Traffic uses loopback HTTP; TLS, real network latency, enrollment conversations and notifications are outside this workload. No live credentials, databases or Telegram/portal traffic are used.',
 '', 'Readiness is the first Telegram getUpdates request relative to the container process start. Startup table uses the ten idle trials, rather than pooling 30 startups. Each trial warms up for ten seconds; idle sampling then runs for 60 seconds. Active windows begin at the sample before the first portal GET and end two seconds after the successful reply/marker. RSS is sampled every 250 ms; lifetime peak RSS includes startup. The scheduler probe triggers the normal sequential service workflow after ten seconds rather than waiting for a real Warsaw schedule slot. Before launch, the controller waits outside imminent real schedule slots to prevent unrelated automatic work inside idle/manual trials. These short JVM trials include early-runtime/JIT behavior and do not establish fully warmed sustained throughput.',
 '', 'CPU uses Linux cgroup v2 `cpu.stat` usage counters with microsecond units and is expressed as a percentage of one core. Raw samples also retain process user+system ticks at the host clock resolution. Rounded zero values do not prove zero CPU work. RSS counts resident shared mappings as well as private memory; PSS and complete container memory are not measured. Child-process lists are retained in raw results. Runtime containers request 0.25 CPU, 128 MiB and 64 tasks. Effective controls are retained per trial; this host lacks a memory cgroup controller, so the requested memory cap is not enforced.',
 '', 'Runtime image sizes are Docker logical uncompressed image sizes, not registry transfer sizes or unique disk usage. The Go/Rust launchers and Kotlin application jar are replaced with measurement-only wiring while runtime base images remain unchanged; listed sizes refer to production runtime images. Build containers use available host CPUs without the runtime CPU quota. Builds are grouped by version in Go, Rust, Kotlin, Node order. Builds and runtime collection run in separate phases; host load and filesystem caches are not controlled. The experiment measures one case and cannot establish capacity or performance under sustained multi-user traffic.',
 '', '## Verification','', 'The Go normal test suite and `go vet ./...` passed. Its race suite could not execute on this host: ThreadSanitizer reported an unsupported 47-bit VMA range (requires 48 bits); this is not a passing race check. Rust format/clippy/tests, Kotlin format/tests/packaging/runtime probes and Node syntax/format/tests passed while preparing compiler images. All four stacks passed synthetic manual and scheduled smoke checks before collection. The report generator verifies exactly ten distinct repetitions per version and measurement type.', '', '## Environment and reproducibility','',f"- Host: {host.get('cpu_hardware',{}).get('Model',['unknown model'])[0]}, `{host['host']['machine']}`, `{host['host']['release']}`; {host['cpu_count']} logical CPUs; {host['memory']}.",
 '- Raw CPU/RSS recomputation and source/probe identity checks: [data audit](experiments/final/data-audit.json).',
 '- Baseline checks and smoke audits: [verification](experiments/final/verification.json).',
 '- Exact toolchain versions and compiler image IDs: [build environment](experiments/final/build-host.json).',
 '- Runtime image IDs, source hashes and parameters: [runtime environment](experiments/final/host.json).',
 '- All 80 build observations: [build results](experiments/final/build-results.json).',
 '- All 120 runtime observations and workload audits: [runtime results](experiments/final/runtime-results.json).',
 '- Raw process samples: `experiments/final/{go,rust,kotlin,node}-{idle,manual,scheduled}-{1..10}.jsonl`.',
 '- Reproduction steps and qualifications: [measurement README](experiments/final/README.md).', '']
 (ROOT/'EXPERIMENT-REPORT.md').write_text('\n'.join(lines))
if __name__=='__main__': main()
