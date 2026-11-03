raise SystemExit("HISTORICAL SESSION SCRIPT: direct execution disabled; see research/perspective-camera/README.md")

from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import json,subprocess
adb=research_config.adb()
def call(*args):return subprocess.check_output([adb,'-P',research_config.adb_port()]+list(args),timeout=20).decode('utf8',errors='replace').strip()
devices=[('k80',research_config.serial()),('pad',research_config.serial())];records=[]
for line in call('forward','--list').splitlines():
    fields=line.split()
    if len(fields)==3 and fields[2].startswith('localabstract:rc25_perf_'):
        assert fields[0] in [s for _,s in devices];call('-s',fields[0],'forward','--remove',fields[1])
for label,serial in devices:
    processes=call('-s',serial,'shell','ps','-A','-o','USER,PID,ARGS')
    research=[line for line in processes.splitlines() if any(s in line for s in ['com.genymobile.scrcpy.PipelineProbe','com.genymobile.scrcpy.SceneProbe','com.genymobile.scrcpy.WindowTreeProbe','com.genymobile.scrcpy.CodecBenchmarkProbe','com.genymobile.scrcpy.SyntheticReadbackProbe','com.genymobile.scrcpy.DeviceCapabilitiesProbe'])]
    assert not research,research
    names=['rc25-perf-probes.zip','rc25-perf-base.jar'];call('-s',serial,'shell','rm','-f',*['/data/local/tmp/'+s for s in names]);remaining=call('-s',serial,'shell','ls','/data/local/tmp');assert not any(s in remaining.splitlines() for s in names)
    records.append(dict(device=label,researchProcesses=research,temporaryFilesRemoved=names,remainingNativeAlphaProcesses=[l for l in processes.splitlines() if 'com.genymobile.scrcpy.NativeAlphaProbe' in l]))
assert 'localabstract:rc25_perf_' not in call('forward','--list')
report=dict(devices=records,researchForwards=0)
Path(__file__).with_name('out').joinpath('cleanup.json').write_text(json.dumps(report,indent=2),encoding='utf8');print(json.dumps(report))
