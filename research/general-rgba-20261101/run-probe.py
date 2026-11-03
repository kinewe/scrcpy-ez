from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import subprocess, sys
from device_tools import ADB, select_serial

folder = Path(__file__).resolve().parent
root = folder.parents[1]
adb = ADB
serial = select_serial()
allowed = {'GraphicsCapabilitiesProbe','NameFilterLab','AppNameFilterProbe'}
name = sys.argv[1]
assert name in allowed
def run(args, timeout=30):
    p = subprocess.run([adb,'-P',research_config.adb_port()]+args, capture_output=True, timeout=timeout)
    if p.returncode: raise RuntimeError(p.stdout.decode(errors='replace')+p.stderr.decode(errors='replace'))
    return p.stdout.decode('utf8',errors='replace')
print('Using online research device',flush=True)
print(run(['-s',serial,'push',str(folder/'out/probes.zip'),'/data/local/tmp/rc25-general-probes.zip']).strip(),flush=True)
print(run(['-s',serial,'push',str(research_config.build_dir() / 'scrcpy-server'),'/data/local/tmp/rc25-general-base.jar']).strip(),flush=True)
output = run(['-s',serial,'shell','CLASSPATH=/data/local/tmp/rc25-general-probes.zip:/data/local/tmp/rc25-general-base.jar',
              'app_process','/','com.genymobile.scrcpy.'+name]+sys.argv[2:],90)
(folder/'out'/(name+'.log')).write_text(output,encoding='utf8')
print(output,flush=True)
