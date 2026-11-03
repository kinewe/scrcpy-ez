"""Record an allowlist of build/ROM properties, excluding account/device secrets."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import json, re, subprocess

adb=research_config.adb()
keys=['ro.product.manufacturer','ro.product.brand','ro.product.model','ro.product.device','ro.build.fingerprint','ro.build.version.sdk','ro.build.version.release','ro.build.version.incremental','ro.mi.os.version.name','ro.mi.os.version.incremental','ro.miui.ui.version.name','ro.miui.ui.version.code','ro.build.version.emui','ro.build.hw_emui_api_level']
devices=research_config.devices()
report=[]
for label,serial in devices:
    data=subprocess.check_output([adb,'-P',research_config.adb_port(),'-s',serial,'shell','getprop'],timeout=15).decode('utf8',errors='replace')
    props=dict(re.findall(r'^\[([^\]]+)\]: \[(.*)\]$',data.replace('\r',''),re.M))
    assert props.get('ro.build.fingerprint') and props.get('ro.build.version.sdk'),label
    report.append(dict(device=label,properties={k:props.get(k,'') for k in keys}))
Path(__file__).with_name('out').joinpath('device-builds.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf8')
print(json.dumps(report,ensure_ascii=False))
