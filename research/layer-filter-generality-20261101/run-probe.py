from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import os, subprocess, sys
folder=Path(__file__).resolve().parent;root=folder.parents[1]
adb=research_config.adb()
label,serial,klass=sys.argv[1:4];assert label.replace('-','').isalnum() and klass in ['DeviceCapabilitiesProbe','NameFilterLab','SceneProbe','PipelineProbe']
port=os.environ.get('RC25_RESEARCH_PORT',research_config.adb_port())
def run(args,timeout=30):return subprocess.run([adb,'-P',port,'-s',serial]+args,capture_output=True,timeout=timeout)
for source,target in [(folder/'out/probes.zip','rc25-scope-probes.zip'),(research_config.build_dir() / 'scrcpy-server','rc25-scope-base.jar')]:
 p=run(['push',str(source),'/data/local/tmp/'+target]);assert p.returncode==0,p.stderr.decode(errors='replace')
p=run(['shell','CLASSPATH=/data/local/tmp/rc25-scope-probes.zip:/data/local/tmp/rc25-scope-base.jar','app_process','/','com.genymobile.scrcpy.'+klass]+sys.argv[4:],600)
log=(p.stdout+p.stderr).decode('utf8',errors='replace');(folder/'out'/(label+'-'+klass+'.log')).write_text(log,encoding='utf8')
print(log if len(log)<6000 else log[-6000:]);sys.exit(p.returncode)
