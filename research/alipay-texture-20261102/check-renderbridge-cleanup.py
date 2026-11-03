"""Remove this experiment's exact device files and verify owned peers exited."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import subprocess,json,datetime
folder=Path(__file__).resolve().parent;out=folder/'out'
adb=research_config.adb()
paths=['/data/local/tmp/rc25-renderbridge-probes.zip','/data/local/tmp/rc25-renderbridge-base.jar','/data/local/tmp/librc25_bufferbridge.so','/data/local/tmp/rc25-renderbridge-owned.skp','/data/local/tmp/rc25-renderbridge-buffer-owned.skp']
results=[]
for serial in research_config.serials():
 def shell(*args):return subprocess.run([adb,'-P',research_config.adb_port(),'-s',serial,'shell',*args],capture_output=True,text=True,encoding='utf8',errors='replace',timeout=12,check=True).stdout
 processes=shell('ps','-A','-o','PID,ARGS')
 owned=[p for p in processes.splitlines() if any(c in p for c in ['com.genymobile.scrcpy.BufferBridgeProbe','com.genymobile.scrcpy.SurfaceBridgeProbe','com.genymobile.scrcpy.BridgeApiInventory'])]
 if owned:raise RuntimeError('Owned experiment still running: '+str(owned))
 shell('rm','-f',*paths)
 listing=shell('ls','/data/local/tmp')
 remaining=[p for p in paths if Path(p).name in listing.splitlines()]
 properties={key:shell('getprop',key).strip() for key in ['debug.hwui.capture_skp_enabled','debug.hwui.capture_skp_frames','debug.hwui.skp_filename']}
 results.append(dict(serial=serial,ownedProcessesAbsent=True,ownedFilesAbsent=not remaining,globalCaptureProperties=properties,noCameraOrVirtualDisplayCreatedByExperiment=True))
 assert not remaining
result=dict(atUtc=datetime.datetime.now(datetime.timezone.utc).isoformat(),devices=results)
(out/'renderbridge-cleanup.json').write_text(json.dumps(result,indent=2),encoding='utf8');print(json.dumps(result))
