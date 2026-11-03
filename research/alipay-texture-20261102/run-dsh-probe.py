from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import subprocess,sys,json,time,re
folder=Path(__file__).resolve().parent;label,klass,*params=sys.argv[1:]
port=research_config.adb_port();serial=research_config.serial()
if '--adb-port' in params:
    at=params.index('--adb-port');port=params[at+1];del params[at:at+2];assert port.isdigit()
if '--serial' in params:
    at=params.index('--serial');serial=params[at+1];del params[at:at+2]
live_log=None
if '--live-log' in params:
    at=params.index('--live-log');live_log=Path(params[at+1]).resolve();del params[at:at+2]
assert label.replace('-','').isalnum() and klass in ['WindowAdaptationProbe','FreeformProbe','SceneCleanupProbe','GenericUiProbe','WindowTreeProbe','DiagnosticPageProbe','VirtualCameraControlProbe','OpaqueApiInventory','CompositeResponseProbe']
adb=research_config.adb()
path=folder/'out'/('dsh-'+label+'.log');assert not path.exists()
if klass in ['WindowAdaptationProbe','FreeformProbe']:
    if live_log is None or live_log.parent!=(folder/'out').resolve():
        raise SystemExit('LIVE_CAST_LOG_REQUIRED: pass --live-log pointing to this experiment out directory')
    offset=live_log.stat().st_size;deadline=time.monotonic()+8;ready=False
    while time.monotonic()<deadline:
        text=live_log.read_bytes()[offset:].decode('utf8',errors='replace')
        if re.search(r'Perspective requests:.*rate=[1-9][0-9]*fps',text):ready=True;break
        time.sleep(.2)
    if not ready:raise SystemExit('LIVE_CAST_NOT_STREAMING: no fresh positive request rate; target not mutated')
command=[adb,'-P',port,'-s',serial,'shell','CLASSPATH=/data/local/tmp/rc25-temporal-20261102-probes.zip:/data/local/tmp/rc25-temporal-20261102-base.jar','app_process','/','com.genymobile.scrcpy.'+klass,'com.eg.android.AlipayGphone',*params]
start=time.time()
with path.open('wb') as log:p=subprocess.run(command,stdout=log,stderr=subprocess.STDOUT,timeout=90 if params and params[-1]=='primitive-refresh' else 60)
print(json.dumps(dict(label=label,exit=p.returncode,seconds=time.time()-start,path=str(path))))
text=path.read_text(encoding='utf8',errors='replace');print('\n'.join(s[:500] for s in text.splitlines() if any(k in s for k in ['RESTORED','RESOURCES_RELEASED','Exception','SCOPED_CHILD','FREEFORM_TRIAL','WINDOW_SOURCE'])))
raise SystemExit(p.returncode)
