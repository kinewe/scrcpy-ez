"""Remove only the processes, forwards and two files owned by this experiment."""
from pathlib import Path
import json, re
from device_tools import ADB, adb_output, select_serial

folder=Path(__file__).resolve().parent
serial=select_serial()
pattern=re.compile(r'\bcom\.genymobile\.scrcpy\.(?:AppNameFilterProbe|NameFilterLab|GraphicsCapabilitiesProbe)(?:\s|$)')
def owned_processes():
    rows=[]
    for line in adb_output('-s',serial,'shell','ps','-A','-o','USER,PID,ARGS').splitlines()[1:]:
        parts=line.split(None,2)
        if len(parts)==3 and parts[0]=='shell' and parts[1].isdigit() and pattern.search(parts[2]):rows.append(parts[1])
    return rows
killed=owned_processes()
if killed:adb_output('-s',serial,'shell','kill',*killed)
removed=[]
for line in adb_output('forward','--list').splitlines():
    fields=line.split()
    if len(fields)==3 and fields[0]==serial and fields[2].startswith('localabstract:rc25_named_'):
        adb_output('-s',serial,'forward','--remove',fields[1]);removed.append(fields[1])
files=['/data/local/tmp/rc25-general-probes.zip','/data/local/tmp/rc25-general-base.jar']
adb_output('-s',serial,'shell','rm','-f',*files)
remaining=owned_processes()
assert not remaining
for name in files:
    assert adb_output('-s',serial,'shell',f'if test -e {name}; then echo PRESENT; else echo ABSENT; fi')=='ABSENT'
report={'ownedProcessesStopped':len(killed),'ownedProcessesRemaining':0,'ownedForwardsRemoved':len(removed),
        'temporaryDeviceFilesRemoved':files,'normalResearchWindowExit':'0'}
(folder/'out/cleanup.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf8')
print(json.dumps(report,ensure_ascii=False))
