"""Read public power/thermal/CPU frequency status without changing any settings."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import json, subprocess, sys, time
label,serial=sys.argv[1:3]
assert label.replace('-','').isalnum()
adb=research_config.adb()
def read(*args):return subprocess.check_output([adb,'-P',research_config.adb_port(),'-s',serial,'shell']+list(args),timeout=20).decode('utf8',errors='replace')
power=read('dumpsys','power');thermal=read('dumpsys','thermalservice');cpu={}
for number in [0,4,6,7]:
    try:cpu[str(number)]=int(read('cat',f'/sys/devices/system/cpu/cpu{number}/cpufreq/scaling_cur_freq').strip())
    except (subprocess.SubprocessError,ValueError):cpu[str(number)]=None
report=dict(epoch=time.time(),power=[l.strip() for l in power.splitlines() if any(s in l for s in ['mWakefulness=','mIsPowered=','mLowPowerStandbyActive='])],thermal=[l.strip() for l in thermal.splitlines() if any(s in l for s in ['Thermal Status:','IsStatusOverride:','Cached temperatures:','Current temperatures from HAL:','Temperature{mValue='])],cpuCurrentKHz=cpu)
Path(__file__).with_name('out').joinpath(label+'-power.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf8')
print(json.dumps(dict(power=report['power'],cpuCurrentKHz=cpu,thermalStatus=[l for l in report['thermal'] if l.startswith('Thermal Status:')]),ensure_ascii=False))
