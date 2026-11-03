raise SystemExit("HISTORICAL SESSION SCRIPT: direct execution disabled; see research/perspective-camera/README.md")

"""Remove only fixed research files and this experiment's residual forwards."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import json, subprocess
adb=research_config.adb()
def call(*args):return subprocess.check_output([adb,'-P',research_config.adb_port()]+list(args),timeout=20).decode('utf8',errors='replace').strip()
devices=[('k80',research_config.serial()),('tablet',research_config.serial()),('huawei',research_config.serial())]
before=call('forward','--list');removed=[]
for line in before.splitlines():
    parts=line.split()
    if len(parts)==3 and parts[2].startswith('localabstract:rc25_scope_'):
        assert parts[0] in [s for _,s in devices] or parts[0]==research_config.serial()
        call('-s',parts[0],'forward','--remove',parts[1]);removed.append(parts[1])
records=[]
for label,serial in devices:
    processes=call('-s',serial,'shell','ps','-A','-o','USER,PID,ARGS')
    live=[l for l in processes.splitlines() if any(n in l for n in ['com.genymobile.scrcpy.PipelineProbe','com.genymobile.scrcpy.SceneProbe','com.genymobile.scrcpy.DeviceCapabilitiesProbe','com.genymobile.scrcpy.NameFilterLab','com.genymobile.scrcpy.AppNameFilterProbe'])]
    assert not live,(label,live)
    names=['rc25-scope-probes.zip','rc25-scope-base.jar','rc25-scope-baseline-probes.zip','rc25-scope-baseline-base.jar','rc25-general-probes.zip','rc25-general-base.jar']
    paths=['/data/local/tmp/'+n for n in names]
    call('-s',serial,'shell','rm','-f',*paths)
    check=call('-s',serial,'shell','ls','/data/local/tmp')
    assert not any(n in check.splitlines() for n in names)
    records.append(dict(device=label,liveResearchProcesses=live,fixedTemporaryFilesRemoved=names))
after=call('forward','--list');assert 'localabstract:rc25_scope_' not in after
report=dict(devices=records,removedResidualForwards=removed,remainingResearchForwards=0,adbServerPort=5038)
Path(__file__).with_name('out').joinpath('cleanup.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf8')
print(json.dumps(report))
