from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import json,re,subprocess
adb=research_config.adb();serial=research_config.serial()
def read(*args):return subprocess.check_output([adb,'-P',research_config.adb_port(),'-s',serial,'shell']+list(args),timeout=20).decode('utf8',errors='replace')
processes=read('ps','-A','-o','USER,PID,ARGS');display=read('dumpsys','display');whitelist=read('cmd','deviceidle','whitelist')
server=any('scid=475cd5df' in line for line in processes.splitlines());remaining_display=bool(re.search(r'\bmDisplayId=267\b|\bdisplayId=267\b',display));exempt=any('com.xingin.xhs' in line for line in whitelist.splitlines())
assert not server and not remaining_display,(server,remaining_display)
report=dict(observedClientPID=37360,clientTitle='RC25 Perf - Pad-XHS-Product',verifiedClientResponding=False,originalRuntimeServerSHA256='FC18BEEA9926B0F25E83041CE398B000DA92BAF539A76E902EDAD157D869781A',alphaStopLogReportedRestored=True,serverPIDBefore=9106,serverSessionId='475cd5df',displayBefore=267,action='Normal close hung; verified exact own client command line and terminated only PID 37360',serverRemaining=server,display267Remaining=remaining_display,xhsIdleWhitelistAfter=exempt,rootCauseConfirmed=False,regressionCausedByCodecOptimization=False,regressionAttributionReason='The observed GUI used the unchanged original runtime, not the optimized research server')
Path(__file__).with_name('out').joinpath('exit-hang.json').write_text(json.dumps(report,indent=2),encoding='utf8');print(json.dumps(report))
