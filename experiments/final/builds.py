"""Ten paired clean/incremental release builds, offline inside compiler containers."""
import json, subprocess, time, pathlib, statistics, platform, os, datetime
ROOT=pathlib.Path(__file__).resolve().parents[2]
OUT=pathlib.Path(__file__).resolve().parent
specs={
 'go': {'image':'case-watch-bench-go-toolchain','dir':'/app','setup':'cp -a /bench-mod /tmp/mod','env':['GOMODCACHE=/tmp/mod','GOCACHE=/tmp/build-cache','CGO_ENABLED=0'],'clean':'rm -rf /tmp/build-cache /tmp/app','build':"go build -trimpath -ldflags='-s -w' -o /tmp/app .",'source':'main.go'},
 'rust': {'image':'case-watch-bench-rust-toolchain','dir':'/source','setup':'mkdir -p /usr/local/cargo/registry; cp -a /bench-registry/. /usr/local/cargo/registry/','env':[],'clean':'rm -rf target','build':'cargo build --offline --locked --release --target aarch64-unknown-linux-musl','source':'src/main.rs'},
 'kotlin': {'image':'case-watch-bench-kotlin-toolchain','dir':'/workspace/experiments/kotlin','setup':'mkdir -p /root/.gradle; cp -a /bench-gradle/. /root/.gradle/','env':[],'clean':'rm -rf build .gradle .kotlin','build':'./gradlew --offline --no-daemon -Pkotlin.compiler.execution.strategy=in-process installDist distTar','source':'src/main/kotlin/casewatch/Main.kt'},
 'node': {'image':'case-watch-bench-node-builder','dir':'/workspace/experiments/nodejs','setup':'true','env':[],'clean':'rm -f /tmp/case-watch-nodejs-*.tgz','build':'npm run build','source':'src/main.js'},
}
def run(args, input_text=None):
 r=subprocess.run(args,input=input_text,capture_output=True,text=True)
 if r.returncode: raise RuntimeError(str(args)+'\n'+r.stderr[-3000:]+r.stdout[-3000:])
 return r.stdout

def execute(name,s,cmd):
 return run(['docker','exec','-w',s['dir'],name,'sh','-ec',cmd])

def main():
 results=json.loads((OUT/'build-results.json').read_text()) if (OUT/'build-results.json').exists() else []
 metadata={'host':platform.uname()._asdict(),'revision':run(['git','rev-parse','HEAD']).strip(),'toolchains':{},'method':{'samples_per_kind':10,'incremental_edit':'change an entrypoint error diagnostic literal; no dependency/API changes','fresh':'empty compiler outputs/caches; downloaded dependencies retained','timing':'Docker exec wall time, excludes explicit cache/output removal'}}
 if (OUT/'build-host.json').exists(): metadata=json.loads((OUT/'build-host.json').read_text())
 for tech,s in specs.items():
  if len([r for r in results if r['technology']==tech])==20: continue
  results=[r for r in results if r['technology']!=tech]
  name='case-watch-final-build-'+tech
  launch=['docker','run','-d','--name',name,'--network','none']
  for env in s['env']: launch+=['-e',env]
  run(launch+['--entrypoint','sleep',s['image'],'infinity'])
  try:
   execute(name,s,s['setup'])
   versions={'go':'go version','rust':'rustc --version; cargo --version','kotlin':'java -version 2>&1; ./gradlew --offline --version','node':'node --version; npm --version'}
   metadata['toolchains'][tech]={'versions':execute(name,s,versions[tech]),'image':json.loads(run(['docker','image','inspect',s['image']]))[0]['Id']}
   (OUT/'build-host.json').write_text(json.dumps(metadata,indent=2)+'\n')
   original=execute(name,s,'cat '+s['source'])
   edits={'go':('log.Fatal(err)', 'log.Fatal("benchmark revision {rep}: ", err)'), 'rust':('eprintln!("{e}");','eprintln!("benchmark revision {rep}: {{e}}");'), 'kotlin':('service operation failed','service operation failed revision {rep}'), 'node':('console.error(error.message);','console.error("benchmark revision {rep}:", error.message);')}
   for rep in range(1,11):
    run(['docker','exec','-i',name,'tee',s['dir']+'/'+s['source']],original)
    execute(name,s,s['clean'])
    for kind in ['fresh','incremental']:
     if kind=='incremental':
      old,new=edits[tech]
      assert old in original
      run(['docker','exec','-i',name,'tee',s['dir']+'/'+s['source']],original.replace(old,new.format(rep=rep)))
     load_start=os.getloadavg()
     timestamp=datetime.datetime.now(datetime.timezone.utc).isoformat()
     start=time.monotonic()
     log=execute(name,s,s['build'])
     duration=time.monotonic()-start
     (OUT/f'build-{tech}-{kind}-{rep}.log').write_text(log)
     row={'technology':tech,'kind':kind,'repetition':rep,'wall_s':duration,'command':s['build'],'utc':timestamp,'host_loadavg_start':load_start,'host_loadavg_end':os.getloadavg()}
     results.append(row)
     (OUT/'build-results.json').write_text(json.dumps(results,indent=2)+'\n')
     print(json.dumps(row),flush=True)
  finally: run(['docker','rm','-f',name])
if __name__=='__main__': main()
