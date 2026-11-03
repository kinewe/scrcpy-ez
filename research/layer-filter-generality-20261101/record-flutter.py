"""Identify installed engine assets and the observed obfuscated surface classes."""
from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import hashlib, importlib.util, json, subprocess, sys, zipfile
folder=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('framework_metadata',folder/'collect-framework.py')
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
serial=sys.argv[1];adb=module.adb
remote=subprocess.check_output([adb,'-P',research_config.adb_port(),'-s',serial,'shell','pm','path','fans.lty.app'],timeout=15).decode().strip()
paths=[line[len('package:'):] for line in remote.splitlines() if line.startswith('package:') and line.endswith('/base.apk')]
assert len(paths)==1 and paths[0].startswith('/data/app/')
apk=folder/'out/v-ciyuan-base.apk'
subprocess.run([adb,'-P',research_config.adb_port(),'-s',serial,'pull',paths[0],str(apk)],check=True,timeout=90)
with zipfile.ZipFile(apk) as z:
    names=z.namelist();engines=[n for n in names if n.endswith('/libflutter.so')]
    assets=sum(n.startswith('assets/flutter_assets/') for n in names)
    classes=[]
    for n in names:
        if n.endswith('.dex'):classes.extend(module.metadata(z.read(n),('Lt6/i;','Lt6/p;')))
report=dict(package='fans.lty.app',apkSHA256=hashlib.sha256(apk.read_bytes()).hexdigest(),flutterLibraries=engines,flutterAssetEntries=assets,observedSurfaceClasses=classes)
(folder/'out/v-ciyuan-engine.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf8')
print(json.dumps(dict(flutterLibraries=engines,flutterAssetEntries=assets,classes=[dict(name=c['class'],super=c['super']) for c in classes])))
