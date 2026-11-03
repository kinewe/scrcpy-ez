raise SystemExit("HISTORICAL SESSION SCRIPT: direct execution disabled; see research/perspective-camera/README.md")

from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import json,subprocess
adb=research_config.adb()
def call(serial,*args):return subprocess.check_output([adb,'-P',research_config.adb_port(),'-s',serial,*args],timeout=20).decode('utf8',errors='replace')
records=[]
for serial,old_client in [(research_config.serial(),None),(research_config.serial(),'37360')]:
    ps=call(serial,'shell','ps','-A','-o','PID,ARGS')
    assert not any(c in ps for c in ['com.genymobile.scrcpy.WindowTreeProbe','com.genymobile.scrcpy.NativeAlphaProbe']),ps
    names=['rc25-perf-probes.zip','rc25-perf-base.jar']
    if old_client:names.append('ez-native-alpha-'+old_client+'.jar')
    listing=call(serial,'shell','ls','/data/local/tmp').splitlines();existed=[n for n in names if n in listing]
    call(serial,'shell','rm','-f',*['/data/local/tmp/'+n for n in names])
    remaining=call(serial,'shell','ls','/data/local/tmp').splitlines();assert not any(n in remaining for n in names)
    records.append(dict(serial=serial,ownedTemporaryFilesExisted=existed,ownedTemporaryFilesRemoved=names))
result=dict(devices=records,previousOwnStaleForwardRemoved=dict(serial=research_config.serial(),local='tcp:3948',remote='localabstract:ez_native_alpha_37360'))
Path(__file__).with_name('out').joinpath('cleanup.json').write_text(json.dumps(result,indent=2),encoding='utf8');print(json.dumps(result))
