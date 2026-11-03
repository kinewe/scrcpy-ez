from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import json,re,subprocess,time,shutil
out=Path(__file__).with_name('out');prior=out.parent.parent/'performance-portability-20261101'/'out'
for name in ['k80-alipay-virtual-WindowTreeProbe.log','k80-alipay-virtual-camera-client.json']:
    shutil.copy2(prior/name,out/name)
adb=research_config.adb()
raw=subprocess.check_output([adb,'-P',research_config.adb_port(),'-s',research_config.serial(),'shell','dumpsys','window','windows'],timeout=20).decode('utf8',errors='replace')
sections=re.split(r'(?=  Window #\d+ Window\{)',raw)
selected=[]
for section in sections:
    header=section.splitlines()[0] if section.splitlines() else ''
    if 'com.eg.android.AlipayGphone' in header and 'MainCaptureActivity' in header:
        selected.append([line.strip() for line in section.splitlines() if re.search(r'^  Window #|mDisplayId=|mAttrs=|fmt=|mHasSurface=|isOnScreen=|isVisible=|mViewVisibility=',line)])
report=dict(epoch=time.time(),windows=selected)
(out/'alipay-window-attributes.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf8');print(json.dumps(report,ensure_ascii=False))
