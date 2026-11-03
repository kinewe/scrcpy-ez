from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import json,os,subprocess,sys
folder=Path(__file__).resolve().parent;root=folder.parents[1]
serial=research_config.serial();baseline=folder/'before-product/scrcpy-server.jar';optimized=research_config.build_dir() / 'scrcpy-server'
adb=research_config.adb()
try:
    for label,server in [('real-a1',baseline),('real-b1',optimized),('real-b2',optimized),('real-a2',baseline)]:
        subprocess.run([sys.executable,str(folder/'profile-run.py'),label,serial,'com.tencent.wework','wire','120','0','awake'],env=dict(os.environ,RC25_PERF_SERVER=str(server)),check=True)
finally:
    subprocess.run([adb,'-P',research_config.adb_port(),'-s',serial,'shell','input','-d','237','keyevent','4'],timeout=20,check=False)
results=[json.loads((folder/'out'/(n+'.json')).read_text(encoding='utf8')) for n in ['real-a1','real-b1','real-b2','real-a2']]
assert sum(r['verifiedWireFrames'] for r in results)==480
(folder/'out/real-ab.json').write_text(json.dumps(results,ensure_ascii=False,indent=2),encoding='utf8');print('REAL_AB_480_CRC_PASS')
